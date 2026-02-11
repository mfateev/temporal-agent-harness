// Package workflow contains Temporal workflow definitions.
//
// Corresponds to: codex-rs/core/src/codex.rs (run_turn, run_sampling_request)
package workflow

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/mfateev/codex-temporal-go/internal/activities"
	"github.com/mfateev/codex-temporal-go/internal/execpolicy"
	"github.com/mfateev/codex-temporal-go/internal/history"
	"github.com/mfateev/codex-temporal-go/internal/instructions"
	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/tools"
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

// registerHandlers registers query and update handlers on the workflow.
func (s *SessionState) registerHandlers(ctx workflow.Context) {
	logger := workflow.GetLogger(ctx)

	// Query: get_conversation_items
	// Maps to: Codex ContextManager::raw_items()
	err := workflow.SetQueryHandler(ctx, QueryGetConversationItems, func() ([]models.ConversationItem, error) {
		return s.History.GetRawItems()
	})
	if err != nil {
		logger.Error("Failed to register get_conversation_items query handler", "error", err)
	}

	// Query: get_turn_status
	// Returns current turn phase and stats for CLI polling.
	err = workflow.SetQueryHandler(ctx, QueryGetTurnStatus, func() (TurnStatus, error) {
		turnCount, _ := s.History.GetTurnCount()
		return TurnStatus{
			Phase:                   s.Phase,
			CurrentTurnID:           s.CurrentTurnID,
			ToolsInFlight:           s.ToolsInFlight,
			PendingApprovals:        s.PendingApprovals,
			PendingEscalations:      s.PendingEscalations,
			PendingUserInputRequest: s.PendingUserInputReq,
			IterationCount:          s.IterationCount,
			TotalTokens:             s.TotalTokens,
			TurnCount:               turnCount,
		}, nil
	})
	if err != nil {
		logger.Error("Failed to register get_turn_status query handler", "error", err)
	}

	// Update: user_input
	// Maps to: Codex Op::UserInput / turn/start
	err = workflow.SetUpdateHandlerWithOptions(
		ctx,
		UpdateUserInput,
		func(ctx workflow.Context, input UserInput) (UserInputAccepted, error) {
			turnID := generateTurnID(ctx)

			// Add TurnStarted marker
			if err := s.History.AddItem(models.ConversationItem{
				Type:   models.ItemTypeTurnStarted,
				TurnID: turnID,
			}); err != nil {
				return UserInputAccepted{}, fmt.Errorf("failed to add turn started: %w", err)
			}

			// Add user message
			if err := s.History.AddItem(models.ConversationItem{
				Type:    models.ItemTypeUserMessage,
				Content: input.Content,
				TurnID:  turnID,
			}); err != nil {
				return UserInputAccepted{}, fmt.Errorf("failed to add user message: %w", err)
			}

			s.CurrentTurnID = turnID
			s.PendingUserInput = true

			return UserInputAccepted{TurnID: turnID}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, input UserInput) error {
				if input.Content == "" {
					return fmt.Errorf("content must not be empty")
				}
				if s.ShutdownRequested {
					return fmt.Errorf("session is shutting down")
				}
				return nil
			},
		},
	)
	if err != nil {
		logger.Error("Failed to register user_input update handler", "error", err)
	}

	// Update: interrupt
	// Maps to: Codex Op::Interrupt
	err = workflow.SetUpdateHandlerWithOptions(
		ctx,
		UpdateInterrupt,
		func(ctx workflow.Context, req InterruptRequest) (InterruptResponse, error) {
			s.Interrupted = true

			// Add TurnComplete marker for interrupted turn
			if s.CurrentTurnID != "" {
				_ = s.History.AddItem(models.ConversationItem{
					Type:    models.ItemTypeTurnComplete,
					TurnID:  s.CurrentTurnID,
					Content: "interrupted",
				})
			}

			return InterruptResponse{Acknowledged: true}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, req InterruptRequest) error {
				if s.ShutdownRequested {
					return fmt.Errorf("session is shutting down")
				}
				return nil
			},
		},
	)
	if err != nil {
		logger.Error("Failed to register interrupt update handler", "error", err)
	}

	// Update: shutdown
	// Maps to: Codex Op::Shutdown
	err = workflow.SetUpdateHandlerWithOptions(
		ctx,
		UpdateShutdown,
		func(ctx workflow.Context, req ShutdownRequest) (ShutdownResponse, error) {
			s.ShutdownRequested = true
			s.Interrupted = true // Also interrupt current turn
			return ShutdownResponse{Acknowledged: true}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, req ShutdownRequest) error {
				if s.ShutdownRequested {
					return fmt.Errorf("session is already shutting down")
				}
				return nil
			},
		},
	)
	if err != nil {
		logger.Error("Failed to register shutdown update handler", "error", err)
	}

	// Update: approval_response
	// Maps to: Codex approval flow (user approves/denies tool calls)
	err = workflow.SetUpdateHandlerWithOptions(
		ctx,
		UpdateApprovalResponse,
		func(ctx workflow.Context, resp ApprovalResponse) (ApprovalResponseAck, error) {
			s.ApprovalResponse = &resp
			s.ApprovalReceived = true
			return ApprovalResponseAck{}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, resp ApprovalResponse) error {
				if s.Phase != PhaseApprovalPending {
					return fmt.Errorf("no approval pending")
				}
				return nil
			},
		},
	)
	if err != nil {
		logger.Error("Failed to register approval_response update handler", "error", err)
	}

	// Update: escalation_response
	// Maps to: Codex on-failure escalation flow
	err = workflow.SetUpdateHandlerWithOptions(
		ctx,
		UpdateEscalationResponse,
		func(ctx workflow.Context, resp EscalationResponse) (EscalationResponseAck, error) {
			s.EscalationResponse = &resp
			s.EscalationReceived = true
			return EscalationResponseAck{}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, resp EscalationResponse) error {
				if s.Phase != PhaseEscalationPending {
					return fmt.Errorf("no escalation pending")
				}
				return nil
			},
		},
	)
	if err != nil {
		logger.Error("Failed to register escalation_response update handler", "error", err)
	}

	// Update: user_input_question_response
	// Maps to: Codex request_user_input flow (user answers multi-choice questions)
	err = workflow.SetUpdateHandlerWithOptions(
		ctx,
		UpdateUserInputQuestionResponse,
		func(ctx workflow.Context, resp UserInputQuestionResponse) (UserInputQuestionResponseAck, error) {
			s.UserInputQResponse = &resp
			s.UserInputQReceived = true
			return UserInputQuestionResponseAck{}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, resp UserInputQuestionResponse) error {
				if s.Phase != PhaseUserInputPending {
					return fmt.Errorf("no user input question pending")
				}
				return nil
			},
		},
	)
	if err != nil {
		logger.Error("Failed to register user_input_question_response update handler", "error", err)
	}
}

