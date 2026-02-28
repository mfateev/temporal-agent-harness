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

	// Create LoopControl and register handlers early, before init activities.
	// Handlers capture state/ctrl by pointer and read current values at call
	// time, so they work correctly even while init is still running. This
	// prevents races where a query or Update arrives during a slow init
	// activity (e.g. LoadSkills retry) and finds no handlers registered.
	ctrl := &LoopControl{}
	state.registerHandlers(ctx, ctrl)

	if input.ResolvedProfile != nil {
		// Pre-resolved by SessionWorkflow — skip init.
		state.ResolvedProfile = *input.ResolvedProfile
		state.ToolSpecs = buildToolSpecs(input.Config.Tools, state.ResolvedProfile)
		if len(input.McpToolSpecs) > 0 {
			state.ToolSpecs = append(state.ToolSpecs, input.McpToolSpecs...)
		}
		state.McpToolLookup = input.McpToolLookup
		state.LoadedSkills = input.LoadedSkills
		state.ExecPolicyRules = input.Config.ExecPolicyRules
	} else {
		// Direct invocation (E2E tests, standalone, subagent) — do full init.
		state.resolveProfile()
		state.ToolSpecs = buildToolSpecs(input.Config.Tools, state.ResolvedProfile)

		if err := state.initMcpServers(ctx); err != nil {
			return WorkflowResult{}, err
		}

		if state.Config.BaseInstructions == "" {
			state.resolveInstructions(ctx)
		}

		state.ExecPolicyRules = input.Config.ExecPolicyRules
		if state.ExecPolicyRules == "" {
			state.loadExecPolicy(ctx)
		}

		if state.Config.MemoryEnabled && input.Depth == 0 {
			state.loadMemorySummary(ctx)
		}

		if input.Depth == 0 {
			state.loadSkills(ctx)
		}
	}

	// Warn if using deprecated on-failure mode (Codex PR #11631)
	if state.Config.Permissions.ApprovalMode == models.ApprovalOnFailure {
		workflow.GetLogger(ctx).Warn("`on-failure` approval policy is deprecated and will be removed in a future release. Use `unless-trusted` for interactive approvals or `never` for non-interactive runs.")
	}

	// Generate initial turn ID
	turnID := state.nextTurnID()

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

	// Mark first turn as pending and run multi-turn loop.
	ctrl.SetPendingUserInput(turnID)
	return state.runMultiTurnLoop(ctx, ctrl)
}

// AgenticWorkflowContinued handles ContinueAsNew.
func AgenticWorkflowContinued(ctx workflow.Context, state SessionState) (WorkflowResult, error) {
	// Restore History interface from serialized HistoryItems
	state.initHistory()

	// Construct a fresh LoopControl — coordination state is not serialized.
	ctrl := &LoopControl{}

	// Re-register handlers after ContinueAsNew
	state.registerHandlers(ctx, ctrl)
	return state.runMultiTurnLoop(ctx, ctrl)
}

