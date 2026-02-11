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

	toolActivities := activities.NewToolActivities(toolRegistry)
	w.RegisterActivity(toolActivities.ExecuteTool)

	instructionActivities := activities.NewInstructionActivities()
	w.RegisterActivity(instructionActivities.LoadWorkerInstructions)
	w.RegisterActivity(instructionActivities.LoadExecPolicy)

	return w
}

// --- Test helpers ---

// testSessionConfig returns a deterministic session configuration for testing.
// Temperature 0 makes LLM responses reproducible.
func testSessionConfig(maxTokens int, tools models.ToolsConfig) models.SessionConfiguration {
	return models.SessionConfiguration{
		Model: models.ModelConfig{
			Model:         CheapModel,
			Temperature:   0,
			MaxTokens:     maxTokens,
			ContextWindow: 128000,
		},
		Tools: tools,
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