// generateTurnID generates a unique turn ID using Temporal's SideEffect.
func generateTurnID(ctx workflow.Context) string {
	var nanos int64
	encoded := workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
		return workflow.Now(ctx).UnixNano()
	})
	_ = encoded.Get(&nanos)
	return fmt.Sprintf("turn-%d", nanos)
}

// resolveInstructions loads worker-side AGENTS.md files and merges all
// instruction sources into the session configuration. Called once before
// the first turn. Non-fatal: falls back to CLI-provided docs on failure.
func (s *SessionState) resolveInstructions(ctx workflow.Context) {
	logger := workflow.GetLogger(ctx)

	// Load worker-side project docs via activity (runs on session task queue)
	var workerDocs string
	loadInput := activities.LoadWorkerInstructionsInput{
		Cwd: s.Config.Cwd,
	}

	actOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 2,
		},
	}
	if s.Config.SessionTaskQueue != "" {
		actOpts.TaskQueue = s.Config.SessionTaskQueue
	}
	loadCtx := workflow.WithActivityOptions(ctx, actOpts)

	var loadResult activities.LoadWorkerInstructionsOutput
	err := workflow.ExecuteActivity(loadCtx, "LoadWorkerInstructions", loadInput).Get(ctx, &loadResult)
	if err != nil {
		logger.Warn("Failed to load worker instructions, using CLI fallback", "error", err)
	} else {
		workerDocs = loadResult.ProjectDocs
	}

	// Merge all instruction sources
	merged := instructions.MergeInstructions(instructions.MergeInput{
		BaseOverride:             s.Config.BaseInstructions,
		CLIProjectDocs:           s.Config.CLIProjectDocs,
		WorkerProjectDocs:        workerDocs,
		UserPersonalInstructions: s.Config.UserPersonalInstructions,
		ApprovalMode:             string(s.Config.ApprovalMode),
		Cwd:                      s.Config.Cwd,
	})

	// Store merged results in config (persists through ContinueAsNew)
	s.Config.BaseInstructions = merged.Base
	s.Config.DeveloperInstructions = merged.Developer
	s.Config.UserInstructions = merged.User

	logger.Info("Instructions resolved",
		"base_len", len(merged.Base),
		"developer_len", len(merged.Developer),
		"user_len", len(merged.User))
}

