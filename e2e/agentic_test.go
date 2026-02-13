// E2E tests for codex-temporal-go
//
// These tests are self-contained: TestMain starts a Temporal dev server on a
// non-standard port (17233) and an in-process worker. No external services
// need to be running except an LLM provider (OPENAI_API_KEY or ANTHROPIC_API_KEY).
//
// The non-standard port avoids collisions with a dev server on the default 7233.
package e2e

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/mfateev/codex-temporal-go/internal/activities"
	"github.com/mfateev/codex-temporal-go/internal/llm"
	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/tools"
	"github.com/mfateev/codex-temporal-go/internal/tools/handlers"
	"github.com/mfateev/codex-temporal-go/internal/workflow"
)

const (
	TaskQueue       = "codex-temporal"
	TestHostPort    = "localhost:17233" // Non-standard port to avoid collisions
	TestUIPort      = "17234"          // UI port (also non-standard)
	WorkflowTimeout = 3 * time.Minute
	CheapModel      = "gpt-4o-mini"
)

// Package-level state managed by TestMain.
var (
	temporalCmd    *exec.Cmd
	testWorker     worker.Worker
	temporalClient client.Client
)

func TestMain(m *testing.M) {
	// Skip everything if no LLM provider key is set.
	if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("ANTHROPIC_API_KEY") == "" {
		log.Println("E2E: No LLM provider key set (OPENAI_API_KEY or ANTHROPIC_API_KEY), skipping E2E tests")
		os.Exit(0)
	}

	// 1. Find temporal CLI
	temporalBin := findTemporalBin()
	if temporalBin == "" {
		log.Fatal("E2E: temporal CLI not found. Install it or set PATH.")
	}
	log.Printf("E2E: Using temporal CLI: %s", temporalBin)

	// 2. Start Temporal dev server on non-standard port
	temporalCmd = exec.Command(temporalBin, "server", "start-dev",
		"--port", "17233",
		"--ui-port", TestUIPort,
		"--headless",
		"--log-format", "pretty",
	)
	temporalCmd.Stdout = os.Stderr // Send server logs to stderr so they don't interfere with test output
	temporalCmd.Stderr = os.Stderr
	if err := temporalCmd.Start(); err != nil {
		log.Fatalf("E2E: Failed to start Temporal server: %v", err)
	}
	log.Printf("E2E: Temporal server starting (pid %d) on %s", temporalCmd.Process.Pid, TestHostPort)

	// 3. Wait for Temporal server to be ready
	if err := waitForPort("localhost", "17233", 30*time.Second); err != nil {
		temporalCmd.Process.Kill()
		log.Fatalf("E2E: Temporal server failed to start: %v", err)
	}
	log.Println("E2E: Temporal server is ready")

	// 4. Create Temporal client
	var err error
	temporalClient, err = client.Dial(client.Options{HostPort: TestHostPort})
	if err != nil {
		temporalCmd.Process.Kill()
		log.Fatalf("E2E: Failed to create Temporal client: %v", err)
	}

	// 5. Start in-process worker
	testWorker = createWorker(temporalClient)
	go func() {
		if err := testWorker.Run(nil); err != nil {
			log.Printf("E2E: Worker stopped with error: %v", err)
		}
	}()
	// Give the worker a moment to register with the server
	time.Sleep(time.Second)
	log.Println("E2E: Worker started")

	// 6. Run tests
	code := m.Run()

	// 6b. Write E2E passed marker on success
	if code == 0 {
		writeE2EPassedMarker()
	}

	// 7. Tear down
	log.Println("E2E: Tearing down...")
	testWorker.Stop()
	temporalClient.Close()
	temporalCmd.Process.Kill()
	temporalCmd.Wait()
	log.Println("E2E: Done")

	os.Exit(code)
}

// findTemporalBin locates the temporal CLI binary.
func findTemporalBin() string {
	// Check well-known install location
	home, _ := os.UserHomeDir()
	if home != "" {
		candidate := filepath.Join(home, ".temporalio", "bin", "temporal")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	// Fall back to PATH
	if p, err := exec.LookPath("temporal"); err == nil {
		return p
	}
	return ""
}

// writeE2EPassedMarker writes the current HEAD SHA to <repo-root>/.e2e-passed
// so that the e2e-test-gate hook can verify tests passed for this commit.
func writeE2EPassedMarker() {
	rootOut, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		log.Printf("E2E: Failed to find repo root for marker: %v", err)
		return
	}
	root := strings.TrimSpace(string(rootOut))

	shaOut, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		log.Printf("E2E: Failed to get HEAD SHA for marker: %v", err)
		return
	}
	sha := strings.TrimSpace(string(shaOut))

	markerPath := filepath.Join(root, ".e2e-passed")
	if err := os.WriteFile(markerPath, []byte(sha+"\n"), 0644); err != nil {
		log.Printf("E2E: Failed to write marker %s: %v", markerPath, err)
		return
	}
	log.Printf("E2E: Wrote passed marker to %s (SHA: %s)", markerPath, sha)
}

