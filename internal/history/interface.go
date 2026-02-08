// Package history provides conversation history management interfaces and implementations.
//
// Corresponds to: codex-rs/core/src/state/session.rs (ContextManager)
package history

import "github.com/mfateev/codex-temporal-go/internal/models"

// ContextManager is the interface for managing conversation history.
//
// Corresponds to: codex-rs/core/src/state/session.rs ContextManager
//
// This interface supports multiple implementations:
// - InMemoryHistory: Simple in-memory storage (default)
// - ExternalHistory: External persistence (future)
type ContextManager interface {
	// Core operations

	// AddItem adds a new conversation item to history
	AddItem(item models.ConversationItem) error

	// GetForPrompt returns conversation items formatted for LLM prompt
	// Maps to: codex-rs clone_history().for_prompt()
	GetForPrompt() ([]models.ConversationItem, error)

	// EstimateTokenCount estimates the total token count of the history
	// Maps to: codex-rs clone_history().estimate_token_count()
	EstimateTokenCount() (int, error)

	// Admin operations

	// DropLastNUserTurns removes the last N user turns from history (for undo)
	// Maps to: codex-rs clone_history().drop_last_n_user_turns()
	DropLastNUserTurns(n int) error

	// GetRawItems returns raw conversation items for analysis
	// Maps to: codex-rs clone_history().raw_items()
	GetRawItems() ([]models.ConversationItem, error)

	// Query operations

	// GetTurnCount returns the number of user turns
	GetTurnCount() (int, error)
}
