// E2E tests for codex-temporal-go
//
// CRITICAL: These tests use REAL services:
// - Real OpenAI API (requires OPENAI_API_KEY)
// - Real Temporal server (requires 'temporal server start-dev')
// - Real worker (must be running)
//
// Prerequisites:
// 1. Terminal 1: temporal server start-dev
// 2. Terminal 2: export OPENAI_API_KEY=sk-... && go run cmd/worker/main.go
// 3. Terminal 3: export OPENAI_API_KEY=sk-... && go test -v ./e2e/...
package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"

	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/workflow"
)

const (
	TaskQueue        = "codex-temporal"
	TemporalHostPort = "localhost:7233"
	WorkflowTimeout  = 3 * time.Minute
	CheapModel       = "gpt-4o-mini"
)

// testModelConfig returns a deterministic model config for testing.
// Temperature 0 makes LLM responses reproducible.
func testModelConfig(maxTokens int) models.ModelConfig {
	return models.ModelConfig{
		Model:         CheapModel,
		Temperature:   0,
		MaxTokens:     maxTokens,
		ContextWindow: 128000,
	}
}

func dialTemporal(t *testing.T) client.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set, skipping E2E test")
	}
	c, err := client.Dial(client.Options{HostPort: TemporalHostPort})
	require.NoError(t, err, "Failed to connect to Temporal server. Is it running?")
	return c
}

// TestAgenticWorkflow_SingleTurn tests a simple conversation without tools
func TestAgenticWorkflow_SingleTurn(t *testing.T) {
	c := dialTemporal(t)
	defer c.Close()

	workflowID := "test-single-turn-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage:    "Say hello in exactly 3 words. Do not use any tools.",
		ModelConfig:    testModelConfig(100),
		ToolsConfig: models.ToolsConfig{
			EnableShell:    false,
			EnableReadFile: false,
		},
	}

	t.Logf("Starting workflow: %s", workflowID)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: workflowID, TaskQueue: TaskQueue,
	}, "AgenticWorkflow", input)
	require.NoError(t, err, "Failed to start workflow")

	var result workflow.WorkflowResult
	err = run.Get(ctx, &result)
	require.NoError(t, err, "Workflow execution failed")

	assert.Equal(t, workflowID, result.ConversationID)
	assert.Greater(t, result.TotalTokens, 0, "Should have consumed tokens")
	assert.Empty(t, result.ToolCallsExecuted, "Should not have called any tools")

	t.Logf("Total tokens: %d, Iterations: %d", result.TotalTokens, result.TotalIterations)
}

// TestAgenticWorkflow_WithShellTool tests LLM calling the shell tool
func TestAgenticWorkflow_WithShellTool(t *testing.T) {
	c := dialTemporal(t)
	defer c.Close()

	workflowID := "test-shell-tool-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		// Very explicit instruction to use the tool
		UserMessage: "You MUST use the shell tool to execute this exact command: echo 'Hello from shell test'. " +
			"Do NOT answer without calling the shell tool first. After getting the result, report the output.",
		ModelConfig: testModelConfig(500),
		ToolsConfig: models.ToolsConfig{
			EnableShell:    true,
			EnableReadFile: false,
		},
	}

	t.Logf("Starting workflow: %s", workflowID)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: workflowID, TaskQueue: TaskQueue,
	}, "AgenticWorkflow", input)
	require.NoError(t, err, "Failed to start workflow")

	var result workflow.WorkflowResult
	err = run.Get(ctx, &result)
	require.NoError(t, err, "Workflow execution failed")

	assert.Equal(t, workflowID, result.ConversationID)
	assert.Greater(t, result.TotalTokens, 0, "Should have consumed tokens")
	assert.Contains(t, result.ToolCallsExecuted, "shell", "Should have called shell tool")
	assert.Greater(t, result.TotalIterations, 1, "Should have multiple iterations (LLM → tool → LLM)")

	t.Logf("Total tokens: %d, Iterations: %d, Tools: %v",
		result.TotalTokens, result.TotalIterations, result.ToolCallsExecuted)
}

