package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"

	"github.com/mfateev/temporal-agent-harness/internal/models"
	"github.com/mfateev/temporal-agent-harness/internal/workflow"
)

// TestTUI runs the TypeScript TUI test suite (@microsoft/tui-test) using the
// Temporal server and worker already started by TestMain. This unifies TUI
// tests into the Go E2E suite so the push-gate hook covers both.
func TestTUI(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Skipping TUI tests in short mode")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found in PATH; skipping TUI tests")
	}
	// Lazily build the tcx binary (only TestTUI needs it)
	binary := getTcxBinary()
	if binary == "" {
		t.Skip("tcx binary build failed; skipping TUI tests")
	}

	// Resolve tui-tests/ directory relative to the repo root.
	rootOut, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	require.NoError(t, err, "Failed to find repo root")
	root := strings.TrimSpace(string(rootOut))
	tuiDir := filepath.Join(root, "tui-tests")

	if _, err := os.Stat(tuiDir); os.IsNotExist(err) {
		t.Skipf("tui-tests/ directory not found at %s", tuiDir)
	}

	// Install npm dependencies if needed.
	nodeModules := filepath.Join(tuiDir, "node_modules")
	if _, err := os.Stat(nodeModules); os.IsNotExist(err) {
		t.Log("Installing npm dependencies in tui-tests/...")
		npm := exec.Command("npm", "install")
		npm.Dir = tuiDir
		npm.Stdout = os.Stderr
		npm.Stderr = os.Stderr
		require.NoError(t, npm.Run(), "npm install failed")
	}

	// Create a seed session for the resume test.
	seedID := createSeedSession(t)

	// Run tui-test with environment variables pointing to our infrastructure.
	cmd := exec.Command("npx", "@microsoft/tui-test")
	cmd.Dir = tuiDir
	cmd.Env = append(os.Environ(),
		"TCX_BINARY="+binary,
		"TEMPORAL_HOST="+TestHostPort,
	)
	if seedID != "" {
		cmd.Env = append(cmd.Env, "RESUME_SESSION_ID="+seedID)
		t.Logf("Seed session for resume test: %s", seedID)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	t.Log("Running: npx @microsoft/tui-test")
	if err := cmd.Run(); err != nil {
		t.Fatalf("TUI tests failed: %v", err)
	}
}

// createSeedSession starts a workflow with a known prompt, waits for the LLM
// to respond, then shuts the workflow down. Returns the workflow ID so the
// session-resume TUI test can reconnect to it. Returns "" if the seed session
// cannot be created (logged but not fatal — the resume test will skip).
func createSeedSession(t *testing.T) string {
	t.Helper()

	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Log("Seed session: skipping (OPENAI_API_KEY not set)")
		return ""
	}

	workflowID := "tui-seed-" + uuid.New().String()[:8]
	input := workflow.WorkflowInput{
		ConversationID: workflowID,
		UserMessage:    "Say exactly the word: persimmon",
		Config: testSessionConfig(100, models.ToolsConfig{
			EnableShell:    false,
			EnableReadFile: false,
		}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	_, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: TaskQueue,
	}, "AgenticWorkflow", input)
	if err != nil {
		t.Logf("Seed session: failed to start workflow: %v", err)
		return ""
	}

	// Wait for the LLM to respond.
	waitForTurnComplete(t, ctx, temporalClient, workflowID, 1)

	// Shut the workflow down so it's in a completed state for resume.
	shutdownWorkflow(t, ctx, temporalClient, workflowID)

	t.Logf("Seed session created: %s", workflowID)
	return workflowID
}
