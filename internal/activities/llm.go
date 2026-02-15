// Package activities contains Temporal activity implementations.
//
// Corresponds to: codex-rs/core/src/codex.rs try_run_sampling_request
package activities

import (
	"context"
	"errors"

	"github.com/mfateev/temporal-agent-harness/internal/instructions"
	"github.com/mfateev/temporal-agent-harness/internal/llm"
	"github.com/mfateev/temporal-agent-harness/internal/models"
	"github.com/mfateev/temporal-agent-harness/internal/tools"
)

// LLMActivityInput is the input for the LLM activity.
//
// Maps to: codex-rs/core/src/codex.rs try_run_sampling_request input
type LLMActivityInput struct {
	History     []models.ConversationItem `json:"history"`
	ModelConfig models.ModelConfig        `json:"model_config"`
	ToolSpecs   []tools.ToolSpec          `json:"tool_specs"`

	// Instructions hierarchy (maps to Codex 3-tier system)
	BaseInstructions      string `json:"base_instructions,omitempty"`
	DeveloperInstructions string `json:"developer_instructions,omitempty"`
	UserInstructions      string `json:"user_instructions,omitempty"`

	// OpenAI Responses API: chain to previous response for incremental sends
	PreviousResponseID string `json:"previous_response_id,omitempty"`

	// Web search mode (OpenAI-only). When set, the native web_search tool is added.
	WebSearchMode models.WebSearchMode `json:"web_search_mode,omitempty"`
}

// LLMActivityOutput is the output from the LLM activity.
// Items contains all response items (assistant messages + function calls),
// matching Codex's SamplingRequestResult.
//
// Maps to: codex-rs/core/src/codex.rs SamplingRequestResult
type LLMActivityOutput struct {
	Items        []models.ConversationItem `json:"items"`
	FinishReason models.FinishReason       `json:"finish_reason"`
	TokenUsage   models.TokenUsage         `json:"token_usage"`

	// OpenAI Responses API: response ID for chaining
	ResponseID string `json:"response_id,omitempty"`
}

// LLMActivities contains LLM-related activities.
type LLMActivities struct {
	client llm.LLMClient
}

// NewLLMActivities creates a new LLMActivities instance.
func NewLLMActivities(client llm.LLMClient) *LLMActivities {
	return &LLMActivities{client: client}
}

// ExecuteLLMCall executes an LLM call and returns the complete response.
//
// Maps to: codex-rs/core/src/codex.rs try_run_sampling_request
func (a *LLMActivities) ExecuteLLMCall(ctx context.Context, input LLMActivityInput) (LLMActivityOutput, error) {
	request := llm.LLMRequest{
		History:               input.History,
		ModelConfig:           input.ModelConfig,
		ToolSpecs:             input.ToolSpecs,
		BaseInstructions:      input.BaseInstructions,
		DeveloperInstructions: input.DeveloperInstructions,
		UserInstructions:      input.UserInstructions,
		PreviousResponseID:    input.PreviousResponseID,
		WebSearchMode:         input.WebSearchMode,
	}

	response, err := a.client.Call(ctx, request)
	if err != nil {
		var activityErr *models.ActivityError
		if errors.As(err, &activityErr) {
			return LLMActivityOutput{}, models.WrapActivityError(activityErr)
		}
		return LLMActivityOutput{}, err
	}

	return LLMActivityOutput{
		Items:        response.Items,
		FinishReason: response.FinishReason,
		TokenUsage:   response.TokenUsage,
		ResponseID:   response.ResponseID,
	}, nil
}

// CompactActivityInput is the input for the compact activity.
//
// Maps to: codex-rs/core/src/compact.rs compact operation input
type CompactActivityInput struct {
	Model        string                      `json:"model"`
	Input        []models.ConversationItem   `json:"input"`
	Instructions string                      `json:"instructions,omitempty"`
}

// CompactActivityOutput is the output from the compact activity.
//
// Maps to: codex-rs/core/src/compact.rs compact operation output
type CompactActivityOutput struct {
	Items      []models.ConversationItem `json:"items"`
	TokenUsage models.TokenUsage         `json:"token_usage"`
}

// ExecuteCompact performs context compaction via the LLM provider.
// For OpenAI, uses remote compaction (POST /responses/compact).
// For other providers, uses local compaction (LLM summarization).
//
// Maps to: codex-rs/core/src/compact.rs compact operation
func (a *LLMActivities) ExecuteCompact(ctx context.Context, input CompactActivityInput) (CompactActivityOutput, error) {
	resp, err := a.client.Compact(ctx, llm.CompactRequest{
		Model:        input.Model,
		Input:        input.Input,
		Instructions: input.Instructions,
	})
	if err != nil {
		var activityErr *models.ActivityError
		if errors.As(err, &activityErr) {
			return CompactActivityOutput{}, models.WrapActivityError(activityErr)
		}
		return CompactActivityOutput{}, err
	}

	return CompactActivityOutput{
		Items:      resp.Items,
		TokenUsage: resp.TokenUsage,
	}, nil
}

// SuggestionInput is the input for the GenerateSuggestions activity.
type SuggestionInput struct {
	UserMessage      string            `json:"user_message"`
	AssistantMessage string            `json:"assistant_message"`
	ToolSummaries    []string          `json:"tool_summaries,omitempty"`
	ModelConfig      models.ModelConfig `json:"model_config"`
}

// SuggestionOutput is the output from the GenerateSuggestions activity.
type SuggestionOutput struct {
	Suggestion string `json:"suggestion"` // Single suggestion or empty string
}

// GenerateSuggestions calls a cheap/fast LLM to generate a single prompt
// suggestion after a turn completes. Best-effort: any error returns empty.
func (a *LLMActivities) GenerateSuggestions(ctx context.Context, input SuggestionInput) (SuggestionOutput, error) {
	userContent := instructions.BuildSuggestionInput(
		input.UserMessage, input.AssistantMessage, input.ToolSummaries)

	request := llm.LLMRequest{
		History: []models.ConversationItem{
			{
				Type:    models.ItemTypeUserMessage,
				Content: userContent,
			},
		},
		ModelConfig:      input.ModelConfig,
		BaseInstructions: instructions.SuggestionSystemPrompt,
	}

	response, err := a.client.Call(ctx, request)
	if err != nil {
		// Best-effort: return empty on any error
		return SuggestionOutput{}, nil
	}

	// Extract the first assistant message content
	for _, item := range response.Items {
		if item.Type == models.ItemTypeAssistantMessage && item.Content != "" {
			suggestion := instructions.ParseSuggestionResponse(item.Content)
			return SuggestionOutput{Suggestion: suggestion}, nil
		}
	}

	return SuggestionOutput{}, nil
}

// EstimateContextUsage estimates if we're approaching context window limits.
func (a *LLMActivities) EstimateContextUsage(ctx context.Context, history []models.ConversationItem, contextWindow int) (float64, error) {
	totalChars := 0
	for _, item := range history {
		totalChars += len(item.Content)
		totalChars += len(item.Arguments)
		totalChars += len(item.Name)
		if item.Output != nil {
			totalChars += len(item.Output.Content)
		}
	}

	estimatedTokens := totalChars / 4
	usage := float64(estimatedTokens) / float64(contextWindow)
	return usage, nil
}
