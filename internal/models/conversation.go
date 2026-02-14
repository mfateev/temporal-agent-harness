// Package models contains shared types for the temporal-agent-harness project.
//
// Corresponds to: codex-rs/core/src/protocol (ResponseItem, ToolCall, etc.)
package models

// ConversationItemType matches Codex's ResponseItem enum variants.
//
// See: codex-rs/core/src/protocol ResponseItem
type ConversationItemType string

const (
	ItemTypeUserMessage        ConversationItemType = "user_message"         // Codex: ResponseItem::UserMessage
	ItemTypeAssistantMessage   ConversationItemType = "assistant_message"    // Codex: ResponseItem::AssistantMessage
	ItemTypeFunctionCall       ConversationItemType = "function_call"        // Codex: ResponseItem::FunctionCall
	ItemTypeFunctionCallOutput ConversationItemType = "function_call_output" // Codex: ResponseItem::FunctionCallOutput
	ItemTypeCompaction         ConversationItemType = "compaction"            // Codex: ResponseItem::Compaction

	// Turn lifecycle markers (maps to Codex EventMsg::TurnStarted / EventMsg::TurnComplete)
	ItemTypeTurnStarted  ConversationItemType = "turn_started"  // Codex: EventMsg::TurnStarted
	ItemTypeTurnComplete ConversationItemType = "turn_complete"  // Codex: EventMsg::TurnComplete
)

// FunctionCallOutputPayload matches Codex's FunctionCallOutputPayload.
//
// See: codex-rs/core/src/protocol FunctionCallOutputPayload
type FunctionCallOutputPayload struct {
	Content string `json:"content"`
	Success *bool  `json:"success,omitempty"`
}

// ConversationItem matches Codex's ResponseItem enum.
// Different fields are populated depending on Type.
//
// Maps to: codex-rs/core/src/protocol ResponseItem
//
// Variant field mapping:
//   UserMessage:        Content
//   AssistantMessage:   Content
//   FunctionCall:       CallID, Name, Arguments
//   FunctionCallOutput: CallID, Output
type ConversationItem struct {
	Type ConversationItemType `json:"type"`

	// Seq is a monotonically increasing sequence number assigned by history.
	// Used by the CLI to track which items have already been rendered.
	Seq int `json:"seq"`

	// UserMessage / AssistantMessage fields
	Content string `json:"content,omitempty"`

	// FunctionCall fields (Codex: ResponseItem::FunctionCall)
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"` // Raw JSON string (matches Codex's FunctionCall.arguments)

	// FunctionCallOutput fields (Codex: ResponseItem::FunctionCallOutput)
	// CallID is shared with FunctionCall
	Output *FunctionCallOutputPayload `json:"output,omitempty"`

	// Turn tracking (maps to Codex TurnContext.turn_id)
	TurnID string `json:"turn_id,omitempty"`
}

// ToolCall represents a parsed tool call for internal dispatch.
// This is separate from the ConversationItem representation - it holds
// parsed arguments ready for execution.
//
// Maps to: codex-rs/core/src/tools/router.rs ToolCall
type ToolCall struct {
	CallID    string                 `json:"call_id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// FinishReason indicates why the LLM stopped generating.
type FinishReason string

const (
	FinishReasonStop          FinishReason = "stop"
	FinishReasonToolCalls     FinishReason = "tool_calls"
	FinishReasonLength        FinishReason = "length"
	FinishReasonContentFilter FinishReason = "content_filter"
)

// TokenUsage tracks token consumption.
//
// Maps to: codex-rs TokenUsageInfo
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CachedTokens     int `json:"cached_tokens"`
}
