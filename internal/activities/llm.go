// Package activities contains Temporal activity implementations.
//
// Corresponds to: codex-rs/core/src/codex.rs try_run_sampling_request
package activities

import (
	"context"
	"fmt"

	"github.com/mfateev/codex-temporal-go/internal/llm"
	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/tools"
)

// LLMActivityInput is the input for the LLM activity
//
// Maps to: codex-rs/core/src/codex.rs try_run_sampling_request input
type LLMActivityInput struct {
	History     []models.ConversationItem `json:"history"`
	ModelConfig models.ModelConfig        `json:"model_config"`
	ToolSpecs   []tools.ToolSpec          `json:"tool_specs"`
}

// LLMActivityOutput is the output from the LLM activity
//
// Maps to: codex-rs/core/src/codex.rs SamplingRequestResult
type LLMActivityOutput struct {
	Content      string               `json:"content"`
	ToolCalls    []models.ToolCall    `json:"tool_calls,omitempty"`
	FinishReason models.FinishReason  `json:"finish_reason"`
	TokenUsage   models.TokenUsage    `json:"token_usage"`
}

// LLMActivities contains LLM-related activities
type LLMActivities struct {
	client llm.LLMClient
}

// NewLLMActivities creates a new LLMActivities instance
func NewLLMActivities(client llm.LLMClient) *LLMActivities {
	return &LLMActivities{
		client: client,
	}
}

// ExecuteLLMCall executes an LLM call and returns the complete response
//
// Maps to: codex-rs/core/src/codex.rs try_run_sampling_request
func (a *LLMActivities) ExecuteLLMCall(ctx context.Context, input LLMActivityInput) (LLMActivityOutput, error) {
	// Prepare LLM request
	request := llm.LLMRequest{
		History:     input.History,
		ModelConfig: input.ModelConfig,
		ToolSpecs:   input.ToolSpecs,
	}

	// Call LLM
	response, err := a.client.Call(ctx, request)
	if err != nil {
		// Error is already categorized by the LLM client
		return LLMActivityOutput{}, err
	}

	// Convert to activity output
	output := LLMActivityOutput{
		Content:      response.Content,
		ToolCalls:    response.ToolCalls,
		FinishReason: response.FinishReason,
		TokenUsage:   response.TokenUsage,
	}

	return output, nil
}

// EstimateContextUsage estimates if we're approaching context window limits
//
// This is a helper activity for context management
func (a *LLMActivities) EstimateContextUsage(ctx context.Context, history []models.ConversationItem, contextWindow int) (float64, error) {
	// Simple token estimation (4 chars per token)
	totalChars := 0
	for _, item := range history {
		totalChars += len(item.Content)
		totalChars += len(item.ToolOutput)
		totalChars += len(item.ToolError)

		for _, tc := range item.ToolCalls {
			totalChars += len(tc.Name)
			totalChars += len(fmt.Sprintf("%v", tc.Arguments))
		}
	}

	estimatedTokens := totalChars / 4
	usage := float64(estimatedTokens) / float64(contextWindow)

	return usage, nil
}
