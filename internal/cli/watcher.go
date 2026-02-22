package cli

import (
	"context"
	"fmt"
	"time"

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
	// rpcTimeout, if > 0, limits how long each Temporal RPC waits.
	// When the server is unreachable, calls fail after this duration
	// instead of retrying gRPC connections forever.
	rpcTimeout time.Duration
}

// NewWatcher creates a Watcher for the given workflow.
func NewWatcher(c client.Client, workflowID string) *Watcher {
	return &Watcher{
		client:     c,
		workflowID: workflowID,
	}
}

// WithRPCTimeout sets a per-call timeout on Temporal RPCs.
func (w *Watcher) WithRPCTimeout(d time.Duration) *Watcher {
	w.rpcTimeout = d
	return w
}

// Watch performs a single blocking call to the get_state_update Update.
// It blocks server-side until the workflow has new items or a phase change.
func (w *Watcher) Watch(ctx context.Context, sinceSeq int, sincePhase workflow.TurnPhase) WatchResult {
	callCtx := ctx
	if w.rpcTimeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, w.rpcTimeout)
		defer cancel()
	}
	updateHandle, err := w.client.UpdateWorkflow(callCtx, client.UpdateWorkflowOptions{
		WorkflowID:   w.workflowID,
		UpdateName:   workflow.UpdateGetStateUpdate,
		Args:         []interface{}{workflow.StateUpdateRequest{SinceSeq: sinceSeq, SincePhase: sincePhase}},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	if err != nil {
		return WatchResult{Err: fmt.Errorf("get_state_update call failed: %w", err)}
	}

	var resp workflow.StateUpdateResponse
	if err := updateHandle.Get(callCtx, &resp); err != nil {
		return WatchResult{Err: fmt.Errorf("get_state_update get failed: %w", err)}
	}

	return WatchResult{
		Items:     resp.Items,
		Status:    resp.Status,
		Compacted: resp.Compacted,
		Completed: resp.Completed,
	}
}

// maxConsecutiveErrors is the number of consecutive RPC failures before
// RunWatching gives up. Prevents infinite retry loops when the server is dead.
const maxConsecutiveErrors = 3

// RunWatching runs a blocking watch loop, sending results to the channel.
// Tracks sinceSeq/sincePhase across iterations. Stops when context is
// cancelled or after maxConsecutiveErrors consecutive failures.
func (w *Watcher) RunWatching(ctx context.Context, ch chan<- WatchResult, initialSeq int, initialPhase workflow.TurnPhase) {
	sinceSeq := initialSeq
	sincePhase := initialPhase
	consecutiveErrors := 0

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		result := w.Watch(ctx, sinceSeq, sincePhase)

		if result.Err != nil {
			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				result.Err = fmt.Errorf("giving up after %d consecutive failures: %w", consecutiveErrors, result.Err)
				select {
				case ch <- result:
				case <-ctx.Done():
				}
				return
			}
			// Brief pause before retry to avoid tight error loops
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return
			}
		} else {
			consecutiveErrors = 0
		}

		// Update cursor for next iteration
		if result.Err == nil {
			if result.Compacted {
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
