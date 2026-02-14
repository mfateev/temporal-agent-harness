// Package workflow contains Temporal workflow definitions.
//
// Corresponds to: codex-rs/core/src/codex.rs (run_turn, run_sampling_request)
package workflow

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/mfateev/temporal-agent-harness/internal/history"
	"github.com/mfateev/temporal-agent-harness/internal/instructions"
	"github.com/mfateev/temporal-agent-harness/internal/models"
)

// IdleTimeout is how long the workflow waits for user input before triggering ContinueAsNew.
const IdleTimeout = 24 * time.Hour

// maxIterationsBeforeCAN is the total iteration count across all turns in a
// single workflow run before triggering ContinueAsNew to keep history bounded.
const maxIterationsBeforeCAN = 100

// maxRepeatToolCalls is the number of consecutive identical tool call batches
// before the turn is ended early to prevent tight loops.
const maxRepeatToolCalls = 3

// AgenticWorkflow is the main durable agentic loop.
//
// Maps to: codex-rs/core/src/codex.rs run_turn
func AgenticWorkflow(ctx workflow.Context, input WorkflowInput) (WorkflowResult, error) {
	state := SessionState{
		ConversationID: input.ConversationID,
		History:        history.NewInMemoryHistory(),
		Config:         input.Config,
		MaxIterations:  20,
		IterationCount: 0,
		AgentCtl:       NewAgentControl(input.Depth),
	}

	// Build tool specs based on configuration
	state.ToolSpecs = buildToolSpecs(input.Config.Tools)

	// Resolve instructions (load worker-side AGENTS.md, merge all sources)
	state.resolveInstructions(ctx)

	// Load exec policy rules from worker filesystem
	state.loadExecPolicy(ctx)

	// Generate initial turn ID
	turnID := generateTurnID(ctx)
	state.CurrentTurnID = turnID

	// Add initial TurnStarted marker
	if err := state.History.AddItem(models.ConversationItem{
		Type:   models.ItemTypeTurnStarted,
		TurnID: turnID,
	}); err != nil {
		return WorkflowResult{}, fmt.Errorf("failed to add turn started: %w", err)
	}

	// Add environment context as the first user message
	if state.Config.Cwd != "" {
		envCtx := instructions.BuildEnvironmentContext(state.Config.Cwd, "")
		if err := state.History.AddItem(models.ConversationItem{
			Type:    models.ItemTypeUserMessage,
			Content: envCtx,
			TurnID:  turnID,
		}); err != nil {
			return WorkflowResult{}, fmt.Errorf("failed to add environment context: %w", err)
		}
	}

	// Add initial user message to history
	if err := state.History.AddItem(models.ConversationItem{
		Type:    models.ItemTypeUserMessage,
		Content: input.UserMessage,
		TurnID:  turnID,
	}); err != nil {
		return WorkflowResult{}, fmt.Errorf("failed to add user message: %w", err)
	}

	// Mark that we have pending input for the first turn
	state.PendingUserInput = true

	// Register handlers and run multi-turn loop
	state.registerHandlers(ctx)
	return state.runMultiTurnLoop(ctx)
}

// AgenticWorkflowContinued handles ContinueAsNew.
func AgenticWorkflowContinued(ctx workflow.Context, state SessionState) (WorkflowResult, error) {
	// Restore History interface from serialized HistoryItems
	state.initHistory()
	// Re-register handlers after ContinueAsNew
	state.registerHandlers(ctx)
	return state.runMultiTurnLoop(ctx)
}

