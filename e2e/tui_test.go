package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
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
	binary := getTcxBinary()
	if binary == "" {
		t.Skip("tcx binary build failed; skipping TUI tests")
	}

	rootOut, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	require.NoError(t, err, "Failed to find repo root")
	root := strings.TrimSpace(string(rootOut))
	tuiDir := filepath.Join(root, "tui-tests")

	if _, err := os.Stat(tuiDir); os.IsNotExist(err) {
		t.Skipf("tui-tests/ directory not found at %s", tuiDir)
	}

	nodeModules := filepath.Join(tuiDir, "node_modules")
	if _, err := os.Stat(nodeModules); os.IsNotExist(err) {
		t.Log("Installing npm dependencies in tui-tests/...")
		npm := exec.Command("npm", "install")
		npm.Dir = tuiDir
		npm.Stdout = os.Stderr
		npm.Stderr = os.Stderr
		require.NoError(t, npm.Run(), "npm install failed")
	}

	seedID := createSeedSession(t)

	// Use a predictable harness workflow ID so the health monitor can watch it
	// without listing workflows.
	harnessID := "tui-harness-" + uuid.New().String()[:8]

	// Pre-create the HarnessWorkflow so the first tui-test doesn't race
	// against workflow handler registration. Without this, the first 1-2 tcx
	// processes may time out because start_session arrives before the workflow
	// has registered its Update handlers.
	preCtx, preCancel := context.WithTimeout(context.Background(), 30*time.Second)
	_, err = temporalClient.ExecuteWorkflow(preCtx, client.StartWorkflowOptions{
		ID:        harnessID,
		TaskQueue: TaskQueue,
	}, "HarnessWorkflow", workflow.HarnessWorkflowInput{HarnessID: harnessID})
	preCancel()
	if err != nil {
		t.Logf("Pre-creating harness workflow: %v (may be fine if it already exists)", err)
	}
	time.Sleep(1 * time.Second) // let the workflow task run and register handlers

	tuiCtx, tuiCancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer tuiCancel()

	// Monitor the harness workflow and its child sessions for failures.
	// Cancels tuiCtx on first detected failure so tui-test is killed immediately.
	go monitorHarnessHealth(tuiCtx, tuiCancel, t, harnessID)

	cmd := exec.CommandContext(tuiCtx, "npx", "@microsoft/tui-test")
	cmd.Dir = tuiDir
	cmd.Env = append(os.Environ(),
		"TCX_BINARY="+binary,
		"TEMPORAL_HOST="+TestHostPort,
		"TCX_CONNECTION_TIMEOUT=10s",
		"TCX_HARNESS_ID="+harnessID,
	)
	if seedID != "" {
		cmd.Env = append(cmd.Env, "RESUME_SESSION_ID="+seedID)
		t.Logf("Seed session for resume test: %s", seedID)
	}
	// Capture stdout for latency tracking while still printing to os.Stdout.
	var tuiOutput bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &tuiOutput)
	cmd.Stderr = os.Stderr

	t.Log("Running: npx @microsoft/tui-test")
	latencyTracker.Track(t)
	runErr := cmd.Run()

	// Record TUI sub-test latencies from tui-test output (✔/✗ lines with durations).
	latencyTracker.AddTUIResults(tuiOutput.String())

	if runErr != nil {
		if tuiCtx.Err() != nil {
			t.Fatalf("TUI tests killed (workflow failure or 4m deadline): %v", runErr)
		}
		t.Fatalf("TUI tests failed: %v", runErr)
	}
}

// monitorHarnessHealth watches the harness workflow and its children for
// failures. Uses two strategies:
//  1. DescribeWorkflowExecution every 2s — checks pending activity failures and
//     child workflow statuses.
//  2. GetWorkflowHistory with long poll — catches WorkflowTaskFailed events
//     (panics, non-determinism) that Describe doesn't surface.
//
// On first detected failure, cancels the context to kill tui-test immediately.
func monitorHarnessHealth(ctx context.Context, cancel context.CancelFunc, t *testing.T, harnessID string) {
	t.Helper()

	// Give tui-test a few seconds to start tcx and create the harness workflow.
	select {
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
		return
	}

	// Start history watcher in a separate goroutine — it long-polls for events
	// on the harness workflow.
	go watchHistoryForFailures(ctx, cancel, t, harnessID)

	// Periodically describe the harness and its child workflows.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if reason := describeForFailures(ctx, t, harnessID); reason != "" {
			t.Logf("Health monitor (describe): %s", reason)
			cancel()
			return
		}
	}
}

