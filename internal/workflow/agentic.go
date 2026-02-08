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

// AgenticWorkflow is the main durable agentic loop.
//
// Maps to: codex-rs/core/src/codex.rs run_turn
func AgenticWorkflow(ctx workflow.Context, input WorkflowInput) (WorkflowResult, error) {
	state := WorkflowState{
		ConversationID: input.ConversationID,
		History:        history.NewInMemoryHistory(),
		ModelConfig:    input.ModelConfig,
		MaxIterations:  20,
		IterationCount: 0,
	}

	// Build tool specs based on configuration
	state.ToolSpecs = buildToolSpecs(input.ToolsConfig)

	// Add initial user message to history
	err := state.History.AddItem(models.ConversationItem{
		Type:    models.ItemTypeUserMessage,
		Content: input.UserMessage,
	})
	if err != nil {
		return WorkflowResult{}, fmt.Errorf("failed to add user message: %w", err)
	}

	return runAgenticLoop(ctx, state)
}

// AgenticWorkflowContinued handles ContinueAsNew.
func AgenticWorkflowContinued(ctx workflow.Context, state WorkflowState) (WorkflowResult, error) {
	// Restore History interface from serialized HistoryItems
	state.initHistory()
	return runAgenticLoop(ctx, state)
}

// runAgenticLoop is the main loop logic.
//
// Maps to: codex-rs/core/src/codex.rs run_sampling_request
func runAgenticLoop(ctx workflow.Context, state WorkflowState) (WorkflowResult, error) {
	logger := workflow.GetLogger(ctx)
	totalTokens := 0
	toolCallsExecuted := []string{}

	for state.IterationCount < state.MaxIterations {
		logger.Info("Starting iteration", "iteration", state.IterationCount)

		// Get history for prompt
		historyItems, err := state.History.GetForPrompt()
		if err != nil {
			return WorkflowResult{}, fmt.Errorf("failed to get history: %w", err)
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
			History:     historyItems,
			ModelConfig: state.ModelConfig,
			ToolSpecs:   state.ToolSpecs,
		}

		var llmResult activities.LLMActivityOutput
		err = workflow.ExecuteActivity(llmCtx, "ExecuteLLMCall", llmInput).Get(ctx, &llmResult)

		if err != nil {
			var activityErr *models.ActivityError
			if errors.As(err, &activityErr) {
				switch activityErr.Type {
				case models.ErrorTypeContextOverflow:
					logger.Warn("Context overflow, triggering ContinueAsNew")
					state.IterationCount = 0
					state.syncHistoryItems()
					return WorkflowResult{}, workflow.NewContinueAsNewError(ctx, "AgenticWorkflowContinued", state)

				case models.ErrorTypeAPILimit:
					logger.Warn("API rate limit, sleeping for 1 minute")
					workflow.Sleep(ctx, time.Minute)
					continue

				case models.ErrorTypeFatal:
					return WorkflowResult{}, fmt.Errorf("fatal error: %w", err)
				}
			}
			return WorkflowResult{}, fmt.Errorf("LLM activity failed: %w", err)
		}

		// Track token usage
		totalTokens += llmResult.TokenUsage.TotalTokens
		logger.Info("LLM call completed",
			"tokens", llmResult.TokenUsage.TotalTokens,
			"finish_reason", llmResult.FinishReason,
			"items", len(llmResult.Items))

		// Add all LLM response items to history
		// Matches Codex: record_into_history(items)
		for _, item := range llmResult.Items {
			if err := state.History.AddItem(item); err != nil {
				return WorkflowResult{}, fmt.Errorf("failed to add response item: %w", err)
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

			toolResults, err := executeToolsInParallel(ctx, functionCalls)
			if err != nil {
				return WorkflowResult{}, fmt.Errorf("failed to execute tools: %w", err)
			}

			// Track which tools were executed
			for _, fc := range functionCalls {
				toolCallsExecuted = append(toolCallsExecuted, fc.Name)
			}

			// Add all tool results to history as FunctionCallOutput items.
			// Errors from tool activities have already been converted to
			// results with Success=false in executeToolsInParallel.
			// Matches Codex: drain_in_flight() -> record results
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

				if err := state.History.AddItem(item); err != nil {
					return WorkflowResult{}, fmt.Errorf("failed to add tool result: %w", err)
				}
			}

			// Continue loop to get next LLM response
			state.IterationCount++
			continue
		}

		// No function calls - check finish reason
		if llmResult.FinishReason == models.FinishReasonStop {
			logger.Info("Conversation completed", "iterations", state.IterationCount)
			return WorkflowResult{
				ConversationID:    state.ConversationID,
				TotalIterations:   state.IterationCount + 1,
				TotalTokens:       totalTokens,
				ToolCallsExecuted: toolCallsExecuted,
			}, nil
		}

		// Other finish reasons without tool calls - break
		state.IterationCount++
		break
	}

	// Max iterations reached
	if state.IterationCount >= state.MaxIterations {
		logger.Info("Max iterations reached, triggering ContinueAsNew")

		tokenCount, _ := state.History.EstimateTokenCount()
		contextUsage := float64(tokenCount) / float64(state.ModelConfig.ContextWindow)

		if contextUsage > 0.8 {
			logger.Info("High context usage", "usage", contextUsage)
		}

		state.IterationCount = 0
		state.syncHistoryItems()
		return WorkflowResult{}, workflow.NewContinueAsNewError(ctx, "AgenticWorkflowContinued", state)
	}

	return WorkflowResult{
		ConversationID:    state.ConversationID,
		TotalIterations:   state.IterationCount,
		TotalTokens:       totalTokens,
		ToolCallsExecuted: toolCallsExecuted,
	}, nil
}

// executeToolsInParallel runs all tool activities in parallel and waits for all.
//
// Maps to: codex-rs/core/src/tools/parallel.rs drain_in_flight
func executeToolsInParallel(ctx workflow.Context, functionCalls []models.ConversationItem) ([]activities.ToolActivityOutput, error) {
	logger := workflow.GetLogger(ctx)

	toolActivityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    5,
		},
	}
	toolCtx := workflow.WithActivityOptions(ctx, toolActivityOptions)

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

		input := activities.ToolActivityInput{
			CallID:    fc.CallID,
			ToolName:  fc.Name,
			Arguments: args,
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
	if errors.As(err, &appErr) {
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
	} else {
		logger.Error("Tool activity failed with non-ApplicationError",
			"tool", toolName, "error", err)
		reason = "activity execution failed"
	}

	return activities.ToolActivityOutput{
		CallID:  callID,
		Content: reason,
		Success: &success,
	}
}