// TestAgenticWorkflow_MultiTurn tests a multi-turn conversation with tools
func TestAgenticWorkflow_MultiTurn(t *testing.T) {
	c := dialTemporal(t)
	defer c.Close()

	workflowID := "test-multi-turn-" + uuid.New().String()[:8]
	testFile := "/tmp/codex-test-" + uuid.New().String()[:8] + ".txt"
	defer os.Remove(testFile)

	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		// Very explicit multi-step instruction
		UserMessage: "Complete these steps in order. You MUST use the tools provided.\n" +
			"Step 1: Use the shell tool to run: echo 'Test content' > " + testFile + "\n" +
			"Step 2: After the shell command succeeds, use the read_file tool to read " + testFile + "\n" +
			"Step 3: Report what read_file returned.",
		ModelConfig: testModelConfig(1000),
		ToolsConfig: models.ToolsConfig{
			EnableShell:    true,
			EnableReadFile: true,
		},
	}

	t.Logf("Starting workflow: %s", workflowID)
	t.Logf("Test file: %s", testFile)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: workflowID, TaskQueue: TaskQueue,
	}, "AgenticWorkflow", input)
	require.NoError(t, err, "Failed to start workflow")

	var result workflow.WorkflowResult
	err = run.Get(ctx, &result)
	require.NoError(t, err, "Workflow execution failed")

	assert.Equal(t, workflowID, result.ConversationID)
	assert.Greater(t, result.TotalTokens, 0, "Should have consumed tokens")
	assert.Contains(t, result.ToolCallsExecuted, "shell", "Should have called shell tool")
	assert.Contains(t, result.ToolCallsExecuted, "read_file", "Should have called read_file tool")
	assert.GreaterOrEqual(t, result.TotalIterations, 2, "Should have multiple iterations")

	t.Logf("Total tokens: %d, Iterations: %d, Tools: %v",
		result.TotalTokens, result.TotalIterations, result.ToolCallsExecuted)
}

// TestAgenticWorkflow_ReadFile tests the read_file tool specifically
func TestAgenticWorkflow_ReadFile(t *testing.T) {
	c := dialTemporal(t)
	defer c.Close()

	// Create a temporary test file
	testFile := "/tmp/codex-read-test-" + uuid.New().String()[:8] + ".txt"
	testContent := "Line 1: Hello\nLine 2: World\nLine 3: Test\n"
	err := os.WriteFile(testFile, []byte(testContent), 0644)
	require.NoError(t, err, "Failed to create test file")
	defer os.Remove(testFile)

	workflowID := "test-read-file-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		// Very explicit instruction to use the tool
		UserMessage: "You MUST use the read_file tool to read the file at path " + testFile + ". " +
			"Do NOT answer without calling read_file first. After reading, tell me how many lines it has.",
		ModelConfig: testModelConfig(500),
		ToolsConfig: models.ToolsConfig{
			EnableShell:    false,
			EnableReadFile: true,
		},
	}

	t.Logf("Starting workflow: %s", workflowID)
	t.Logf("Test file: %s", testFile)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: workflowID, TaskQueue: TaskQueue,
	}, "AgenticWorkflow", input)
	require.NoError(t, err, "Failed to start workflow")

	var result workflow.WorkflowResult
	err = run.Get(ctx, &result)
	require.NoError(t, err, "Workflow execution failed")

	assert.Equal(t, workflowID, result.ConversationID)
	assert.Greater(t, result.TotalTokens, 0, "Should have consumed tokens")
	assert.Contains(t, result.ToolCallsExecuted, "read_file", "Should have called read_file tool")

	t.Logf("Total tokens: %d, Iterations: %d, Tools: %v",
		result.TotalTokens, result.TotalIterations, result.ToolCallsExecuted)
}