// waitForPort polls a TCP port until it accepts connections or the timeout expires.
func waitForPort(host, port string, timeout time.Duration) error {
	deadline := time.After(timeout)
	addr := net.JoinHostPort(host, port)
	for {
		select {
		case <-deadline:
			return fmt.Errorf("timed out waiting for %s", addr)
		default:
			conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
			if err == nil {
				conn.Close()
				return nil
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
}

// createWorker builds a Temporal worker with all workflows and activities
// registered, matching the setup in cmd/worker/main.go.
func createWorker(c client.Client) worker.Worker {
	w := worker.New(c, TaskQueue, worker.Options{})

	// Register workflows
	w.RegisterWorkflow(workflow.AgenticWorkflow)
	w.RegisterWorkflow(workflow.AgenticWorkflowContinued)

	// Create tool registry with all built-in tools
	toolRegistry := tools.NewToolRegistry()
	toolRegistry.Register(handlers.NewShellTool())
	toolRegistry.Register(handlers.NewReadFileTool())
	toolRegistry.Register(handlers.NewWriteFileTool())
	toolRegistry.Register(handlers.NewListDirTool())
	toolRegistry.Register(handlers.NewGrepFilesTool())
	toolRegistry.Register(handlers.NewApplyPatchTool())

	// Create multi-provider LLM client
	llmClient := llm.NewMultiProviderClient()

	// Register activities
	llmActivities := activities.NewLLMActivities(llmClient)
	w.RegisterActivity(llmActivities.ExecuteLLMCall)
	w.RegisterActivity(llmActivities.ExecuteCompact)
	w.RegisterActivity(llmActivities.GenerateSuggestions)

	toolActivities := activities.NewToolActivities(toolRegistry)
	w.RegisterActivity(toolActivities.ExecuteTool)

	instructionActivities := activities.NewInstructionActivities()
	w.RegisterActivity(instructionActivities.LoadWorkerInstructions)
	w.RegisterActivity(instructionActivities.LoadExecPolicy)

	return w
}

// --- Test helpers ---

// testSessionConfig returns a deterministic session configuration for testing.
// Temperature 0 makes LLM responses reproducible. Suggestions are disabled by
// default to avoid extra API calls; tests that exercise suggestions should
// override DisableSuggestions.
func testSessionConfig(maxTokens int, tools models.ToolsConfig) models.SessionConfiguration {
	return models.SessionConfiguration{
		Model: models.ModelConfig{
			Model:         CheapModel,
			Temperature:   0,
			MaxTokens:     maxTokens,
			ContextWindow: 128000,
		},
		Tools:              tools,
		DisableSuggestions: true,
	}
}

// dialTemporal returns the shared Temporal client, skipping the test if
// prerequisites are missing.
func dialTemporal(t *testing.T) client.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set, skipping E2E test")
	}
	return temporalClient
}

// waitForTurnComplete polls the get_conversation_items query until the expected
// number of TurnComplete markers appear, then returns the full history.
func waitForTurnComplete(t *testing.T, ctx context.Context, c client.Client, workflowID string, expectedTurnCount int) []models.ConversationItem {
	t.Helper()
	deadline := time.After(2 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Fatalf("Timed out waiting for %d TurnComplete markers", expectedTurnCount)
		case <-ctx.Done():
			t.Fatalf("Context cancelled waiting for TurnComplete")
		case <-ticker.C:
			resp, err := c.QueryWorkflow(ctx, workflowID, "", workflow.QueryGetConversationItems)
			if err != nil {
				t.Logf("Query failed (may retry): %v", err)
				continue
			}
			var items []models.ConversationItem
			if err := resp.Get(&items); err != nil {
				t.Logf("Decode failed (may retry): %v", err)
				continue
			}

			turnCompleteCount := 0
			for _, item := range items {
				if item.Type == models.ItemTypeTurnComplete {
					turnCompleteCount++
				}
			}
			t.Logf("History has %d items, %d TurnComplete markers (need %d)",
				len(items), turnCompleteCount, expectedTurnCount)

			if turnCompleteCount >= expectedTurnCount {
				return items
			}
		}
	}
}

// shutdownWorkflow sends a shutdown Update and waits for the workflow to complete.
func shutdownWorkflow(t *testing.T, ctx context.Context, c client.Client, workflowID string) workflow.WorkflowResult {
	t.Helper()

	updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   workflowID,
		UpdateName:   workflow.UpdateShutdown,
		Args:         []interface{}{workflow.ShutdownRequest{}},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	require.NoError(t, err, "Failed to send shutdown")

	var shutdownResp workflow.ShutdownResponse
	require.NoError(t, updateHandle.Get(ctx, &shutdownResp))
	assert.True(t, shutdownResp.Acknowledged)

	// Wait for workflow to complete
	run := c.GetWorkflow(ctx, workflowID, "")
	var result workflow.WorkflowResult
	require.NoError(t, run.Get(ctx, &result), "Workflow should complete after shutdown")
	return result
}

// startWorkflow starts a workflow and returns the workflow ID.
func startWorkflow(t *testing.T, ctx context.Context, c client.Client, input workflow.WorkflowInput) string {
	t.Helper()
	workflowID := input.ConversationID
	_, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: workflowID, TaskQueue: TaskQueue,
	}, "AgenticWorkflow", input)
	require.NoError(t, err, "Failed to start workflow")
	return workflowID
}

// waitForCompactionAndTurnComplete polls until the history contains both an
// ItemTypeCompaction marker and at least one TurnComplete. This is used after
// sending a second user input that triggers compaction — ReplaceAll wipes the
// old history so we can't count cumulative TurnComplete markers.
func waitForCompactionAndTurnComplete(t *testing.T, ctx context.Context, c client.Client, workflowID string) []models.ConversationItem {
	t.Helper()
	deadline := time.After(2 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Fatalf("Timed out waiting for compaction + TurnComplete")
		case <-ctx.Done():
			t.Fatalf("Context cancelled")
		case <-ticker.C:
			resp, err := c.QueryWorkflow(ctx, workflowID, "", workflow.QueryGetConversationItems)
			if err != nil {
				continue
			}
			var items []models.ConversationItem
			if err := resp.Get(&items); err != nil {
				continue
			}

			hasCompaction := false
			hasTurnComplete := false
			for _, item := range items {
				if item.Type == models.ItemTypeCompaction {
					hasCompaction = true
				}
				if item.Type == models.ItemTypeTurnComplete {
					hasTurnComplete = true
				}
			}
			t.Logf("History: %d items, compaction=%v, turnComplete=%v",
				len(items), hasCompaction, hasTurnComplete)

			if hasCompaction && hasTurnComplete {
				return items
			}
		}
	}
}

// --- Tests ---

// TestAgenticWorkflow_SingleTurn tests a simple conversation without tools
func TestAgenticWorkflow_SingleTurn(t *testing.T) {
	c := dialTemporal(t)

	workflowID := "test-single-turn-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage:    "Say hello in exactly 3 words. Do not use any tools.",
		Config: testSessionConfig(100, models.ToolsConfig{
			EnableShell:    false,
			EnableReadFile: false,
		}),
	}

	t.Logf("Starting workflow: %s", workflowID)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	startWorkflow(t, ctx, c, input)

	// Wait for the first turn to complete
	waitForTurnComplete(t, ctx, c, workflowID, 1)

	// Send shutdown and get result
	result := shutdownWorkflow(t, ctx, c, workflowID)

	assert.Equal(t, workflowID, result.ConversationID)
	assert.Greater(t, result.TotalTokens, 0, "Should have consumed tokens")
	assert.Empty(t, result.ToolCallsExecuted, "Should not have called any tools")
	assert.Equal(t, "shutdown", result.EndReason)

	t.Logf("Total tokens: %d, Iterations: %d", result.TotalTokens, result.TotalIterations)
}

