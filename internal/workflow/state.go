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

// WorkflowInput is the initial input to start a conversation.
//
// Maps to: codex-rs/core/src/codex.rs run_turn input
type WorkflowInput struct {
	ConversationID string                      `json:"conversation_id"`
	UserMessage    string                      `json:"user_message"`
	Config         models.SessionConfiguration `json:"config"`
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
}

// WorkflowResult is the final result of the workflow.
type WorkflowResult struct {
	ConversationID    string   `json:"conversation_id"`
	TotalIterations   int      `json:"total_iterations"`
	TotalTokens       int      `json:"total_tokens"`
	ToolCallsExecuted []string `json:"tool_calls_executed"`
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
