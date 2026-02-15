// Package llm provides LLM client integrations.
//
// Corresponds to: codex-rs/core/src/client.rs
package llm

import (
	"context"
	"fmt"
	"net/http"

	"github.com/mfateev/temporal-agent-harness/internal/models"
	"github.com/mfateev/temporal-agent-harness/internal/tools"
)

// LLMRequest represents a request to the LLM.
//
// Maps to: codex-rs/core/src/client_common.rs Prompt
type LLMRequest struct {
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

// LLMResponse represents a response from the LLM.
// Items contains all response items (assistant messages + function calls),
// matching Codex's SamplingRequestResult which returns Vec<ResponseItem>.
//
// Maps to: codex-rs/core/src/codex.rs SamplingRequestResult
type LLMResponse struct {
	Items        []models.ConversationItem `json:"items"`
	FinishReason models.FinishReason       `json:"finish_reason"`
	TokenUsage   models.TokenUsage         `json:"token_usage"`

	// OpenAI Responses API: response ID for chaining via PreviousResponseID
	ResponseID string `json:"response_id,omitempty"`
}

// CompactRequest represents a request to compact conversation history.
//
// Maps to: codex-rs/core/src/compact.rs CompactRequest
type CompactRequest struct {
	Model        string                      `json:"model"`
	Input        []models.ConversationItem   `json:"input"`
	Instructions string                      `json:"instructions,omitempty"`
}

// CompactResponse represents the result of a compaction operation.
// Items contains the compacted history to use as input for the next call.
//
// Maps to: codex-rs/core/src/compact.rs CompactResponse
type CompactResponse struct {
	Items      []models.ConversationItem `json:"items"`
	TokenUsage models.TokenUsage         `json:"token_usage"`
}

// LLMClient is the interface for LLM providers.
//
// Maps to: codex-rs/core/src/client.rs ModelClient trait
type LLMClient interface {
	Call(ctx context.Context, request LLMRequest) (LLMResponse, error)
	Compact(ctx context.Context, request CompactRequest) (CompactResponse, error)
}

// classifyByStatusCode maps an HTTP status code to the appropriate ActivityError.
// Shared by all provider error classifiers.
//
// Classification:
//   - 429 (Too Many Requests): rate limit, retryable with delay
//   - 408 (Request Timeout), 409 (Conflict): transient, retryable
//   - Other 4xx: fatal client error, non-retryable (e.g., 400, 401, 403, 404)
//   - 5xx: transient server error, retryable
func classifyByStatusCode(statusCode int, err error) *models.ActivityError {
	switch {
	case statusCode == http.StatusTooManyRequests:
		return models.NewAPILimitError(fmt.Sprintf("rate limit (%d): %v", statusCode, err))
	case statusCode == http.StatusRequestTimeout || statusCode == http.StatusConflict:
		return models.NewTransientError(fmt.Sprintf("retryable error (%d): %v", statusCode, err))
	case statusCode >= 400 && statusCode < 500:
		return models.NewFatalError(fmt.Sprintf("client error (%d): %v", statusCode, err))
	case statusCode >= 500:
		return models.NewTransientError(fmt.Sprintf("server error (%d): %v", statusCode, err))
	default:
		return models.NewTransientError(fmt.Sprintf("unexpected status (%d): %v", statusCode, err))
	}
}
