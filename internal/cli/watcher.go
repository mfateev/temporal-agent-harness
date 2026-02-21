package cli

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/client"

	"github.com/mfateev/temporal-agent-harness/internal/models"
	"github.com/mfateev/temporal-agent-harness/internal/workflow"
)

// WatchResult holds the result of a single blocking watch call.
type WatchResult struct {
	Items     []models.ConversationItem
	Status    workflow.TurnStatus
	Compacted bool
	Completed bool
	Err       error
}

// Watcher uses the blocking get_state_update Update instead of polling queries.
// Each call to Watch blocks until the workflow has new state to report.
type Watcher struct {
	client     client.Client
	workflowID string
}

// NewWatcher creates a Watcher for the given workflow.
func NewWatcher(c client.Client, workflowID string) *Watcher {
	return &Watcher{
		client:     c,
		workflowID: workflowID,
	}
}

// Watch performs a single blocking call to the get_state_update Update.
// It blocks server-side until the workflow has new items or a phase change.
func (w *Watcher) Watch(ctx context.Context, sinceSeq int, sincePhase workflow.TurnPhase) WatchResult {
	updateHandle, err := w.client.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   w.workflowID,
		UpdateName:   workflow.UpdateGetStateUpdate,
		Args:         []interface{}{workflow.StateUpdateRequest{SinceSeq: sinceSeq, SincePhase: sincePhase}},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	if err != nil {
		return WatchResult{Err: fmt.Errorf("get_state_update call failed: %w", err)}
	}

	var resp workflow.StateUpdateResponse
	if err := updateHandle.Get(ctx, &resp); err != nil {
		return WatchResult{Err: fmt.Errorf("get_state_update get failed: %w", err)}
	}

	return WatchResult{
		Items:     resp.Items,
		Status:    resp.Status,
		Compacted: resp.Compacted,
		Completed: resp.Completed,
	}
}

// RunWatching runs a blocking watch loop, sending results to the channel.
// Tracks sinceSeq/sincePhase across iterations. Stops when context is cancelled.
func (w *Watcher) RunWatching(ctx context.Context, ch chan<- WatchResult, initialSeq int, initialPhase workflow.TurnPhase) {
	sinceSeq := initialSeq
	sincePhase := initialPhase

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		result := w.Watch(ctx, sinceSeq, sincePhase)

		// Update cursor for next iteration
		if result.Err == nil {
			if result.Compacted {
				// After compaction, reset seq to the latest item
				if len(result.Items) > 0 {
					sinceSeq = result.Items[len(result.Items)-1].Seq
				} else {
					sinceSeq = -1
				}
			} else if len(result.Items) > 0 {
				sinceSeq = result.Items[len(result.Items)-1].Seq
			}
			sincePhase = result.Status.Phase
		}

		select {
		case ch <- result:
		case <-ctx.Done():
			return
		}

		// If completed, stop watching
		if result.Completed {
			return
		}
	}
}