// TestAgenticWorkflow_WithShellTool tests LLM calling the shell tool
func TestAgenticWorkflow_WithShellTool(t *testing.T) {
	c := dialTemporal(t)

	workflowID := "test-shell-tool-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage: "You MUST use the shell tool to execute this exact command: echo 'Hello from shell test'. " +
			"Do NOT answer without calling the shell tool first. After getting the result, report the output.",
		Config: testSessionConfig(500, models.ToolsConfig{
			EnableShell:    true,
			EnableReadFile: false,
		}),
	}

	t.Logf("Starting workflow: %s", workflowID)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	startWorkflow(t, ctx, c, input)
	waitForTurnComplete(t, ctx, c, workflowID, 1)
	result := shutdownWorkflow(t, ctx, c, workflowID)

	assert.Equal(t, workflowID, result.ConversationID)
	assert.Greater(t, result.TotalTokens, 0, "Should have consumed tokens")
	assert.Contains(t, result.ToolCallsExecuted, "shell", "Should have called shell tool")

	t.Logf("Total tokens: %d, Iterations: %d, Tools: %v",
		result.TotalTokens, result.TotalIterations, result.ToolCallsExecuted)
}

// TestAgenticWorkflow_MultiTurn tests a multi-turn conversation with tools
func TestAgenticWorkflow_MultiTurn(t *testing.T) {
	c := dialTemporal(t)

	workflowID := "test-multi-turn-" + uuid.New().String()[:8]
	testFile := "/tmp/codex-test-" + uuid.New().String()[:8] + ".txt"
	defer os.Remove(testFile)

	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage: "Complete these steps in order. You MUST use the tools provided.\n" +
			"Step 1: Use the shell tool to run: echo 'Test content' > " + testFile + "\n" +
			"Step 2: After the shell command succeeds, use the read_file tool to read " + testFile + "\n" +
			"Step 3: Report what read_file returned.",
		Config: testSessionConfig(1000, models.ToolsConfig{
			EnableShell:    true,
			EnableReadFile: true,
		}),
	}

	t.Logf("Starting workflow: %s", workflowID)
	t.Logf("Test file: %s", testFile)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	startWorkflow(t, ctx, c, input)
	waitForTurnComplete(t, ctx, c, workflowID, 1)
	result := shutdownWorkflow(t, ctx, c, workflowID)

	assert.Equal(t, workflowID, result.ConversationID)
	assert.Greater(t, result.TotalTokens, 0, "Should have consumed tokens")
	assert.Contains(t, result.ToolCallsExecuted, "shell", "Should have called shell tool")
	assert.Contains(t, result.ToolCallsExecuted, "read_file", "Should have called read_file tool")

	t.Logf("Total tokens: %d, Iterations: %d, Tools: %v",
		result.TotalTokens, result.TotalIterations, result.ToolCallsExecuted)
}

// TestAgenticWorkflow_ReadFile tests the read_file tool specifically
func TestAgenticWorkflow_ReadFile(t *testing.T) {
	c := dialTemporal(t)

	testFile := "/tmp/codex-read-test-" + uuid.New().String()[:8] + ".txt"
	testContent := "Line 1: Hello\nLine 2: World\nLine 3: Test\n"
	err := os.WriteFile(testFile, []byte(testContent), 0644)
	require.NoError(t, err, "Failed to create test file")
	defer os.Remove(testFile)

	workflowID := "test-read-file-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage: "You MUST use the read_file tool to read the file at path " + testFile + ". " +
			"Do NOT answer without calling read_file first. After reading, tell me how many lines it has.",
		Config: testSessionConfig(500, models.ToolsConfig{
			EnableShell:    false,
			EnableReadFile: true,
		}),
	}

	t.Logf("Starting workflow: %s", workflowID)
	t.Logf("Test file: %s", testFile)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	startWorkflow(t, ctx, c, input)
	waitForTurnComplete(t, ctx, c, workflowID, 1)
	result := shutdownWorkflow(t, ctx, c, workflowID)

	assert.Equal(t, workflowID, result.ConversationID)
	assert.Greater(t, result.TotalTokens, 0, "Should have consumed tokens")
	assert.Contains(t, result.ToolCallsExecuted, "read_file", "Should have called read_file tool")

	t.Logf("Total tokens: %d, Iterations: %d, Tools: %v",
		result.TotalTokens, result.TotalIterations, result.ToolCallsExecuted)
}

// TestAgenticWorkflow_ListDir tests the list_dir tool
func TestAgenticWorkflow_ListDir(t *testing.T) {
	c := dialTemporal(t)

	testDir := "/tmp/codex-listdir-test-" + uuid.New().String()[:8]
	require.NoError(t, os.MkdirAll(filepath.Join(testDir, "subdir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "hello.txt"), []byte("hello"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "subdir", "nested.txt"), []byte("nested"), 0o644))
	defer os.RemoveAll(testDir)

	workflowID := "test-list-dir-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage: "You MUST use the list_dir tool to list the directory at " + testDir + ". " +
			"Do NOT use any other tool. After listing, report the entries you see.",
		Config: testSessionConfig(500, models.ToolsConfig{
			EnableShell:    false,
			EnableReadFile: false,
			EnableListDir:  true,
		}),
	}

	t.Logf("Starting workflow: %s", workflowID)
	t.Logf("Test dir: %s", testDir)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	startWorkflow(t, ctx, c, input)
	waitForTurnComplete(t, ctx, c, workflowID, 1)
	result := shutdownWorkflow(t, ctx, c, workflowID)

	assert.Equal(t, workflowID, result.ConversationID)
	assert.Greater(t, result.TotalTokens, 0, "Should have consumed tokens")
	assert.Contains(t, result.ToolCallsExecuted, "list_dir", "Should have called list_dir tool")

	t.Logf("Total tokens: %d, Iterations: %d, Tools: %v",
		result.TotalTokens, result.TotalIterations, result.ToolCallsExecuted)
}

// TestAgenticWorkflow_GrepFiles tests the grep_files tool
func TestAgenticWorkflow_GrepFiles(t *testing.T) {
	c := dialTemporal(t)

	testDir := "/tmp/codex-grep-test-" + uuid.New().String()[:8]
	require.NoError(t, os.MkdirAll(testDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "match.txt"), []byte("hello needle world"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "miss.txt"), []byte("no match here"), 0o644))
	defer os.RemoveAll(testDir)

	workflowID := "test-grep-files-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage: "You MUST use the grep_files tool to search for the pattern 'needle' in the directory " + testDir + ". " +
			"Do NOT use any other tool. After searching, report which files matched.",
		Config: testSessionConfig(500, models.ToolsConfig{
			EnableShell:     false,
			EnableReadFile:  false,
			EnableGrepFiles: true,
		}),
	}

	t.Logf("Starting workflow: %s", workflowID)
	t.Logf("Test dir: %s", testDir)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	startWorkflow(t, ctx, c, input)
	waitForTurnComplete(t, ctx, c, workflowID, 1)
	result := shutdownWorkflow(t, ctx, c, workflowID)

	assert.Equal(t, workflowID, result.ConversationID)
	assert.Greater(t, result.TotalTokens, 0, "Should have consumed tokens")
	assert.Contains(t, result.ToolCallsExecuted, "grep_files", "Should have called grep_files tool")

	t.Logf("Total tokens: %d, Iterations: %d, Tools: %v",
		result.TotalTokens, result.TotalIterations, result.ToolCallsExecuted)
}