// runMultiTurnLoop is the outer loop that waits for user input between turns.
func (s *SessionState) runMultiTurnLoop(ctx workflow.Context, ctrl *LoopControl) (WorkflowResult, error) {
	logger := workflow.GetLogger(ctx)

	for {
		// Wait for pending user input (first turn has it set already via SetPendingUserInput)
		if !ctrl.HasPendingWork() {
			ctrl.SetPhase(PhaseWaitingForInput)
			ctrl.ClearToolsInFlight()
			logger.Info("Waiting for user input or shutdown")
			timedOut, err := ctrl.WaitForInput(ctx)
			if err != nil {
				return WorkflowResult{}, fmt.Errorf("await failed: %w", err)
			}
			if timedOut {
				if s.AgentCtl != nil && s.AgentCtl.HasActiveChildren() {
					logger.Info("Idle timeout reached but active children exist, deferring CAN")
				} else {
					logger.Info("Idle timeout reached, triggering ContinueAsNew")
					// Extract memory before ContinueAsNew (root workflows only)
					if s.Config.MemoryEnabled && s.AgentCtl != nil && s.AgentCtl.ParentDepth == 0 && s.MemoryExtractedAt == 0 {
						s.extractMemoryOnShutdown(ctx)
					}
					return s.continueAsNew(ctx, ctrl)
				}
			}
		}

		// Handle manual compaction request (before shutdown/input checks)
		if ctrl.IsCompactRequested() {
			ctrl.ClearCompactRequested()
			logger.Info("Manual compaction requested via /compact")
			if err := s.performCompaction(ctx, ctrl); err != nil {
				logger.Warn("Manual compaction failed", "error", err)
			}
			continue
		}

		// Check for shutdown
		if ctrl.IsShutdown() {
			logger.Info("Shutdown requested, completing workflow")

			// Extract memory before shutdown (root workflows only)
			if s.Config.MemoryEnabled && s.AgentCtl != nil && s.AgentCtl.ParentDepth == 0 {
				s.extractMemoryOnShutdown(ctx)
			}

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
		ctrl.StartTurn()
		s.IterationCount = 0

		// Run the agentic turn
		done, err := s.runAgenticTurn(ctx, ctrl)
		if err != nil {
			return WorkflowResult{}, err
		}

		if done {
			// ContinueAsNew was triggered
			return s.continueAsNew(ctx, ctrl)
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
				return s.continueAsNew(ctx, ctrl)
			}
		}

		// Turn complete — add TurnComplete marker (unless interrupted, which already added it)
		if !ctrl.IsInterrupted() {
			_ = s.History.AddItem(models.ConversationItem{
				Type:   models.ItemTypeTurnComplete,
				TurnID: ctrl.CurrentTurnID(),
			})
			ctrl.NotifyItemAdded()
		}

		// Workflows without request_user_input auto-complete after a turn.
		// This is the one-shot pattern: the caller sends a task, the workflow
		// does it and returns. Roles that have request_user_input enabled
		// stay alive for more input instead.
		if !s.Config.Tools.HasTool("request_user_input") {
			logger.Info("Auto-completing workflow (request_user_input disabled)")
			// Extract memory before auto-complete (root workflows only)
			if s.Config.MemoryEnabled && s.AgentCtl != nil && s.AgentCtl.ParentDepth == 0 {
				s.extractMemoryOnShutdown(ctx)
			}
			items, _ := s.History.GetRawItems()
			return WorkflowResult{
				ConversationID:    s.ConversationID,
				TotalIterations:   s.IterationCount,
				TotalTokens:       s.TotalTokens,
				TotalCachedTokens: s.TotalCachedTokens,
				ToolCallsExecuted: s.ToolCallsExecuted,
				EndReason:         "completed",
				FinalMessage:      extractFinalMessage(items),
			}, nil
		}

		ctrl.SetPhase(PhaseWaitingForInput)
		ctrl.ClearToolsInFlight()

		// Generate prompt suggestion asynchronously (best-effort).
		// The CLI has already detected TurnComplete via polling and can show
		// the input prompt immediately; the suggestion arrives ~300-500ms later.
		if !ctrl.IsInterrupted() && !s.Config.DisableSuggestions {
			s.generateSuggestion(ctx, ctrl)
		}

		logger.Info("Turn complete, waiting for next input", "turn_id", ctrl.CurrentTurnID())
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
// Accepts ctrl so it can set draining to wake any blocked get_state_update handlers.
func (s *SessionState) continueAsNew(ctx workflow.Context, ctrl *LoopControl) (WorkflowResult, error) {
	// Mark as draining so blocked get_state_update handlers wake up and return.
	ctrl.SetDraining()

	// Wait for all update handlers to finish before ContinueAsNew
	_ = workflow.Await(ctx, func() bool {
		return workflow.AllHandlersFinished(ctx)
	})

	s.syncHistoryItems()
	return WorkflowResult{}, workflow.NewContinueAsNewError(ctx, "AgenticWorkflowContinued", *s)
}