// runMultiTurnLoop is the outer loop that waits for user input between turns.
func (s *SessionState) runMultiTurnLoop(ctx workflow.Context) (WorkflowResult, error) {
	logger := workflow.GetLogger(ctx)

	for {
		// Wait for pending user input (first turn has it set already)
		if !s.PendingUserInput && !s.ShutdownRequested {
			s.Phase = PhaseWaitingForInput
			s.ToolsInFlight = nil
			logger.Info("Waiting for user input or shutdown")
			timedOut, err := awaitWithIdleTimeout(ctx, func() bool {
				return s.PendingUserInput || s.ShutdownRequested
			})
			if err != nil {
				return WorkflowResult{}, fmt.Errorf("await failed: %w", err)
			}
			if timedOut {
				logger.Info("Idle timeout reached, triggering ContinueAsNew")
				return s.continueAsNew(ctx)
			}
		}

		// Check for shutdown
		if s.ShutdownRequested {
			logger.Info("Shutdown requested, completing workflow")
			return WorkflowResult{
				ConversationID:    s.ConversationID,
				TotalIterations:   s.IterationCount,
				TotalTokens:       s.TotalTokens,
				ToolCallsExecuted: s.ToolCallsExecuted,
				EndReason:         "shutdown",
			}, nil
		}

		// Reset for new turn
		s.PendingUserInput = false
		s.Interrupted = false
		s.IterationCount = 0

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
			logger.Info("Total iterations across turns reached CAN threshold",
				"total", s.TotalIterationsForCAN)
			return s.continueAsNew(ctx)
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

// runAgenticTurn runs a single agentic turn (LLM + tool loop).
// Returns (needsContinueAsNew, error).
//
// Maps to: codex-rs/core/src/codex.rs run_sampling_request
func (s *SessionState) runAgenticTurn(ctx workflow.Context) (bool, error) {
	logger := workflow.GetLogger(ctx)

	for s.IterationCount < s.MaxIterations {
		// Check for interrupt before each iteration
		if s.Interrupted {
			logger.Info("Turn interrupted")
			return false, nil
		}

		logger.Info("Starting iteration", "iteration", s.IterationCount, "turn_id", s.CurrentTurnID)

		// Get history for prompt
		historyItems, err := s.History.GetForPrompt()
		if err != nil {
			return false, fmt.Errorf("failed to get history: %w", err)
		}

		// Configure LLM activity options
		llmActivityOptions := workflow.ActivityOptions{
			StartToCloseTimeout: 2 * time.Minute,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval:    time.Second,
				BackoffCoefficient: 2.0,
				MaximumInterval:    30 * time.Second,
				MaximumAttempts:    3,
			},
		}
		llmCtx := workflow.WithActivityOptions(ctx, llmActivityOptions)

		// Set phase to LLM calling
		s.Phase = PhaseLLMCalling
		s.ToolsInFlight = nil

		// Call LLM Activity
		llmInput := activities.LLMActivityInput{
			History:               historyItems,
			ModelConfig:           s.Config.Model,
			ToolSpecs:             s.ToolSpecs,
			BaseInstructions:      s.Config.BaseInstructions,
			DeveloperInstructions: s.Config.DeveloperInstructions,
			UserInstructions:      s.Config.UserInstructions,
		}

		var llmResult activities.LLMActivityOutput
		err = workflow.ExecuteActivity(llmCtx, "ExecuteLLMCall", llmInput).Get(ctx, &llmResult)

		if err != nil {
			var appErr *temporal.ApplicationError
			if errors.As(err, &appErr) {
				switch appErr.Type() {
				case models.LLMErrTypeContextOverflow:
					turnCount, _ := s.History.GetTurnCount()
					keepTurns := turnCount / 2
					if keepTurns < 2 {
						keepTurns = 2
					}
					dropped, _ := s.History.DropOldestUserTurns(keepTurns)
					logger.Warn("Context overflow, compacted history",
						"dropped_items", dropped, "kept_turns", keepTurns)
					return true, nil

				case models.LLMErrTypeAPILimit:
					logger.Warn("API rate limit, sleeping for 1 minute")
					workflow.Sleep(ctx, time.Minute)
					continue

				case models.LLMErrTypeFatal:
					logger.Error("Fatal LLM error, ending turn", "error", err)
					_ = s.History.AddItem(models.ConversationItem{
						Type:    models.ItemTypeAssistantMessage,
						Content: fmt.Sprintf("[Error: %s]", appErr.Message()),
						TurnID:  s.CurrentTurnID,
					})
					return false, nil
				}
			}
			// General activity error (timeout, unknown, etc.) — surface to user
			logger.Error("LLM activity failed, ending turn", "error", err)
			_ = s.History.AddItem(models.ConversationItem{
				Type:    models.ItemTypeAssistantMessage,
				Content: fmt.Sprintf("[Error: LLM call failed: %v]", err),
				TurnID:  s.CurrentTurnID,
			})
			return false, nil
		}

		// Check for interrupt after LLM call
		if s.Interrupted {
			logger.Info("Turn interrupted after LLM call")
			return false, nil
		}

		// Track token usage
		s.TotalTokens += llmResult.TokenUsage.TotalTokens
		logger.Info("LLM call completed",
			"tokens", llmResult.TokenUsage.TotalTokens,
			"finish_reason", llmResult.FinishReason,
			"items", len(llmResult.Items))

		// Add all LLM response items to history
		// Matches Codex: record_into_history(items)
		for _, item := range llmResult.Items {
			if err := s.History.AddItem(item); err != nil {
				return false, fmt.Errorf("failed to add response item: %w", err)
			}
		}

		// Extract FunctionCall items for execution
		// Matches Codex: separate function calls from response items
		var functionCalls []models.ConversationItem
		for _, item := range llmResult.Items {
			if item.Type == models.ItemTypeFunctionCall {
				functionCalls = append(functionCalls, item)
			}
		}

		// Intercept request_user_input calls — these are handled by the workflow,
		// not dispatched as activities. Separate them from normal tool calls.
		// Maps to: codex-rs/protocol/src/request_user_input.rs
		hadUserInputCalls := false
		if len(functionCalls) > 0 {
			var normalCalls []models.ConversationItem
			for _, fc := range functionCalls {
				if fc.Name == "request_user_input" {
					hadUserInputCalls = true
					outputItem, err := s.handleRequestUserInput(ctx, fc)
					if err != nil {
						return false, err
					}
					if err := s.History.AddItem(outputItem); err != nil {
						return false, fmt.Errorf("failed to add user input response: %w", err)
					}
				} else {
					normalCalls = append(normalCalls, fc)
				}
			}
			functionCalls = normalCalls
		}

		// If only request_user_input calls were present (no normal tools),
		// continue to next LLM iteration so it can use the user's answers.
		if hadUserInputCalls && len(functionCalls) == 0 {
			// Check for interrupt/shutdown after user input
			if s.Interrupted || s.ShutdownRequested {
				return false, nil
			}
			s.IterationCount++
			continue
		}

		// Detect repeated identical tool calls (tight-loop prevention).
		// If the LLM is calling the same tools with the same args repeatedly,
		// it's likely stuck. End the turn early so the user can intervene.
		if len(functionCalls) > 0 && s.detectRepeatedToolCalls(functionCalls) {
			logger.Warn("Detected repeated identical tool calls",
				"repeat_count", s.repeatCount)
			_ = s.History.AddItem(models.ConversationItem{
				Type:    models.ItemTypeAssistantMessage,
				Content: "[Turn ended: detected repeated identical tool calls. Please try a different approach.]",
			})
			return false, nil
		}

		// Execute tools if present (parallel execution)
		if len(functionCalls) > 0 {
			// Classify which tools need user approval
			needsApproval, forbiddenResults := classifyToolsForApproval(
				functionCalls, s.Config.ApprovalMode, s.ExecPolicyRules)

			// Add forbidden results to history immediately (LLM will see them)
			for _, fr := range forbiddenResults {
				if err := s.History.AddItem(fr); err != nil {
					return false, fmt.Errorf("failed to add forbidden result: %w", err)
				}
			}

			// Remove forbidden tools from the function calls list
			if len(forbiddenResults) > 0 {
				forbiddenIDs := make(map[string]bool, len(forbiddenResults))
				for _, fr := range forbiddenResults {
					forbiddenIDs[fr.CallID] = true
				}
				var remaining []models.ConversationItem
				for _, fc := range functionCalls {
					if !forbiddenIDs[fc.CallID] {
						remaining = append(remaining, fc)
					}
				}
				functionCalls = remaining
				if len(functionCalls) == 0 {
					s.IterationCount++
					continue
				}
			}

			if len(needsApproval) > 0 {
				// Set approval pending state
				s.Phase = PhaseApprovalPending
				s.PendingApprovals = needsApproval
				s.ApprovalReceived = false
				s.ApprovalResponse = nil

				logger.Info("Waiting for tool approval", "count", len(needsApproval))

				// Wait for user approval or interrupt (no timeout — blocks until response)
				err := workflow.Await(ctx, func() bool {
					return s.ApprovalReceived || s.Interrupted || s.ShutdownRequested
				})
				if err != nil {
					return false, fmt.Errorf("approval await failed: %w", err)
				}

				s.PendingApprovals = nil

				if s.Interrupted || s.ShutdownRequested {
					logger.Info("Approval wait interrupted")
					return false, nil
				}

				// Apply approval decision — filter out denied tools
				var deniedResults []models.ConversationItem
				functionCalls, deniedResults = applyApprovalDecision(functionCalls, s.ApprovalResponse)

				// Add denied results to history so the LLM sees them
				for _, dr := range deniedResults {
					if err := s.History.AddItem(dr); err != nil {
						return false, fmt.Errorf("failed to add denied result: %w", err)
					}
				}

				// If all tools were denied, continue loop for next LLM iteration
				if len(functionCalls) == 0 {
					s.IterationCount++
					continue
				}
			}

			// Set phase to tool executing with names of tools in flight
			s.Phase = PhaseToolExecuting
			toolNames := make([]string, len(functionCalls))
			for i, fc := range functionCalls {
				toolNames[i] = fc.Name
			}
			s.ToolsInFlight = toolNames
			logger.Info("Executing tools", "count", len(functionCalls))

			toolResults, err := executeToolsInParallel(ctx, functionCalls, s.ToolSpecs, s.Config.Cwd, s.Config.SessionTaskQueue)
			if err != nil {
				_ = s.History.AddItem(models.ConversationItem{
					Type:    models.ItemTypeAssistantMessage,
					Content: fmt.Sprintf("[Error: tool execution failed: %v]", err),
					TurnID:  s.CurrentTurnID,
				})
				return false, nil
			}

			// Clear tools in flight
			s.ToolsInFlight = nil

			// On-failure mode: check for failed tools and offer escalation
			if s.Config.ApprovalMode == models.ApprovalOnFailure {
				toolResults, err = s.handleOnFailureEscalation(ctx, functionCalls, toolResults)
				if err != nil {
					return false, err
				}
			}

			// Track which tools were executed
			for _, fc := range functionCalls {
				s.ToolCallsExecuted = append(s.ToolCallsExecuted, fc.Name)
			}

			// Add all tool results to history as FunctionCallOutput items.
			for _, result := range toolResults {
				outputPayload := &models.FunctionCallOutputPayload{
					Content: result.Content,
					Success: result.Success,
				}

				item := models.ConversationItem{
					Type:   models.ItemTypeFunctionCallOutput,
					CallID: result.CallID,
					Output: outputPayload,
				}

				if err := s.History.AddItem(item); err != nil {
					return false, fmt.Errorf("failed to add tool result: %w", err)
				}
			}

			// Check for interrupt after tool execution
			if s.Interrupted {
				logger.Info("Turn interrupted after tool execution")
				return false, nil
			}

			// Continue loop to get next LLM response
			s.IterationCount++
			continue
		}

		// No function calls - check finish reason
		if llmResult.FinishReason == models.FinishReasonStop {
			logger.Info("Turn completed", "iterations", s.IterationCount, "turn_id", s.CurrentTurnID)
			return false, nil
		}

		// Other finish reasons without tool calls - turn done
		s.IterationCount++
		return false, nil
	}

	// Max iterations reached — end the turn with a message so the user can
	// decide what to do next. Previously this triggered ContinueAsNew which
	// allowed runaway loops to continue indefinitely across new workflow runs.
	if s.IterationCount >= s.MaxIterations {
		logger.Warn("Max iterations per turn reached", "iterations", s.IterationCount)
		_ = s.History.AddItem(models.ConversationItem{
			Type:    models.ItemTypeAssistantMessage,
			Content: fmt.Sprintf("[Turn ended: reached maximum of %d iterations without completing. The task may need to be broken into smaller steps.]", s.MaxIterations),
		})
		return false, nil
	}

	return false, nil
}

// executeToolsInParallel runs all tool activities in parallel and waits for all.
//
// Each tool gets a per-activity StartToCloseTimeout derived from:
//  1. timeout_ms argument provided by the LLM (highest priority)
//  2. DefaultTimeoutMs from the tool's ToolSpec
//  3. DefaultToolTimeoutMs constant as a fallback
//
// If sessionTaskQueue is non-empty, tool activities are dispatched to that queue
// (enabling per-session worker routing in multi-host mode).
//
// Maps to: codex-rs/core/src/tools/parallel.rs drain_in_flight
func executeToolsInParallel(ctx workflow.Context, functionCalls []models.ConversationItem, toolSpecs []tools.ToolSpec, cwd, sessionTaskQueue string) ([]activities.ToolActivityOutput, error) {
	logger := workflow.GetLogger(ctx)

	// Build a lookup map from tool name to spec for fast access.
	specByName := make(map[string]tools.ToolSpec, len(toolSpecs))
	for _, spec := range toolSpecs {
		specByName[spec.Name] = spec
	}

	// Start all tool activities in parallel using futures
	futures := make([]workflow.Future, len(functionCalls))
	for i, fc := range functionCalls {
		logger.Info("Starting tool execution", "tool", fc.Name, "call_id", fc.CallID)

		// Parse arguments from raw JSON string
		var args map[string]interface{}
		if fc.Arguments != "" {
			if err := json.Unmarshal([]byte(fc.Arguments), &args); err != nil {
				args = map[string]interface{}{"_raw": fc.Arguments}
			}
		}

		// Resolve per-tool timeout for StartToCloseTimeout.
		timeout := resolveToolTimeout(specByName, fc.Name, args)

		actOpts := workflow.ActivityOptions{
			StartToCloseTimeout: timeout,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval:    time.Second,
				BackoffCoefficient: 2.0,
				MaximumInterval:    time.Minute,
				MaximumAttempts:    5,
			},
		}
		if sessionTaskQueue != "" {
			actOpts.TaskQueue = sessionTaskQueue
		}
		toolCtx := workflow.WithActivityOptions(ctx, actOpts)

		input := activities.ToolActivityInput{
			CallID:    fc.CallID,
			ToolName:  fc.Name,
			Arguments: args,
			Cwd:       cwd,
		}
		futures[i] = workflow.ExecuteActivity(toolCtx, "ExecuteTool", input)
	}

	// Wait for ALL tools to complete.
	// Activity errors (ApplicationError) are converted to failed tool results
	// so the LLM can see what went wrong and decide how to proceed.
	results := make([]activities.ToolActivityOutput, len(functionCalls))
	for i, future := range futures {
		var result activities.ToolActivityOutput
		if err := future.Get(ctx, &result); err != nil {
			results[i] = toolActivityErrorToOutput(logger, functionCalls[i].CallID, functionCalls[i].Name, err)
		} else {
			results[i] = result
			logger.Info("Tool execution completed", "tool", functionCalls[i].Name)
		}
	}

	return results, nil
}

