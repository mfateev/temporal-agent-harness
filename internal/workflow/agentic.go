// Package workflow contains Temporal workflow definitions.
//
// Corresponds to: codex-rs/core/src/codex.rs (run_turn, run_sampling_request)
package workflow

import (
	"errors"
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/mfateev/codex-temporal-go/internal/activities"
	"github.com/mfateev/codex-temporal-go/internal/history"
	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/tools"
)

// WorkflowInput is the initial input to start a conversation
//
// Maps to: codex-rs/core/src/codex.rs run_turn input
type WorkflowInput struct {
	ConversationID string               `json:"conversation_id"`
	UserMessage    string               `json:"user_message"`
	ModelConfig    models.ModelConfig   `json:"model_config"`
	ToolsConfig    models.ToolsConfig   `json:"tools_config"`
}

// WorkflowState is passed through ContinueAsNew
//
// Maps to: codex-rs/core/src/state/session.rs SessionState
type WorkflowState struct {
	ConversationID string                      `json:"conversation_id"`
	History        *history.InMemoryHistory    `json:"history"`
	ToolSpecs      []tools.ToolSpec            `json:"tool_specs"`
	ModelConfig    models.ModelConfig          `json:"model_config"`

	// Iteration tracking
	IterationCount int `json:"iteration_count"`
	MaxIterations  int `json:"max_iterations"`
}

// WorkflowResult is the final result of the workflow
type WorkflowResult struct {
	ConversationID    string   `json:"conversation_id"`
	TotalIterations   int      `json:"total_iterations"`
	TotalTokens       int      `json:"total_tokens"`
	ToolCallsExecuted []string `json:"tool_calls_executed"`
}

// AgenticWorkflow is the main durable agentic loop
//
// Maps to: codex-rs/core/src/codex.rs run_turn
func AgenticWorkflow(ctx workflow.Context, input WorkflowInput) (WorkflowResult, error) {
	// Initialize state on first run
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

	// Run the agentic loop
	return runAgenticLoop(ctx, state)
}

// AgenticWorkflowContinued handles ContinueAsNew
//
// This is called when the workflow continues after hitting max iterations
func AgenticWorkflowContinued(ctx workflow.Context, state WorkflowState) (WorkflowResult, error) {
	return runAgenticLoop(ctx, state)
}