// TestAgenticWorkflow_WriteFile tests the write_file tool
func TestAgenticWorkflow_WriteFile(t *testing.T) {
	c := dialTemporal(t)

	testFile := "/tmp/codex-write-test-" + uuid.New().String()[:8] + ".txt"
	defer os.Remove(testFile)

	workflowID := "test-write-file-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage: "You MUST use the write_file tool to create a file at " + testFile + " with the content 'Hello from write_file'. " +
			"Do NOT use any other tool. After writing, report what you did.",
		Config: testSessionConfig(500, models.ToolsConfig{
			EnableShell:      false,
			EnableReadFile:   true,
			EnableWriteFile:  true,
			EnableApplyPatch: false,
		}),
	}

	t.Logf("Starting workflow: %s", workflowID)
	t.Logf("Test file: %s", testFile)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	startWorkflow(t, ctx, c, input)
	waitForTurnComplete(t, ctx, c, workflowID, 1)
	result := shutdownWorkflow(t, ctx, c, workflowID)

	assert.Equal(t, workflowID, result.ConversationID)
	assert.Greater(t, result.TotalTokens, 0, "Should have consumed tokens")
	assert.Contains(t, result.ToolCallsExecuted, "write_file", "Should have called write_file tool")

	// Verify file was created with expected content
	contents, err := os.ReadFile(testFile)
	if err == nil {
		t.Logf("File contents: %q", string(contents))
		assert.Contains(t, string(contents), "Hello from write_file")
	} else {
		t.Logf("Note: file not found at %s (LLM may have used a different path)", testFile)
	}

	t.Logf("Total tokens: %d, Iterations: %d, Tools: %v",
		result.TotalTokens, result.TotalIterations, result.ToolCallsExecuted)
}

// TestAgenticWorkflow_ApplyPatch tests the apply_patch tool
func TestAgenticWorkflow_ApplyPatch(t *testing.T) {
	c := dialTemporal(t)

	testFile := "/tmp/codex-patch-test-" + uuid.New().String()[:8] + ".txt"
	defer os.Remove(testFile)

	workflowID := "test-apply-patch-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage: "You MUST use the apply_patch tool to create a new file at " + testFile + " with the content 'Hello from apply_patch'. " +
			"Use the *** Add File syntax. Do NOT use any other tool. After the patch is applied, report the result.",
		Config: testSessionConfig(1000, models.ToolsConfig{
			EnableShell:      false,
			EnableReadFile:   false,
			EnableApplyPatch: true,
		}),
	}

	t.Logf("Starting workflow: %s", workflowID)
	t.Logf("Test file: %s", testFile)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	startWorkflow(t, ctx, c, input)
	waitForTurnComplete(t, ctx, c, workflowID, 1)
	result := shutdownWorkflow(t, ctx, c, workflowID)

	assert.Equal(t, workflowID, result.ConversationID)
	assert.Greater(t, result.TotalTokens, 0, "Should have consumed tokens")
	assert.Contains(t, result.ToolCallsExecuted, "apply_patch", "Should have called apply_patch tool")

	// Verify file was created with expected content
	contents, err := os.ReadFile(testFile)
	if err == nil {
		t.Logf("File contents: %q", string(contents))
		assert.Contains(t, string(contents), "Hello from apply_patch")
	} else {
		t.Logf("Note: file not found at %s (LLM may have used a different path)", testFile)
	}

	t.Logf("Total tokens: %d, Iterations: %d, Tools: %v",
		result.TotalTokens, result.TotalIterations, result.ToolCallsExecuted)
}