// runMultiTurnLoop is the outer loop that waits for user input between turns.
func (s *SessionState) runMultiTurnLoop(ctx workflow.Context) (WorkflowResult, error) {
	logger := workflow.GetLogger(ctx)

	for {
		// Wait for pending user input (first turn has it set already)
		if !s.PendingUserInput && !s.ShutdownRequested && !s.CompactRequested {
			s.Phase = PhaseWaitingForInput
			s.ToolsInFlight = nil
			logger.Info("Waiting for user input or shutdown")
			timedOut, err := awaitWithIdleTimeout(ctx, func() bool {
				return s.PendingUserInput || s.ShutdownRequested || s.CompactRequested
			})
			if err != nil {
				return WorkflowResult{}, fmt.Errorf("await failed: %w", err)
			}
			if timedOut {
				if s.AgentCtl != nil && s.AgentCtl.HasActiveChildren() {
					logger.Info("Idle timeout reached but active children exist, deferring CAN")
				} else {
					logger.Info("Idle timeout reached, triggering ContinueAsNew")
					return s.continueAsNew(ctx)
				}
			}
		}

		// Handle manual compaction request (before shutdown/input checks)
		if s.CompactRequested {
			s.CompactRequested = false
			logger.Info("Manual compaction requested via /compact")
			if err := s.performCompaction(ctx); err != nil {
				logger.Warn("Manual compaction failed", "error", err)
			}
			continue
		}

		// Check for shutdown
		if s.ShutdownRequested {
			logger.Info("Shutdown requested, completing workflow")
			items, _ := s.History.GetRawItems()
			return WorkflowResult{
				ConversationID:    s.ConversationID,
				TotalIterations:   s.IterationCount,
				TotalTokens:       s.TotalTokens,
				TotalCachedTokens: s.TotalCachedTokens,
				ToolCallsExecuted: s.ToolCallsExecuted,
				EndReason:         "shutdown",
				FinalMessage:      extractFinalMessage(items),
			}, nil
		}

		// Reset for new turn
		s.PendingUserInput = false
		s.Interrupted = false
		s.IterationCount = 0
		s.Suggestion = ""

		// Run the agentic turn
		done, err := s.runAgenticTurn(ctx)
		if err != nil {
			return WorkflowResult{}, err
		}

		if done {
			// ContinueAsNew was triggered
			return s.continueAsNew(ctx)
		}

		// Accumulate iterations for CAN threshold across turns.
		s.TotalIterationsForCAN += s.IterationCount
		if s.TotalIterationsForCAN >= maxIterationsBeforeCAN {
			// Block ContinueAsNew if there are active child workflows.
			// Re-attaching to child futures after CAN is complex, so we defer.
			if s.AgentCtl != nil && s.AgentCtl.HasActiveChildren() {
				logger.Info("Deferring ContinueAsNew: active child workflows",
					"total", s.TotalIterationsForCAN)
				s.TotalIterationsForCAN = maxIterationsBeforeCAN / 2
			} else {
				logger.Info("Total iterations across turns reached CAN threshold",
					"total", s.TotalIterationsForCAN)
				return s.continueAsNew(ctx)
			}
		}

		// Turn complete — add TurnComplete marker (unless interrupted, which already added it)
		if !s.Interrupted {
			_ = s.History.AddItem(models.ConversationItem{
				Type:   models.ItemTypeTurnComplete,
				TurnID: s.CurrentTurnID,
			})
		}

		s.Phase = PhaseWaitingForInput
		s.ToolsInFlight = nil

		// Generate prompt suggestion asynchronously (best-effort).
		// The CLI has already detected TurnComplete via polling and can show
		// the input prompt immediately; the suggestion arrives ~300-500ms later.
		if !s.Interrupted && !s.Config.DisableSuggestions {
			s.Suggestion = ""
			s.generateSuggestion(ctx)
		}

		logger.Info("Turn complete, waiting for next input", "turn_id", s.CurrentTurnID)
	}
}

// awaitWithIdleTimeout waits for condition or idle timeout.
// Returns (timedOut, error).
func awaitWithIdleTimeout(ctx workflow.Context, condition func() bool) (bool, error) {
	ok, err := workflow.AwaitWithTimeout(ctx, IdleTimeout, condition)
	if err != nil {
		return false, err
	}
	return !ok, nil // ok=false means timed out
}

// continueAsNew prepares state and triggers ContinueAsNew.
func (s *SessionState) continueAsNew(ctx workflow.Context) (WorkflowResult, error) {
	// Wait for all update handlers to finish before ContinueAsNew
	_ = workflow.Await(ctx, func() bool {
		return workflow.AllHandlersFinished(ctx)
	})

	s.syncHistoryItems()
	return WorkflowResult{}, workflow.NewContinueAsNewError(ctx, "AgenticWorkflowContinued", *s)
}