// watchHistoryForFailures long-polls the harness workflow history for failure
// events (WorkflowTaskFailed, WorkflowExecutionFailed, etc.).
// Retries "workflow not found" during startup — the harness workflow may not
// exist yet when the monitor starts.
func watchHistoryForFailures(ctx context.Context, cancel context.CancelFunc, t *testing.T, workflowID string) {
	t.Helper()

	// Retry loop: the harness workflow may not exist yet.
	for {
		if ctx.Err() != nil {
			return
		}

		iter := temporalClient.GetWorkflowHistory(ctx, workflowID, "",
			true, // long poll
			enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)

		iterFailed := false
		for iter.HasNext() {
			event, err := iter.Next()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				// "not found" means workflow doesn't exist yet — retry after delay.
				if strings.Contains(err.Error(), "not found") {
					iterFailed = true
					break
				}
				// Other error (server down) — fatal.
				t.Logf("Health monitor (history): error: %v", err)
				cancel()
				return
			}

			if reason := checkEventForFailure(event); reason != "" {
				t.Logf("Health monitor (history): %s", reason)
				cancel()
				return
			}
		}

		if !iterFailed {
			return // iterator exhausted normally (workflow completed)
		}

		// Wait before retrying.
		select {
		case <-time.After(3 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

// checkEventForFailure returns a non-empty reason if the event indicates a
// workflow or activity failure.
func checkEventForFailure(event *historypb.HistoryEvent) string {
	switch event.EventType {
	case enumspb.EVENT_TYPE_WORKFLOW_TASK_FAILED:
		attrs := event.GetWorkflowTaskFailedEventAttributes()
		if attrs != nil && attrs.Failure != nil {
			return fmt.Sprintf("WorkflowTaskFailed: %s", attrs.Failure.Message)
		}
		return "WorkflowTaskFailed (no details)"

	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED:
		attrs := event.GetWorkflowExecutionFailedEventAttributes()
		if attrs != nil && attrs.Failure != nil {
			return fmt.Sprintf("WorkflowExecutionFailed: %s", attrs.Failure.Message)
		}
		return "WorkflowExecutionFailed (no details)"

	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT:
		return "WorkflowExecutionTimedOut"

	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED:
		return "WorkflowExecutionTerminated"
	}

	return ""
}

// describeForFailures checks the harness workflow and its children for
// pending activity failures via DescribeWorkflowExecution.
func describeForFailures(ctx context.Context, t *testing.T, harnessID string) string {
	t.Helper()

	descCtx, descCancel := context.WithTimeout(ctx, 5*time.Second)
	defer descCancel()

	desc, err := temporalClient.DescribeWorkflowExecution(descCtx, harnessID, "")
	if err != nil {
		// Workflow may not exist yet — not a failure.
		return ""
	}

	// Check harness workflow status.
	if info := desc.WorkflowExecutionInfo; info != nil {
		if info.Status == enumspb.WORKFLOW_EXECUTION_STATUS_FAILED ||
			info.Status == enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED ||
			info.Status == enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT {
			return fmt.Sprintf("harness workflow %s: status %s", harnessID, info.Status)
		}
	}

	// Check harness pending activities.
	if reason := checkPendingActivities(desc.PendingActivities, harnessID); reason != "" {
		return reason
	}

	// Check each child workflow (sessions started by HarnessWorkflow).
	for _, child := range desc.PendingChildren {
		childID := child.WorkflowId

		childDescCtx, childDescCancel := context.WithTimeout(ctx, 5*time.Second)
		childDesc, err := temporalClient.DescribeWorkflowExecution(childDescCtx, childID, "")
		childDescCancel()
		if err != nil {
			continue
		}

		if info := childDesc.WorkflowExecutionInfo; info != nil {
			if info.Status == enumspb.WORKFLOW_EXECUTION_STATUS_FAILED {
				return fmt.Sprintf("child workflow %s: status FAILED", childID)
			}
		}

		if reason := checkPendingActivities(childDesc.PendingActivities, childID); reason != "" {
			return reason
		}
	}

	return ""
}

// checkPendingActivities returns a reason string if any activity has a
// non-retryable failure (attempt > 1 with a LastFailure).
func checkPendingActivities(activities []*workflowpb.PendingActivityInfo, wfID string) string {
	for _, pa := range activities {
		if pa.LastFailure != nil && pa.Attempt > 1 {
			actType := ""
			if pa.ActivityType != nil {
				actType = pa.ActivityType.Name
			}
			return fmt.Sprintf("workflow %s activity %s failing (attempt %d): %s",
				wfID, actType, pa.Attempt, pa.LastFailure.Message)
		}
	}
	return ""
}

// createSeedSession starts a workflow with a known prompt, waits for the LLM
// to respond, then shuts the workflow down. Returns the workflow ID so the
// session-resume TUI test can reconnect to it.
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
		Config: testSessionConfig(100, models.ToolsConfig{}),
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

	waitForTurnComplete(t, ctx, temporalClient, workflowID, 1)
	shutdownWorkflow(t, ctx, temporalClient, workflowID)

	t.Logf("Seed session created: %s", workflowID)
	return workflowID
}