// TestAgenticWorkflow_QueryHistory tests the get_conversation_items query handler
func TestAgenticWorkflow_QueryHistory(t *testing.T) {
	c := dialTemporal(t)

	workflowID := "test-query-history-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage:    "Say 'hello world'. Do not use any tools.",
		Config: testSessionConfig(100, models.ToolsConfig{
			EnableShell:    false,
			EnableReadFile: false,
		}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	startWorkflow(t, ctx, c, input)
	items := waitForTurnComplete(t, ctx, c, workflowID, 1)

	// Verify history structure
	require.GreaterOrEqual(t, len(items), 4, "Should have TurnStarted + UserMessage + AssistantMessage + TurnComplete")

	// Check for TurnStarted
	assert.Equal(t, models.ItemTypeTurnStarted, items[0].Type)
	assert.NotEmpty(t, items[0].TurnID)

	// Check for UserMessage
	assert.Equal(t, models.ItemTypeUserMessage, items[1].Type)
	assert.Contains(t, items[1].Content, "hello world")

	// Find TurnComplete
	lastItem := items[len(items)-1]
	assert.Equal(t, models.ItemTypeTurnComplete, lastItem.Type)
	assert.NotEmpty(t, lastItem.TurnID)

	// Clean up
	shutdownWorkflow(t, ctx, c, workflowID)

	t.Logf("History has %d items", len(items))
}

// TestAgenticWorkflow_MultiTurnInteractive tests sending a second user message
func TestAgenticWorkflow_MultiTurnInteractive(t *testing.T) {
	c := dialTemporal(t)

	workflowID := "test-multi-interactive-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage:    "What is 2 + 2? Answer with just the number. Do not use any tools.",
		Config: testSessionConfig(100, models.ToolsConfig{
			EnableShell:    false,
			EnableReadFile: false,
		}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	// Start and wait for first turn
	startWorkflow(t, ctx, c, input)
	waitForTurnComplete(t, ctx, c, workflowID, 1)

	// Send a second message
	updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   workflowID,
		UpdateName:   workflow.UpdateUserInput,
		Args:         []interface{}{workflow.UserInput{Content: "Now what is 3 + 3? Answer with just the number."}},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	require.NoError(t, err)

	var accepted workflow.UserInputAccepted
	require.NoError(t, updateHandle.Get(ctx, &accepted))
	assert.NotEmpty(t, accepted.TurnID)
	t.Logf("Second turn ID: %s", accepted.TurnID)

	// Wait for second turn
	waitForTurnComplete(t, ctx, c, workflowID, 2)

	// Shutdown and verify
	result := shutdownWorkflow(t, ctx, c, workflowID)
	assert.Equal(t, "shutdown", result.EndReason)
	assert.Greater(t, result.TotalTokens, 0)

	t.Logf("Total tokens: %d", result.TotalTokens)
}

// TestAgenticWorkflow_Shutdown tests clean shutdown
func TestAgenticWorkflow_Shutdown(t *testing.T) {
	c := dialTemporal(t)

	workflowID := "test-shutdown-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage:    "Say 'goodbye'. Do not use any tools.",
		Config: testSessionConfig(100, models.ToolsConfig{
			EnableShell:    false,
			EnableReadFile: false,
		}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	startWorkflow(t, ctx, c, input)
	waitForTurnComplete(t, ctx, c, workflowID, 1)
	result := shutdownWorkflow(t, ctx, c, workflowID)

	assert.Equal(t, workflowID, result.ConversationID)
	assert.Equal(t, "shutdown", result.EndReason)
	assert.Greater(t, result.TotalTokens, 0)

	t.Logf("Total tokens: %d, EndReason: %s", result.TotalTokens, result.EndReason)
}

// TestAgenticWorkflow_AnthropicProvider tests using Anthropic Claude models
func TestAgenticWorkflow_AnthropicProvider(t *testing.T) {
	c := dialTemporal(t)

	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping Anthropic E2E test")
	}

	workflowID := "test-anthropic-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage:    "Say hello in exactly 3 words. Do not use any tools.",
		Config: models.SessionConfiguration{
			Model: models.ModelConfig{
				Provider:      "anthropic",
				Model:         "claude-sonnet-4.5-20250929",
				Temperature:   0,
				MaxTokens:     100,
				ContextWindow: 200000,
			},
			Tools: models.ToolsConfig{
				EnableShell:    false,
				EnableReadFile: false,
			},
		},
	}

	t.Logf("Starting Anthropic workflow: %s", workflowID)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	startWorkflow(t, ctx, c, input)

	// Wait for the first turn to complete
	waitForTurnComplete(t, ctx, c, workflowID, 1)

	// Send shutdown and get result
	result := shutdownWorkflow(t, ctx, c, workflowID)

	assert.Equal(t, workflowID, result.ConversationID)
	assert.Greater(t, result.TotalTokens, 0, "Should have consumed tokens")
	assert.Empty(t, result.ToolCallsExecuted, "Should not have called any tools")
	assert.Equal(t, "shutdown", result.EndReason)

	t.Logf("Anthropic - Total tokens: %d, Iterations: %d", result.TotalTokens, result.TotalIterations)
}

// TestAgenticWorkflow_AnthropicWithTools tests Anthropic with tool calling
func TestAgenticWorkflow_AnthropicWithTools(t *testing.T) {
	c := dialTemporal(t)

	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping Anthropic E2E test")
	}

	workflowID := "test-anthropic-tools-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage:    "Run 'echo hello world' using the shell tool and tell me the output.",
		Config: models.SessionConfiguration{
			Model: models.ModelConfig{
				Provider:      "anthropic",
				Model:         "claude-haiku-4.5-20251001", // Use cheaper Haiku model for tool testing
				Temperature:   0,
				MaxTokens:     1000,
				ContextWindow: 200000,
			},
			Tools: models.ToolsConfig{
				EnableShell:      true,
				EnableReadFile:   false,
				EnableWriteFile:  false,
				EnableListDir:    false,
				EnableGrepFiles:  false,
				EnableApplyPatch: false,
			},
		},
	}

	t.Logf("Starting Anthropic workflow with tools: %s", workflowID)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	startWorkflow(t, ctx, c, input)

	// Wait for the turn to complete (LLM -> tool -> LLM)
	waitForTurnComplete(t, ctx, c, workflowID, 1)

	// Send shutdown and get result
	result := shutdownWorkflow(t, ctx, c, workflowID)

	assert.Equal(t, workflowID, result.ConversationID)
	assert.Greater(t, result.TotalTokens, 0, "Should have consumed tokens")
	assert.Contains(t, result.ToolCallsExecuted, "shell", "Should have called shell tool")
	assert.Equal(t, "shutdown", result.EndReason)

	t.Logf("Anthropic with tools - Total tokens: %d, Iterations: %d, Tools: %v",
		result.TotalTokens, result.TotalIterations, result.ToolCallsExecuted)
}

// TestAgenticWorkflow_ProactiveCompaction verifies that proactive context compaction
// fires when the conversation history exceeds AutoCompactTokenLimit. Uses a prompt
// that generates a long response to build up history, then a very low token limit
// to trigger compaction on the second turn. Verifies the conversation continues
// successfully with a compaction marker in history.
func TestAgenticWorkflow_ProactiveCompaction(t *testing.T) {
	c := dialTemporal(t)

	workflowID := "test-compaction-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		// Ask for a long response so history accumulates enough tokens to exceed the limit.
		// At ~4 chars/token, 2000 chars ≈ 500 tokens in the history estimate.
		UserMessage: "Write a detailed paragraph (at least 300 words) explaining how photosynthesis works. " +
			"Include the light reactions, Calvin cycle, and the role of chlorophyll. Do not use any tools.",
		Config: models.SessionConfiguration{
			Model: models.ModelConfig{
				Model:         CheapModel,
				Temperature:   0,
				MaxTokens:     2000, // Allow a long response
				ContextWindow: 128000,
			},
			Tools: models.ToolsConfig{
				EnableShell:    false,
				EnableReadFile: false,
			},
			// Set limit low enough that a ~300-word response exceeds it.
			// 300 words ≈ 1500 chars ≈ 375 tokens + prompt ≈ 500+ tokens total.
			AutoCompactTokenLimit: 200,
		},
	}

	t.Logf("Starting compaction test workflow: %s", workflowID)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	startWorkflow(t, ctx, c, input)

	// Wait for the first turn to complete
	items := waitForTurnComplete(t, ctx, c, workflowID, 1)
	t.Logf("After turn 1: %d history items", len(items))

	// Log the estimated history size for debugging
	totalChars := 0
	for _, item := range items {
		totalChars += len(item.Content)
	}
	t.Logf("Estimated history tokens: ~%d (chars: %d)", totalChars/4, totalChars)

	// Send a second user message — this should trigger proactive compaction
	// because the history from turn 1 exceeds the 200-token limit.
	updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   workflowID,
		UpdateName:   workflow.UpdateUserInput,
		Args:         []interface{}{workflow.UserInput{Content: "Now summarize photosynthesis in exactly one sentence."}},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	require.NoError(t, err, "Failed to send second user input")

	var accepted workflow.UserInputAccepted
	require.NoError(t, updateHandle.Get(ctx, &accepted))
	t.Logf("Second input accepted, turn ID: %s", accepted.TurnID)

	// Wait until we see both a compaction marker AND a TurnComplete in history.
	// Compaction via ReplaceAll wipes the old history (including turn 1's
	// TurnComplete), so we poll until the compacted history contains a fresh
	// TurnComplete from turn 2.
	items = waitForCompactionAndTurnComplete(t, ctx, c, workflowID)
	t.Logf("After turn 2 with compaction: %d history items", len(items))

	// Check that compaction happened
	hasCompaction := false
	for _, item := range items {
		if item.Type == models.ItemTypeCompaction {
			hasCompaction = true
			t.Logf("Found compaction marker: %q", item.Content)
			break
		}
	}
	assert.True(t, hasCompaction, "History should contain a compaction marker after proactive compaction")

	// Verify the conversation still works — the LLM should have answered
	hasAssistantReply := false
	for _, item := range items {
		if item.Type == models.ItemTypeAssistantMessage && item.Content != "" {
			hasAssistantReply = true
		}
	}
	assert.True(t, hasAssistantReply, "LLM should have produced a response after compaction")

	// Shutdown and verify result
	result := shutdownWorkflow(t, ctx, c, workflowID)
	assert.Equal(t, "shutdown", result.EndReason)
	assert.Greater(t, result.TotalTokens, 0, "Should have consumed tokens")

	t.Logf("Compaction test - Total tokens: %d, History items: %d", result.TotalTokens, len(items))
}

