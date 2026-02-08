// Package llm provides LLM client integrations.
//
// Corresponds to: codex-rs/core/src/client.rs
package llm

import (
	"context"

	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/tools"
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
}

// LLMClient is the interface for LLM providers.
//
// Maps to: codex-rs/core/src/client.rs ModelClient trait
type LLMClient interface {
	Call(ctx context.Context, request LLMRequest) (LLMResponse, error)
}
