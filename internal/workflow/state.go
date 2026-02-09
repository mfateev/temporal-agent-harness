// Package workflow contains Temporal workflow definitions.
//
// state.go manages workflow state, separated from workflow logic.
//
// Maps to: codex-rs/core/src/state/session.rs SessionState
package workflow

import (
	"github.com/mfateev/codex-temporal-go/internal/history"
	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/tools"
)

// Handler name constants for Temporal query and update handlers.
const (
	// QueryGetConversationItems returns conversation history.
	// Maps to: Codex ContextManager::raw_items()
	QueryGetConversationItems = "get_conversation_items"

	// UpdateUserInput submits a new user message to the workflow.
	// Maps to: Codex Op::UserInput / turn/start
	UpdateUserInput = "user_input"

	// UpdateInterrupt aborts the current turn.
	// Maps to: Codex Op::Interrupt
	UpdateInterrupt = "interrupt"

	// UpdateShutdown ends the session.
	// Maps to: Codex Op::Shutdown
	UpdateShutdown = "shutdown"
)

// WorkflowInput is the initial input to start a conversation.
//
// Maps to: codex-rs/core/src/codex.rs run_turn input
type WorkflowInput struct {
	ConversationID string                      `json:"conversation_id"`
	UserMessage    string                      `json:"user_message"`
	Config         models.SessionConfiguration `json:"config"`
}

// UserInput is the payload for the user_input Update.
// Maps to: codex-rs/protocol/src/user_input.rs UserInput
type UserInput struct {
	Content string `json:"content"`
}

// UserInputAccepted is returned by the user_input Update after acceptance.
// Maps to: Codex submit() return value (submission ID)
type UserInputAccepted struct {
	TurnID string `json:"turn_id"`
}

// InterruptRequest is the payload for the interrupt Update.
// Maps to: codex-rs/protocol/src/protocol.rs Op::Interrupt
type InterruptRequest struct{}

// InterruptResponse is returned by the interrupt Update.
// Maps to: Codex EventMsg::TurnAborted
type InterruptResponse struct {
	Acknowledged bool `json:"acknowledged"`
}

// ShutdownRequest is the payload for the shutdown Update.
// Maps to: codex-rs/protocol/src/protocol.rs Op::Shutdown
type ShutdownRequest struct {
	Reason string `json:"reason,omitempty"`
}

// ShutdownResponse is returned by the shutdown Update.
// Maps to: Codex EventMsg::ShutdownComplete
type ShutdownResponse struct {
	Acknowledged bool `json:"acknowledged"`
}

// SessionState is passed through ContinueAsNew.
// Uses ContextManager interface to allow pluggable storage backends.
//
// Corresponds to: codex-rs/core/src/state/session.rs SessionState
type SessionState struct {
	ConversationID string                      `json:"conversation_id"`
	History        history.ContextManager      `json:"-"`             // Not serialized directly; see note below
	HistoryItems   []models.ConversationItem   `json:"history_items"` // Serialized form for ContinueAsNew
	ToolSpecs      []tools.ToolSpec            `json:"tool_specs"`
	Config         models.SessionConfiguration `json:"config"`

	// Iteration tracking
	IterationCount int `json:"iteration_count"`
	MaxIterations  int `json:"max_iterations"`

	// Multi-turn state
	PendingUserInput  bool   `json:"pending_user_input"`   // New user input waiting
	ShutdownRequested bool   `json:"shutdown_requested"`   // Session shutdown requested
	Interrupted       bool   `json:"interrupted"`          // Current turn interrupted
	CurrentTurnID     string `json:"current_turn_id"`      // Active turn ID

	// Cumulative stats (persist across ContinueAsNew)
	TotalTokens       int      `json:"total_tokens"`
	ToolCallsExecuted []string `json:"tool_calls_executed"`
}

// WorkflowResult is the final result of the workflow.
type WorkflowResult struct {
	ConversationID    string   `json:"conversation_id"`
	TotalIterations   int      `json:"total_iterations"`
	TotalTokens       int      `json:"total_tokens"`
	ToolCallsExecuted []string `json:"tool_calls_executed"`
	EndReason         string   `json:"end_reason,omitempty"` // "shutdown", "error"
}

// initHistory initializes the History field from HistoryItems.
// Called after deserialization (ContinueAsNew) to restore the interface.
func (s *SessionState) initHistory() {
	h := history.NewInMemoryHistory()
	for _, item := range s.HistoryItems {
		h.AddItem(item)
	}
	s.History = h
}

// syncHistoryItems copies history to HistoryItems for serialization.
// Called before ContinueAsNew to persist state.
func (s *SessionState) syncHistoryItems() {
	items, _ := s.History.GetRawItems()
	s.HistoryItems = items
}