// TestAgenticWorkflow_ManualCompact verifies the /compact command flow end-to-end.
// Steps:
//  1. Start a conversation and wait for the first turn to complete.
//  2. Send UpdateCompact to trigger manual compaction.
//  3. Wait for compaction marker to appear in history.
//  4. Send another user message to verify the workflow resumes normally.
//  5. Shutdown and verify result.
func TestAgenticWorkflow_ManualCompact(t *testing.T) {
	c := dialTemporal(t)

	workflowID := "test-manual-compact-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		// Generate enough content for compaction to have something to work with.
		UserMessage: "Write a short paragraph (at least 100 words) about the importance of testing software. Do not use any tools.",
		Config: testSessionConfig(1000, models.ToolsConfig{
			EnableShell:    false,
			EnableReadFile: false,
		}),
	}

	t.Logf("Starting manual compaction test: %s", workflowID)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	startWorkflow(t, ctx, c, input)

	// 1. Wait for the first turn to complete
	waitForTurnComplete(t, ctx, c, workflowID, 1)
	t.Log("Turn 1 complete")

	// 2. Send UpdateCompact
	updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   workflowID,
		UpdateName:   workflow.UpdateCompact,
		Args:         []interface{}{workflow.CompactRequest{}},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	require.NoError(t, err, "Failed to send compact update")

	var compactResp workflow.CompactResponse
	require.NoError(t, updateHandle.Get(ctx, &compactResp))
	assert.True(t, compactResp.Acknowledged, "Compact should be acknowledged")
	t.Log("Compact update acknowledged")

	// 3. Wait for compaction marker in history
	deadline := time.After(time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var items []models.ConversationItem
	compactionFound := false
	for !compactionFound {
		select {
		case <-deadline:
			t.Fatal("Timed out waiting for compaction marker")
		case <-ctx.Done():
			t.Fatal("Context cancelled")
		case <-ticker.C:
			resp, err := c.QueryWorkflow(ctx, workflowID, "", workflow.QueryGetConversationItems)
			if err != nil {
				continue
			}
			if err := resp.Get(&items); err != nil {
				continue
			}
			for _, item := range items {
				if item.Type == models.ItemTypeCompaction {
					compactionFound = true
					t.Logf("Found compaction marker: %q", item.Content[:min(50, len(item.Content))])
					break
				}
			}
		}
	}

	// 4. Send another user message to verify workflow resumes
	updateHandle2, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   workflowID,
		UpdateName:   workflow.UpdateUserInput,
		Args:         []interface{}{workflow.UserInput{Content: "What is 2+2? Answer with just the number."}},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	require.NoError(t, err, "Failed to send second user input")

	var accepted workflow.UserInputAccepted
	require.NoError(t, updateHandle2.Get(ctx, &accepted))
	t.Logf("Second input accepted, turn ID: %s", accepted.TurnID)

	// Wait for the post-compaction turn to complete.
	// After compaction ReplaceAll, old TurnComplete markers are gone, so look
	// for at least 1 TurnComplete in the compacted history.
	waitForTurnComplete(t, ctx, c, workflowID, 1)

	// 5. Shutdown and verify
	result := shutdownWorkflow(t, ctx, c, workflowID)
	assert.Equal(t, "shutdown", result.EndReason)
	assert.Greater(t, result.TotalTokens, 0)

	t.Logf("Manual compact test - Total tokens: %d", result.TotalTokens)
}

// TestAgenticWorkflow_SpawnAndWait tests the subagent collaboration flow:
// Parent spawns an explorer child to answer a question, waits for completion,
// and reports the result. Verifies child workflow appears and results flow back.
func TestAgenticWorkflow_SpawnAndWait(t *testing.T) {
	c := dialTemporal(t)

	workflowID := "test-spawn-wait-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage: "You have access to agent collaboration tools. " +
			"Use spawn_agent to spawn an explorer agent with the message 'What is 2+2? Answer with just the number.' " +
			"Then use the wait tool to wait for the agent to complete. " +
			"Finally, report what the agent returned.",
		Config: models.SessionConfiguration{
			Model: models.ModelConfig{
				Model:         CheapModel,
				Temperature:   0,
				MaxTokens:     1000,
				ContextWindow: 128000,
			},
			Tools: models.ToolsConfig{
				EnableShell:    false,
				EnableReadFile: false,
				EnableCollab:   true,
			},
		},
	}

	t.Logf("Starting spawn-and-wait workflow: %s", workflowID)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	startWorkflow(t, ctx, c, input)
	waitForTurnComplete(t, ctx, c, workflowID, 1)
	result := shutdownWorkflow(t, ctx, c, workflowID)

	assert.Equal(t, workflowID, result.ConversationID)
	assert.Greater(t, result.TotalTokens, 0, "Should have consumed tokens")
	assert.Equal(t, "shutdown", result.EndReason)

	// Query history and look for spawn_agent and wait tool calls
	resp, err := c.QueryWorkflow(ctx, workflowID, "", workflow.QueryGetConversationItems)
	require.NoError(t, err)
	var items []models.ConversationItem
	require.NoError(t, resp.Get(&items))

	hasSpawnCall := false
	hasWaitCall := false
	for _, item := range items {
		if item.Type == models.ItemTypeFunctionCall {
			t.Logf("Tool call: %s (call_id: %s)", item.Name, item.CallID)
			if item.Name == "spawn_agent" {
				hasSpawnCall = true
			}
			if item.Name == "wait" {
				hasWaitCall = true
			}
		}
		if item.Type == models.ItemTypeFunctionCallOutput {
			t.Logf("Tool output (call_id: %s): %s", item.CallID, truncateStr(item.Output.Content, 200))
		}
	}

	assert.True(t, hasSpawnCall, "LLM should have called spawn_agent")
	// wait is optional — the LLM may or may not call it depending on how it interprets the results

	t.Logf("Spawn-and-wait test - Total tokens: %d, spawn_agent: %v, wait: %v",
		result.TotalTokens, hasSpawnCall, hasWaitCall)
}

