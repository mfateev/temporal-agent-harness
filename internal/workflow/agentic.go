// Package workflow contains Temporal workflow definitions.
//
// Corresponds to: codex-rs/core/src/codex.rs (run_turn, run_sampling_request)
package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/mfateev/codex-temporal-go/internal/activities"
	"github.com/mfateev/codex-temporal-go/internal/history"
	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/tools"
)

// IdleTimeout is how long the workflow waits for user input before triggering ContinueAsNew.
const IdleTimeout = 24 * time.Hour

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

// runMultiTurnLoop is the outer loop that waits for user input between turns.
func (s *SessionState) runMultiTurnLoop(ctx workflow.Context) (WorkflowResult, error) {
	logger := workflow.GetLogger(ctx)

	for {
		// Wait for pending user input (first turn has it set already)
		if !s.PendingUserInput && !s.ShutdownRequested {
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

		// Turn complete — add TurnComplete marker (unless interrupted, which already added it)
		if !s.Interrupted {
			_ = s.History.AddItem(models.ConversationItem{
				Type:   models.ItemTypeTurnComplete,
				TurnID: s.CurrentTurnID,
			})
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
			var activityErr *models.ActivityError
			if errors.As(err, &activityErr) {
				switch activityErr.Type {
				case models.ErrorTypeContextOverflow:
					logger.Warn("Context overflow, triggering ContinueAsNew")
					return true, nil

				case models.ErrorTypeAPILimit:
					logger.Warn("API rate limit, sleeping for 1 minute")
					workflow.Sleep(ctx, time.Minute)
					continue

				case models.ErrorTypeFatal:
					return false, fmt.Errorf("fatal error: %w", err)
				}
			}
			return false, fmt.Errorf("LLM activity failed: %w", err)
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

		// Execute tools if present (parallel execution)
		if len(functionCalls) > 0 {
			logger.Info("Executing tools", "count", len(functionCalls))

			toolResults, err := executeToolsInParallel(ctx, functionCalls, s.ToolSpecs, s.Config.Cwd)
			if err != nil {
				return false, fmt.Errorf("failed to execute tools: %w", err)
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

	// Max iterations reached — need ContinueAsNew
	if s.IterationCount >= s.MaxIterations {
		logger.Info("Max iterations reached, triggering ContinueAsNew")

		tokenCount, _ := s.History.EstimateTokenCount()
		contextUsage := float64(tokenCount) / float64(s.Config.Model.ContextWindow)

		if contextUsage > 0.8 {
			logger.Info("High context usage", "usage", contextUsage)
		}

		return true, nil
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
// Maps to: codex-rs/core/src/tools/parallel.rs drain_in_flight
func executeToolsInParallel(ctx workflow.Context, functionCalls []models.ConversationItem, toolSpecs []tools.ToolSpec, cwd string) ([]activities.ToolActivityOutput, error) {
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

		toolCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: timeout,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval:    time.Second,
				BackoffCoefficient: 2.0,
				MaximumInterval:    time.Minute,
				MaximumAttempts:    5,
			},
		})

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

	return specs
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