// runAgenticLoop is the main loop logic
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
			// Handle different error types
			var activityErr *models.ActivityError
			if errors.As(err, &activityErr) {
				switch activityErr.Type {
				case models.ErrorTypeContextOverflow:
					// Trigger ContinueAsNew to compress history
					logger.Warn("Context overflow, triggering ContinueAsNew")
					state.IterationCount = 0
					return WorkflowResult{}, workflow.NewContinueAsNewError(ctx, "AgenticWorkflowContinued", state)

				case models.ErrorTypeAPILimit:
					// Wait and retry
					logger.Warn("API rate limit, sleeping for 1 minute")
					workflow.Sleep(ctx, time.Minute)
					continue

				case models.ErrorTypeFatal:
					// Stop workflow
					return WorkflowResult{}, fmt.Errorf("fatal error: %w", err)
				}
			}
			// For transient errors, Temporal will retry automatically
			return WorkflowResult{}, fmt.Errorf("LLM activity failed: %w", err)
		}

		// Track token usage
		totalTokens += llmResult.TokenUsage.TotalTokens
		logger.Info("LLM call completed", "tokens", llmResult.TokenUsage.TotalTokens, "finish_reason", llmResult.FinishReason)

		// Add assistant message to history
		err = state.History.AddItem(models.ConversationItem{
			Type:      models.ItemTypeAssistantMessage,
			Content:   llmResult.Content,
			ToolCalls: llmResult.ToolCalls,
		})
		if err != nil {
			return WorkflowResult{}, fmt.Errorf("failed to add assistant message: %w", err)
		}

		// Execute tools if present (parallel execution)
		if len(llmResult.ToolCalls) > 0 {
			logger.Info("Executing tools", "count", len(llmResult.ToolCalls))

			toolResults, err := executeToolsInParallel(ctx, llmResult.ToolCalls)
			if err != nil {
				return WorkflowResult{}, fmt.Errorf("failed to execute tools: %w", err)
			}

			// Track which tools were executed
			for _, tc := range llmResult.ToolCalls {
				toolCallsExecuted = append(toolCallsExecuted, tc.Name)
			}

			// Add all tool results to history
			for _, result := range toolResults {
				item := models.ConversationItem{
					Type:       models.ItemTypeToolResult,
					ToolCallID: result.ToolCallID,
				}

				if result.Error != "" {
					item.ToolError = result.Error
				} else {
					item.ToolOutput = result.Output
				}

				err = state.History.AddItem(item)
				if err != nil {
					return WorkflowResult{}, fmt.Errorf("failed to add tool result: %w", err)
				}
			}

			// Continue loop to get next LLM response
			state.IterationCount++
			continue
		}

		// No tool calls - check finish reason
		if llmResult.FinishReason == models.FinishReasonStop {
			// Conversation completed naturally
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

	// Max iterations reached - check if we should continue
	if state.IterationCount >= state.MaxIterations {
		logger.Info("Max iterations reached, triggering ContinueAsNew")

		// Check token count and consider ContinueAsNew
		tokenCount, _ := state.History.EstimateTokenCount()
		contextUsage := float64(tokenCount) / float64(state.ModelConfig.ContextWindow)

		if contextUsage > 0.8 {
			// High context usage, reset for ContinueAsNew
			logger.Info("High context usage, resetting for ContinueAsNew", "usage", contextUsage)
		}

		state.IterationCount = 0
		return WorkflowResult{}, workflow.NewContinueAsNewError(ctx, "AgenticWorkflowContinued", state)
	}

	// Workflow completed
	return WorkflowResult{
		ConversationID:    state.ConversationID,
		TotalIterations:   state.IterationCount,
		TotalTokens:       totalTokens,
		ToolCallsExecuted: toolCallsExecuted,
	}, nil
}

// executeToolsInParallel runs all tool activities in parallel and waits for all
//
// Maps to: codex-rs/core/src/tools/parallel.rs drain_in_flight
func executeToolsInParallel(ctx workflow.Context, toolCalls []models.ToolCall) ([]activities.ToolActivityOutput, error) {
	logger := workflow.GetLogger(ctx)

	// Configure tool activity options
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

	// Start all tool activities (parallel execution using futures)
	futures := make([]workflow.Future, len(toolCalls))
	for i, toolCall := range toolCalls {
		logger.Info("Starting tool execution", "tool", toolCall.Name, "id", toolCall.ID)

		input := activities.ToolActivityInput{
			ToolCall: toolCall,
		}
		futures[i] = workflow.ExecuteActivity(toolCtx, "ExecuteTool", input)
	}

	// Wait for ALL tools to complete
	results := make([]activities.ToolActivityOutput, len(toolCalls))
	for i, future := range futures {
		var result activities.ToolActivityOutput
		if err := future.Get(ctx, &result); err != nil {
			// Activity itself failed (not tool execution failure)
			// Record as tool error
			logger.Error("Tool activity failed", "tool", toolCalls[i].Name, "error", err)
			results[i] = activities.ToolActivityOutput{
				ToolCallID: toolCalls[i].ID,
				Error:      fmt.Sprintf("Activity execution failed: %v", err),
			}
		} else {
			results[i] = result
			if result.Error != "" {
				logger.Warn("Tool execution returned error", "tool", toolCalls[i].Name, "error", result.Error)
			} else {
				logger.Info("Tool execution completed", "tool", toolCalls[i].Name)
			}
		}
	}

	return results, nil
}

// buildToolSpecs builds tool specifications based on configuration
func buildToolSpecs(config models.ToolsConfig) []tools.ToolSpec {
	specs := []tools.ToolSpec{}

	if config.EnableShell {
		specs = append(specs, tools.NewShellToolSpec())
	}

	if config.EnableReadFile {
		specs = append(specs, tools.NewReadFileToolSpec())
	}

	// Future: Add more tools as they're implemented
	// if config.EnableWriteFile {
	//     specs = append(specs, tools.NewWriteFileToolSpec())
	// }

	return specs
}