// TestAgenticWorkflow_PlanMode tests the plan_request Update:
// 1. Start a parent workflow
// 2. Send plan_request Update to spawn a planner child
// 3. Interact with the planner child directly via user_input and get_conversation_items
// 4. Shutdown the planner child
// 5. Verify the plan text flows back from the child
func TestAgenticWorkflow_PlanMode(t *testing.T) {
	c := dialTemporal(t)

	workflowID := "test-plan-mode-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage:    "Say hello and wait for further instructions.",
		Config: models.SessionConfiguration{
			Model: models.ModelConfig{
				Model:         CheapModel,
				Temperature:   0,
				MaxTokens:     500,
				ContextWindow: 128000,
			},
			Tools: models.ToolsConfig{
				EnableShell:    true,
				EnableReadFile: true,
				EnableCollab:   true,
			},
		},
	}

	t.Logf("Starting plan mode test workflow: %s", workflowID)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	// 1. Start parent workflow and wait for initial turn
	startWorkflow(t, ctx, c, input)
	waitForTurnComplete(t, ctx, c, workflowID, 1)
	t.Log("Parent initial turn complete")

	// 2. Send plan_request to spawn planner child
	updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   workflowID,
		UpdateName:   workflow.UpdatePlanRequest,
		Args:         []interface{}{workflow.PlanRequest{Message: "What is 2+2? Answer with just the number."}},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	require.NoError(t, err, "Failed to send plan_request")

	var accepted workflow.PlanRequestAccepted
	require.NoError(t, updateHandle.Get(ctx, &accepted))
	assert.NotEmpty(t, accepted.AgentID, "agent ID should be set")
	assert.NotEmpty(t, accepted.WorkflowID, "workflow ID should be set")
	t.Logf("Planner child spawned: agent_id=%s, workflow_id=%s", accepted.AgentID, accepted.WorkflowID)

	// 3. Wait for planner child to complete its turn
	childWfID := accepted.WorkflowID
	waitForTurnComplete(t, ctx, c, childWfID, 1)
	t.Log("Planner child turn complete")

	// 4. Query planner child's conversation items directly
	resp, err := c.QueryWorkflow(ctx, childWfID, "", workflow.QueryGetConversationItems)
	require.NoError(t, err)
	var childItems []models.ConversationItem
	require.NoError(t, resp.Get(&childItems))
	t.Logf("Planner child has %d conversation items", len(childItems))

	// Verify planner has assistant response
	hasAssistant := false
	for _, item := range childItems {
		if item.Type == models.ItemTypeAssistantMessage && item.Content != "" {
			t.Logf("Planner response: %s", truncateStr(item.Content, 200))
			hasAssistant = true
		}
	}
	assert.True(t, hasAssistant, "Planner should have an assistant response")

	// 5. Verify parent's turn_status shows the planner child
	statusResp, err := c.QueryWorkflow(ctx, workflowID, "", workflow.QueryGetTurnStatus)
	require.NoError(t, err)
	var status workflow.TurnStatus
	require.NoError(t, statusResp.Get(&status))
	assert.NotEmpty(t, status.ChildAgents, "Parent should report child agents")
	if len(status.ChildAgents) > 0 {
		found := false
		for _, child := range status.ChildAgents {
			if child.Role == workflow.AgentRolePlanner {
				found = true
				t.Logf("Parent sees planner child: agent_id=%s, status=%s", child.AgentID, child.Status)
			}
		}
		assert.True(t, found, "Parent should have a planner child")
	}

	// 6. Shutdown planner child directly
	childShutdownHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   childWfID,
		UpdateName:   workflow.UpdateShutdown,
		Args:         []interface{}{workflow.ShutdownRequest{}},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	require.NoError(t, err, "Failed to shutdown planner child")
	var childShutdownResp workflow.ShutdownResponse
	require.NoError(t, childShutdownHandle.Get(ctx, &childShutdownResp))
	assert.True(t, childShutdownResp.Acknowledged)
	t.Log("Planner child shutdown acknowledged")

	// Wait for planner child workflow to complete
	childRun := c.GetWorkflow(ctx, childWfID, "")
	var childResult workflow.WorkflowResult
	require.NoError(t, childRun.Get(ctx, &childResult), "Planner child should complete")
	assert.Equal(t, "shutdown", childResult.EndReason)
	assert.NotEmpty(t, childResult.FinalMessage, "Planner should have a final message")
	t.Logf("Planner final message: %s", truncateStr(childResult.FinalMessage, 200))

	// 7. Shutdown parent
	result := shutdownWorkflow(t, ctx, c, workflowID)
	assert.Equal(t, "shutdown", result.EndReason)
	t.Logf("Plan mode test complete. Parent tokens: %d", result.TotalTokens)
}