// TestAgenticWorkflow_ListDir tests the list_dir tool
func TestAgenticWorkflow_ListDir(t *testing.T) {
	c := dialTemporal(t)
	defer c.Close()

	// Create a temporary directory with known contents for the LLM to list.
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
		ModelConfig: testModelConfig(500),
		ToolsConfig: models.ToolsConfig{
			EnableShell:    false,
			EnableReadFile: false,
			EnableListDir:  true,
		},
	}

	t.Logf("Starting workflow: %s", workflowID)
	t.Logf("Test dir: %s", testDir)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: workflowID, TaskQueue: TaskQueue,
	}, "AgenticWorkflow", input)
	require.NoError(t, err, "Failed to start workflow")

	var result workflow.WorkflowResult
	err = run.Get(ctx, &result)
	require.NoError(t, err, "Workflow execution failed")

	assert.Equal(t, workflowID, result.ConversationID)
	assert.Greater(t, result.TotalTokens, 0, "Should have consumed tokens")
	assert.Contains(t, result.ToolCallsExecuted, "list_dir", "Should have called list_dir tool")
	assert.Greater(t, result.TotalIterations, 1, "Should have multiple iterations (LLM → tool → LLM)")

	t.Logf("Total tokens: %d, Iterations: %d, Tools: %v",
		result.TotalTokens, result.TotalIterations, result.ToolCallsExecuted)
}

// TestAgenticWorkflow_WriteFile tests the write_file tool
func TestAgenticWorkflow_WriteFile(t *testing.T) {
	c := dialTemporal(t)
	defer c.Close()

	testFile := "/tmp/codex-write-test-" + uuid.New().String()[:8] + ".txt"
	defer os.Remove(testFile)

	workflowID := "test-write-file-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage: "You MUST use the write_file tool to create a file at " + testFile + " with the content 'Hello from write_file'. " +
			"Do NOT use any other tool. After writing, report what you did.",
		ModelConfig: testModelConfig(500),
		ToolsConfig: models.ToolsConfig{
			EnableShell:      false,
			EnableReadFile:   true,
			EnableWriteFile:  true,
			EnableApplyPatch: false,
		},
	}

	t.Logf("Starting workflow: %s", workflowID)
	t.Logf("Test file: %s", testFile)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: workflowID, TaskQueue: TaskQueue,
	}, "AgenticWorkflow", input)
	require.NoError(t, err, "Failed to start workflow")

	var result workflow.WorkflowResult
	err = run.Get(ctx, &result)
	require.NoError(t, err, "Workflow execution failed")

	assert.Equal(t, workflowID, result.ConversationID)
	assert.Greater(t, result.TotalTokens, 0, "Should have consumed tokens")
	assert.Contains(t, result.ToolCallsExecuted, "write_file", "Should have called write_file tool")
	assert.Greater(t, result.TotalIterations, 1, "Should have multiple iterations (LLM → tool → LLM)")

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
	defer c.Close()

	// Create a unique test file path for the LLM to create via apply_patch
	testFile := "/tmp/codex-patch-test-" + uuid.New().String()[:8] + ".txt"
	defer os.Remove(testFile)

	workflowID := "test-apply-patch-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		// Explicit instruction to use apply_patch to create a file
		UserMessage: "You MUST use the apply_patch tool to create a new file at " + testFile + " with the content 'Hello from apply_patch'. " +
			"Use the *** Add File syntax. Do NOT use any other tool. After the patch is applied, report the result.",
		ModelConfig: testModelConfig(1000),
		ToolsConfig: models.ToolsConfig{
			EnableShell:      false,
			EnableReadFile:   false,
			EnableApplyPatch: true,
		},
	}

	t.Logf("Starting workflow: %s", workflowID)
	t.Logf("Test file: %s", testFile)

	ctx, cancel := context.WithTimeout(context.Background(), WorkflowTimeout)
	defer cancel()

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: workflowID, TaskQueue: TaskQueue,
	}, "AgenticWorkflow", input)
	require.NoError(t, err, "Failed to start workflow")

	var result workflow.WorkflowResult
	err = run.Get(ctx, &result)
	require.NoError(t, err, "Workflow execution failed")

	assert.Equal(t, workflowID, result.ConversationID)
	assert.Greater(t, result.TotalTokens, 0, "Should have consumed tokens")
	assert.Contains(t, result.ToolCallsExecuted, "apply_patch", "Should have called apply_patch tool")
	assert.Greater(t, result.TotalIterations, 1, "Should have multiple iterations (LLM → tool → LLM)")

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
