// Package llm provides LLM client integrations.
//
// Corresponds to: codex-rs/core/src/client.rs
package llm

import (
	"context"

	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/tools"
)

// LLMRequest represents a request to the LLM
//
// Maps to: codex-rs/core/src/client.rs ModelClientSession input
type LLMRequest struct {
	History     []models.ConversationItem `json:"history"`
	ModelConfig models.ModelConfig        `json:"model_config"`
	ToolSpecs   []tools.ToolSpec          `json:"tool_specs"`
}

// LLMResponse represents a response from the LLM
//
// Maps to: codex-rs/core/src/client.rs ModelClientSession output
type LLMResponse struct {
	Content      string               `json:"content"`
	ToolCalls    []models.ToolCall    `json:"tool_calls,omitempty"`
	FinishReason models.FinishReason  `json:"finish_reason"`
	TokenUsage   models.TokenUsage    `json:"token_usage"`
}

// LLMClient is the interface for LLM providers
//
// Maps to: codex-rs/core/src/client.rs ModelClient trait
type LLMClient interface {
	// Call sends a request to the LLM and returns the complete response (buffered, not streaming)
	Call(ctx context.Context, request LLMRequest) (LLMResponse, error)
}