// TestAgenticWorkflow_PromptSuggestion tests that after a turn completes,
// the GenerateSuggestions activity runs and produces a suggestion visible
// via the get_turn_status query.
func TestAgenticWorkflow_PromptSuggestion(t *testing.T) {
	c := dialTemporal(t)

	workflowID := "test-suggestion-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage:    "Write a Go function that adds two numbers. Do not use any tools.",
		Config: models.SessionConfiguration{
			Model: models.ModelConfig{
				Model:         CheapModel,
				Temperature:   0,
				MaxTokens:     500,
				ContextWindow: 128000,
			},
			Tools: models.ToolsConfig{
				EnableShell:    false,
				EnableReadFile: false,
			},
			DisableSuggestions: false, // Enable suggestions for this test
		},
	}

	t.Logf("Starting suggestion test workflow: %s", workflowID)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	startWorkflow(t, ctx, c, input)

	// Wait for the turn to complete
	waitForTurnComplete(t, ctx, c, workflowID, 1)

	// Poll for suggestion to appear (it's async, may take ~300-500ms after turn complete)
	var suggestion string
	deadline := time.After(30 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for suggestion == "" {
		select {
		case <-deadline:
			t.Log("Suggestion not available after 30s (this is acceptable — LLM may return NONE)")
			goto done
		case <-ctx.Done():
			t.Fatal("Context cancelled waiting for suggestion")
		case <-ticker.C:
			resp, err := c.QueryWorkflow(ctx, workflowID, "", workflow.QueryGetTurnStatus)
			if err != nil {
				continue
			}
			var status workflow.TurnStatus
			if err := resp.Get(&status); err != nil {
				continue
			}
			if status.Suggestion != "" {
				suggestion = status.Suggestion
				t.Logf("Got suggestion: %q", suggestion)
			}
		}
	}

done:
	// The suggestion may be empty if the LLM returned NONE — that's valid.
	// What we verify is that the workflow didn't fail and the field exists.
	t.Logf("Suggestion result: %q (empty is valid if LLM returned NONE)", suggestion)

	// If we got a suggestion, verify it's reasonable (short, single line)
	if suggestion != "" {
		assert.False(t, strings.Contains(suggestion, "\n"), "Suggestion should be single line")
		assert.LessOrEqual(t, len(suggestion), 100, "Suggestion should be concise")
	}

	// Shutdown and verify workflow completed normally
	result := shutdownWorkflow(t, ctx, c, workflowID)
	assert.Equal(t, "shutdown", result.EndReason)
	assert.Greater(t, result.TotalTokens, 0)

	t.Logf("Suggestion test - Total tokens: %d", result.TotalTokens)
}

// TestAgenticWorkflow_SuggestionDisabledE2E tests that with DisableSuggestions=true,
// no suggestion appears after turn completion.
func TestAgenticWorkflow_SuggestionDisabledE2E(t *testing.T) {
	c := dialTemporal(t)

	workflowID := "test-no-suggestion-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage:    "Say hello in exactly 3 words. Do not use any tools.",
		Config: testSessionConfig(100, models.ToolsConfig{
			EnableShell:    false,
			EnableReadFile: false,
		}),
		// testSessionConfig already sets DisableSuggestions: true
	}

	t.Logf("Starting no-suggestion test workflow: %s", workflowID)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	startWorkflow(t, ctx, c, input)
	waitForTurnComplete(t, ctx, c, workflowID, 1)

	// Wait a moment for any async suggestion to arrive (it shouldn't)
	time.Sleep(2 * time.Second)

	// Query turn status — suggestion should be empty
	resp, err := c.QueryWorkflow(ctx, workflowID, "", workflow.QueryGetTurnStatus)
	require.NoError(t, err)
	var status workflow.TurnStatus
	require.NoError(t, resp.Get(&status))

	assert.Equal(t, "", status.Suggestion, "Suggestion should be empty when disabled")

	result := shutdownWorkflow(t, ctx, c, workflowID)
	assert.Equal(t, "shutdown", result.EndReason)

	t.Logf("No-suggestion test - Total tokens: %d", result.TotalTokens)
}

// TestFetchAvailableModels_E2E verifies that FetchAvailableModels returns real
// models from the provider APIs. It checks:
// - At least one model is returned from each configured provider
// - Results are sorted (anthropic before openai)
// - Anthropic models have DisplayName set
// - OpenAI models are filtered to chat-capable models only
func TestFetchAvailableModels_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	hasOpenAI := os.Getenv("OPENAI_API_KEY") != ""
	hasAnthropic := os.Getenv("ANTHROPIC_API_KEY") != ""
	if !hasOpenAI && !hasAnthropic {
		t.Skip("No LLM provider key set, skipping")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	models, err := llm.FetchAvailableModels(ctx)
	require.NoError(t, err)
	require.NotNil(t, models, "Should return models when at least one API key is set")
	require.NotEmpty(t, models, "Should return at least one model")

	t.Logf("Fetched %d models total", len(models))

	// Collect provider stats
	providerCounts := map[string]int{}
	for _, m := range models {
		providerCounts[m.Provider]++
		t.Logf("  %s: %s (display: %q)", m.Provider, m.ID, m.DisplayName)
	}

	if hasOpenAI {
		assert.Greater(t, providerCounts["openai"], 0, "Should have OpenAI models when OPENAI_API_KEY is set")
		// The filter should produce a concise list (no date snapshots, no specialized variants)
		assert.Less(t, providerCounts["openai"], 40, "Filter should keep list concise (<40 OpenAI models)")
		for _, m := range models {
			if m.Provider == "openai" {
				// Capability exclusions
				assert.NotContains(t, m.ID, "embedding", "Should not include embedding models")
				assert.NotContains(t, m.ID, "-tts", "Should not include TTS models")
				assert.NotContains(t, m.ID, "-realtime", "Should not include realtime models")
				assert.NotContains(t, m.ID, "-transcribe", "Should not include transcription models")
				assert.NotContains(t, m.ID, "-instruct", "Should not include instruct models")
				assert.False(t, strings.HasPrefix(m.ID, "ft:"), "Should not include fine-tuned models")
				assert.False(t, strings.HasPrefix(m.ID, "gpt-image"), "Should not include gpt-image models")
				// Noise exclusions
				assert.NotContains(t, m.ID, "-preview", "Should not include preview models")
				assert.NotContains(t, m.ID, "-search", "Should not include search models")
				assert.NotContains(t, m.ID, "-deep-research", "Should not include deep-research models")
				assert.NotContains(t, m.ID, "-chat-latest", "Should not include chat-latest aliases")
			}
		}
	}

	if hasAnthropic {
		assert.Greater(t, providerCounts["anthropic"], 0, "Should have Anthropic models when ANTHROPIC_API_KEY is set")
		// Verify Anthropic models have display names
		for _, m := range models {
			if m.Provider == "anthropic" {
				assert.NotEmpty(t, m.DisplayName, "Anthropic models should have DisplayName: %s", m.ID)
			}
		}
	}

	// Verify sort order: anthropic before openai
	if hasOpenAI && hasAnthropic {
		seenOpenAI := false
		for _, m := range models {
			if m.Provider == "openai" {
				seenOpenAI = true
			}
			if m.Provider == "anthropic" && seenOpenAI {
				t.Error("Anthropic model found after OpenAI model — sort order is wrong")
				break
			}
		}
	}
}

// truncateStr truncates a string to n characters with "..." appended.
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