// buildToolSpecs builds tool specifications based on configuration.
func buildToolSpecs(config models.ToolsConfig) []tools.ToolSpec {
	specs := []tools.ToolSpec{}

	if config.EnableShell {
		specs = append(specs, tools.NewShellToolSpec())
	}

	if config.EnableReadFile {
		specs = append(specs, tools.NewReadFileToolSpec())
	}

	if config.EnableWriteFile {
		specs = append(specs, tools.NewWriteFileToolSpec())
	}

	if config.EnableListDir {
		specs = append(specs, tools.NewListDirToolSpec())
	}

	if config.EnableGrepFiles {
		specs = append(specs, tools.NewGrepFilesToolSpec())
	}

	if config.EnableApplyPatch {
		specs = append(specs, tools.NewApplyPatchToolSpec())
	}

	// request_user_input is always available (intercepted by workflow, not dispatched)
	specs = append(specs, tools.NewRequestUserInputToolSpec())

	return specs
}

// handleRequestUserInput intercepts a request_user_input tool call, parses the
// arguments, sets the pending phase, waits for the user's response, and returns
// a FunctionCallOutput item with the user's answers as JSON.
//
// Maps to: codex-rs/protocol/src/request_user_input.rs
func (s *SessionState) handleRequestUserInput(ctx workflow.Context, fc models.ConversationItem) (models.ConversationItem, error) {
	logger := workflow.GetLogger(ctx)

	// Parse and validate the arguments
	questions, err := parseRequestUserInputArgs(fc.Arguments)
	if err != nil {
		logger.Warn("Invalid request_user_input args", "error", err)
		falseVal := false
		return models.ConversationItem{
			Type:   models.ItemTypeFunctionCallOutput,
			CallID: fc.CallID,
			Output: &models.FunctionCallOutputPayload{
				Content: fmt.Sprintf("Invalid request_user_input arguments: %v", err),
				Success: &falseVal,
			},
		}, nil
	}

	// Set pending state
	s.Phase = PhaseUserInputPending
	s.PendingUserInputReq = &PendingUserInputRequest{
		CallID:    fc.CallID,
		Questions: questions,
	}
	s.UserInputQReceived = false
	s.UserInputQResponse = nil

	logger.Info("Waiting for user input response", "question_count", len(questions))

	// Wait for user response or interrupt
	err = workflow.Await(ctx, func() bool {
		return s.UserInputQReceived || s.Interrupted || s.ShutdownRequested
	})
	if err != nil {
		return models.ConversationItem{}, fmt.Errorf("user input await failed: %w", err)
	}

	s.PendingUserInputReq = nil

	if s.Interrupted || s.ShutdownRequested {
		logger.Info("User input wait interrupted")
		falseVal := false
		return models.ConversationItem{
			Type:   models.ItemTypeFunctionCallOutput,
			CallID: fc.CallID,
			Output: &models.FunctionCallOutputPayload{
				Content: "User input request was interrupted.",
				Success: &falseVal,
			},
		}, nil
	}

	// Build the response JSON
	responseJSON, err := json.Marshal(s.UserInputQResponse)
	if err != nil {
		return models.ConversationItem{}, fmt.Errorf("failed to marshal user input response: %w", err)
	}

	trueVal := true
	return models.ConversationItem{
		Type:   models.ItemTypeFunctionCallOutput,
		CallID: fc.CallID,
		Output: &models.FunctionCallOutputPayload{
			Content: string(responseJSON),
			Success: &trueVal,
		},
	}, nil
}

