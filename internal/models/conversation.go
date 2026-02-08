// Package models contains shared types for the codex-temporal-go project.
//
// Corresponds to: codex-rs/core/src/protocol/models.rs
package models

// ConversationItemType represents the type of a conversation item
type ConversationItemType string

const (
	ItemTypeUserMessage      ConversationItemType = "user_message"
	ItemTypeAssistantMessage ConversationItemType = "assistant_message"
	ItemTypeToolCall         ConversationItemType = "tool_call"
	ItemTypeToolResult       ConversationItemType = "tool_result"
)

// ConversationItem represents a single item in the conversation history
//
// Maps to: codex-rs/core/src/protocol/models.rs ConversationItem
type ConversationItem struct {
	Type       ConversationItemType `json:"type"`
	Content    string               `json:"content,omitempty"`
	ToolCalls  []ToolCall           `json:"tool_calls,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"` // For tool results
	ToolOutput string               `json:"tool_output,omitempty"`  // For tool results
	ToolError  string               `json:"tool_error,omitempty"`   // For tool errors
}

// ToolCall represents a request to call a tool
//
// Maps to: codex-rs/core/src/protocol/models.rs ToolCall
type ToolCall struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ToolResult represents the result of a tool execution
//
// Maps to: codex-rs/core/src/tools/types.rs ToolResult
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
}

// FinishReason indicates why the LLM stopped generating
type FinishReason string

const (
	FinishReasonStop         FinishReason = "stop"          // Natural completion
	FinishReasonToolCalls    FinishReason = "tool_calls"    // LLM wants to call tools
	FinishReasonLength       FinishReason = "length"        // Hit token limit
	FinishReasonContentFilter FinishReason = "content_filter" // Content filtered
)

// TokenUsage tracks token consumption
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