// parseRequestUserInputArgs validates and parses the request_user_input arguments.
// Returns parsed questions or an error if the args are invalid.
func parseRequestUserInputArgs(argsJSON string) ([]RequestUserInputQuestion, error) {
	var args struct {
		Questions []struct {
			ID       string `json:"id"`
			Header   string `json:"header,omitempty"`
			Question string `json:"question"`
			IsOther  bool   `json:"is_other,omitempty"`
			Options  []struct {
				Label       string `json:"label"`
				Description string `json:"description,omitempty"`
			} `json:"options"`
		} `json:"questions"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	if len(args.Questions) == 0 {
		return nil, fmt.Errorf("questions array must not be empty")
	}
	if len(args.Questions) > 4 {
		return nil, fmt.Errorf("at most 4 questions allowed, got %d", len(args.Questions))
	}

	questions := make([]RequestUserInputQuestion, len(args.Questions))
	for i, q := range args.Questions {
		if q.ID == "" {
			return nil, fmt.Errorf("question %d: id is required", i+1)
		}
		if q.Question == "" {
			return nil, fmt.Errorf("question %d: question text is required", i+1)
		}
		if len(q.Options) == 0 {
			return nil, fmt.Errorf("question %d: options must not be empty", i+1)
		}

		options := make([]RequestUserInputQuestionOption, len(q.Options))
		for j, opt := range q.Options {
			if opt.Label == "" {
				return nil, fmt.Errorf("question %d, option %d: label is required", i+1, j+1)
			}
			options[j] = RequestUserInputQuestionOption{
				Label:       opt.Label,
				Description: opt.Description,
			}
		}

		questions[i] = RequestUserInputQuestion{
			ID:       q.ID,
			Header:   q.Header,
			Question: q.Question,
			IsOther:  q.IsOther,
			Options:  options,
		}
	}

	return questions, nil
}

// toolActivityErrorToOutput converts a tool activity error into a ToolActivityOutput
// so the LLM can see what went wrong and decide how to proceed.
//
// Uses ApplicationError.Type() for classification and .Details() for structured context.
// Never parses error messages.
func toolActivityErrorToOutput(logger log.Logger, callID, toolName string, err error) activities.ToolActivityOutput {
	success := false
	reason := "unknown error"

	var appErr *temporal.ApplicationError
	var timeoutErr *temporal.TimeoutError
	var canceledErr *temporal.CanceledError

	switch {
	case errors.As(err, &appErr):
		logger.Warn("Tool activity failed",
			"tool", toolName,
			"error_type", appErr.Type(),
			"non_retryable", appErr.NonRetryable())

		// Extract structured context from Details — never parse the message.
		var details models.ToolErrorDetails
		if appErr.HasDetails() {
			_ = appErr.Details(&details)
			reason = details.Reason
		}

	case errors.As(err, &timeoutErr):
		logger.Warn("Tool activity timed out",
			"tool", toolName,
			"timeout_type", timeoutErr.TimeoutType())
		reason = "tool execution timed out"

	case errors.As(err, &canceledErr):
		logger.Warn("Tool activity canceled", "tool", toolName)
		reason = "tool execution was canceled"

	default:
		logger.Error("Tool activity failed with unexpected error",
			"tool", toolName, "error", err)
		reason = "activity execution failed"
	}

	return activities.ToolActivityOutput{
		CallID:  callID,
		Content: reason,
		Success: &success,
	}
}

// resolveToolTimeout determines the StartToCloseTimeout for a tool activity.
//
// Priority:
//  1. timeout_ms argument from LLM (per-invocation override)
//  2. DefaultTimeoutMs from the tool's ToolSpec
//  3. DefaultToolTimeoutMs constant as a global fallback
//
// Maps to: codex-rs/core/src/exec.rs timeout resolution for tool commands
func resolveToolTimeout(specByName map[string]tools.ToolSpec, toolName string, args map[string]interface{}) time.Duration {
	// 1. Check for LLM-provided timeout_ms in arguments.
	if args != nil {
		if v, ok := args["timeout_ms"]; ok {
			if ms, ok := toInt64(v); ok && ms > 0 {
				return time.Duration(ms) * time.Millisecond
			}
		}
	}

	// 2. Use the tool spec's default timeout.
	if spec, ok := specByName[toolName]; ok && spec.DefaultTimeoutMs > 0 {
		return time.Duration(spec.DefaultTimeoutMs) * time.Millisecond
	}

	// 3. Global fallback.
	return time.Duration(tools.DefaultToolTimeoutMs) * time.Millisecond
}

// classifyToolsForApproval determines which tool calls need user approval.
// Uses the exec policy engine when available, falling back to heuristic classification.
//
// Returns:
//   - pending: tools needing approval (shown to user)
//   - forbidden: tools that are forbidden (denied immediately)
//
// Maps to: Codex AskForApproval policy check before tool dispatch
func classifyToolsForApproval(
	functionCalls []models.ConversationItem,
	mode models.ApprovalMode,
	policyRules string,
) (pending []PendingApproval, forbidden []models.ConversationItem) {
	// Empty/unset mode or "never" → auto-approve all (backward compat)
	if mode == "" || mode == models.ApprovalNever {
		return nil, nil
	}

	// Build exec policy manager from serialized rules
	var policyMgr *execpolicy.ExecPolicyManager
	if policyRules != "" {
		mgr, err := execpolicy.LoadExecPolicyFromSource(policyRules)
		if err == nil {
			policyMgr = mgr
		}
	}

	for _, fc := range functionCalls {
		req, reason := evaluateToolApproval(fc.Name, fc.Arguments, policyMgr, mode)
		switch req {
		case tools.ApprovalSkip:
			continue // auto-approved
		case tools.ApprovalNeeded:
			pending = append(pending, PendingApproval{
				CallID:    fc.CallID,
				ToolName:  fc.Name,
				Arguments: fc.Arguments,
				Reason:    reason,
			})
		case tools.ApprovalForbidden:
			falseVal := false
			msg := "This command is forbidden by exec policy."
			if reason != "" {
				msg = fmt.Sprintf("Forbidden: %s", reason)
			}
			forbidden = append(forbidden, models.ConversationItem{
				Type:   models.ItemTypeFunctionCallOutput,
				CallID: fc.CallID,
				Output: &models.FunctionCallOutputPayload{
					Content: msg,
					Success: &falseVal,
				},
			})
		}
	}
	return pending, forbidden
}

// evaluateToolApproval determines the approval requirement for a single tool call.
// Returns the requirement and a human-readable reason.
func evaluateToolApproval(
	toolName, arguments string,
	policyMgr *execpolicy.ExecPolicyManager,
	mode models.ApprovalMode,
) (tools.ExecApprovalRequirement, string) {
	switch toolName {
	case "read_file", "list_dir", "grep_files", "request_user_input":
		return tools.ApprovalSkip, "" // Read-only / workflow-intercepted tools always safe

	case "shell":
		return evaluateShellApproval(arguments, policyMgr, mode)

	case "write_file", "apply_patch":
		if mode == models.ApprovalNever {
			return tools.ApprovalSkip, ""
		}
		return tools.ApprovalNeeded, "mutating file operation"

	default:
		if mode == models.ApprovalNever {
			return tools.ApprovalSkip, ""
		}
		return tools.ApprovalNeeded, "unknown tool"
	}
}

// evaluateShellApproval evaluates a shell tool call through the exec policy engine.
func evaluateShellApproval(
	arguments string,
	policyMgr *execpolicy.ExecPolicyManager,
	mode models.ApprovalMode,
) (tools.ExecApprovalRequirement, string) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return tools.ApprovalNeeded, "cannot parse arguments"
	}
	cmd, ok := args["command"].(string)
	if !ok || cmd == "" {
		return tools.ApprovalNeeded, "missing command"
	}

	// Use exec policy if available
	if policyMgr != nil {
		eval := policyMgr.GetEvaluation([]string{"bash", "-c", cmd}, string(mode))
		req := decisionToApprovalReq(eval.Decision)
		return req, eval.Justification
	}

	// Fallback to heuristic (same as before exec policy was added)
	if mode == models.ApprovalNever || mode == "" {
		return tools.ApprovalSkip, ""
	}
	if mode == models.ApprovalOnFailure {
		return tools.ApprovalSkip, "" // runs in sandbox
	}
	// unless-trusted: use command_safety heuristic
	mgr := execpolicy.NewExecPolicyManager(execpolicy.NewPolicy())
	return mgr.EvaluateShellCommand(cmd, string(mode)), ""
}

// decisionToApprovalReq maps a policy Decision to ExecApprovalRequirement.
func decisionToApprovalReq(d execpolicy.Decision) tools.ExecApprovalRequirement {
	switch d {
	case execpolicy.DecisionAllow:
		return tools.ApprovalSkip
	case execpolicy.DecisionPrompt:
		return tools.ApprovalNeeded
	case execpolicy.DecisionForbidden:
		return tools.ApprovalForbidden
	default:
		return tools.ApprovalNeeded
	}
}

// loadExecPolicy loads exec policy rules from the worker filesystem.
// Non-fatal: falls back to empty policy on failure.
func (s *SessionState) loadExecPolicy(ctx workflow.Context) {
	logger := workflow.GetLogger(ctx)

	if s.Config.CodexHome == "" {
		return
	}

	loadInput := activities.LoadExecPolicyInput{
		CodexHome: s.Config.CodexHome,
	}

	actOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 2,
		},
	}
	if s.Config.SessionTaskQueue != "" {
		actOpts.TaskQueue = s.Config.SessionTaskQueue
	}
	loadCtx := workflow.WithActivityOptions(ctx, actOpts)

	var loadResult activities.LoadExecPolicyOutput
	err := workflow.ExecuteActivity(loadCtx, "LoadExecPolicy", loadInput).Get(ctx, &loadResult)
	if err != nil {
		logger.Warn("Failed to load exec policy, using defaults", "error", err)
		return
	}

	s.ExecPolicyRules = loadResult.RulesSource
	logger.Info("Exec policy loaded", "rules_len", len(loadResult.RulesSource))
}

// sandboxDenialKeywords are output strings that indicate a sandbox/permission
// denial rather than a normal command failure.
// Matches Codex: codex-rs/core/src/exec.rs SANDBOX_DENIED_KEYWORDS
var sandboxDenialKeywords = []string{
	"operation not permitted",
	"permission denied",
	"read-only file system",
	"seccomp",
	"sandbox",
	"landlock",
	"failed to write file",
}

// isLikelySandboxDenial checks whether a failed tool result looks like it was
// blocked by a sandbox rather than failing for an ordinary reason (file not
// found, invalid args, etc.).
func isLikelySandboxDenial(output string) bool {
	lower := strings.ToLower(output)
	for _, kw := range sandboxDenialKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// truncate returns s truncated to n bytes with "..." appended if it was longer.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// handleOnFailureEscalation checks for failed tools in on-failure mode.
// For failed tools that look like sandbox denials, prompts the user to
// re-execute without sandbox. Normal failures are passed through to the LLM.
// Returns updated tool results (may include re-executed results).
func (s *SessionState) handleOnFailureEscalation(
	ctx workflow.Context,
	functionCalls []models.ConversationItem,
	toolResults []activities.ToolActivityOutput,
) ([]activities.ToolActivityOutput, error) {
	logger := workflow.GetLogger(ctx)

	// Find failed tools
	var escalations []EscalationRequest
	failedIndices := make(map[int]bool)

	for i, result := range toolResults {
		if result.Success != nil && !*result.Success {
			if isLikelySandboxDenial(result.Content) {
				// Looks like sandbox blocked it — escalate to user
				failedIndices[i] = true
				escalations = append(escalations, EscalationRequest{
					CallID:    result.CallID,
					ToolName:  functionCalls[i].Name,
					Arguments: functionCalls[i].Arguments,
					Output:    result.Content,
					Reason:    "command failed in sandbox",
				})
			} else {
				// Normal failure (file not found, bad args, etc.) — let LLM see it
				logger.Info("Tool failed but not sandbox-related, returning to LLM",
					"tool", functionCalls[i].Name, "output_prefix", truncate(result.Content, 100))
			}
		}
	}

	if len(escalations) == 0 {
		return toolResults, nil // No failures
	}

	// Enter escalation pending state
	s.Phase = PhaseEscalationPending
	s.PendingEscalations = escalations
	s.EscalationReceived = false
	s.EscalationResponse = nil

	logger.Info("Waiting for escalation decision", "failed_count", len(escalations))

	// Wait for escalation response
	err := workflow.Await(ctx, func() bool {
		return s.EscalationReceived || s.Interrupted || s.ShutdownRequested
	})
	if err != nil {
		return nil, fmt.Errorf("escalation await failed: %w", err)
	}

	s.PendingEscalations = nil

	if s.Interrupted || s.ShutdownRequested {
		logger.Info("Escalation wait interrupted")
		return toolResults, nil // Return original results
	}

	if s.EscalationResponse == nil {
		return toolResults, nil
	}

	// Re-execute approved tools without sandbox
	approvedSet := make(map[string]bool, len(s.EscalationResponse.Approved))
	for _, id := range s.EscalationResponse.Approved {
		approvedSet[id] = true
	}

	for i, result := range toolResults {
		if !failedIndices[i] || !approvedSet[result.CallID] {
			continue
		}

		logger.Info("Re-executing tool without sandbox", "tool", functionCalls[i].Name)

		// Re-execute without sandbox (no SandboxPolicy)
		reResults, err := executeToolsInParallel(
			ctx,
			[]models.ConversationItem{functionCalls[i]},
			s.ToolSpecs, s.Config.Cwd, s.Config.SessionTaskQueue,
		)
		if err != nil {
			continue // Keep original failed result
		}
		if len(reResults) > 0 {
			toolResults[i] = reResults[0]
		}
	}

	return toolResults, nil
}

// applyApprovalDecision filters function calls based on the approval response.
// Returns approved function calls and denied result items for history.
func applyApprovalDecision(functionCalls []models.ConversationItem, resp *ApprovalResponse) ([]models.ConversationItem, []models.ConversationItem) {
	if resp == nil {
		return functionCalls, nil
	}

	deniedSet := make(map[string]bool, len(resp.Denied))
	for _, id := range resp.Denied {
		deniedSet[id] = true
	}

	var approved []models.ConversationItem
	var denied []models.ConversationItem

	for _, fc := range functionCalls {
		if deniedSet[fc.CallID] {
			falseVal := false
			denied = append(denied, models.ConversationItem{
				Type:   models.ItemTypeFunctionCallOutput,
				CallID: fc.CallID,
				Output: &models.FunctionCallOutputPayload{
					Content: "User denied execution of this tool call.",
					Success: &falseVal,
				},
			})
		} else {
			approved = append(approved, fc)
		}
	}

	return approved, denied
}

// detectRepeatedToolCalls checks whether the current batch of tool calls is
// identical to the previous batch. Returns true if the same batch has been
// seen maxRepeatToolCalls times consecutively, indicating a tight loop.
func (s *SessionState) detectRepeatedToolCalls(calls []models.ConversationItem) bool {
	key := toolCallsKey(calls)
	if key == s.lastToolKey {
		s.repeatCount++
	} else {
		s.lastToolKey = key
		s.repeatCount = 1
	}
	return s.repeatCount >= maxRepeatToolCalls
}

// toolCallsKey produces a deterministic hash for a batch of tool calls
// based on tool names and arguments, used for repeat detection.
func toolCallsKey(calls []models.ConversationItem) string {
	// Build a sorted list of "name:args" strings for deterministic ordering.
	parts := make([]string, len(calls))
	for i, c := range calls {
		parts[i] = c.Name + ":" + c.Arguments
	}
	sort.Strings(parts)
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// toInt64 converts a JSON-decoded number (float64) to int64.
func toInt64(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	}
	return 0, false
}
