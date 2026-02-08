package history

import (
	"fmt"
	"sync"

	"github.com/mfateev/codex-temporal-go/internal/models"
)

// InMemoryHistory is a simple in-memory implementation of ConversationHistory
//
// Maps to: codex-rs/core/src/state/session.rs SessionState history field
type InMemoryHistory struct {
	items []models.ConversationItem
	mu    sync.RWMutex
}

// NewInMemoryHistory creates a new in-memory history
func NewInMemoryHistory() *InMemoryHistory {
	return &InMemoryHistory{
		items: make([]models.ConversationItem, 0),
	}
}

// AddItem adds a new conversation item to history
func (h *InMemoryHistory) AddItem(item models.ConversationItem) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.items = append(h.items, item)
	return nil
}

// GetForPrompt returns conversation items formatted for LLM prompt
func (h *InMemoryHistory) GetForPrompt() ([]models.ConversationItem, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Return a copy to avoid concurrent modification
	result := make([]models.ConversationItem, len(h.items))
	copy(result, h.items)
	return result, nil
}

// EstimateTokenCount estimates the total token count using a simple heuristic
//
// Uses 4 characters per token as a rough estimate (standard GPT heuristic)
func (h *InMemoryHistory) EstimateTokenCount() (int, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	totalChars := 0
	for _, item := range h.items {
		// Count content
		totalChars += len(item.Content)
		totalChars += len(item.ToolOutput)
		totalChars += len(item.ToolError)

		// Count tool calls
		for _, tc := range item.ToolCalls {
			totalChars += len(tc.Name)
			// Rough estimate for arguments JSON
			totalChars += len(fmt.Sprintf("%v", tc.Arguments))
		}
	}

	// 4 characters per token (rough estimate)
	return totalChars / 4, nil
}

// DropLastNUserTurns removes the last N user turns from history
//
// A "turn" consists of:
// 1. User message
// 2. Assistant message (with optional tool calls)
// 3. Tool results (if any)
func (h *InMemoryHistory) DropLastNUserTurns(n int) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if n <= 0 {
		return nil
	}

	// Count user turns from the end
	userTurnsFound := 0
	cutIndex := len(h.items)

	for i := len(h.items) - 1; i >= 0; i-- {
		if h.items[i].Type == models.ItemTypeUserMessage {
			userTurnsFound++
			if userTurnsFound == n {
				cutIndex = i
				break
			}
		}
	}

	if userTurnsFound < n {
		return fmt.Errorf("only %d user turns found, cannot drop %d", userTurnsFound, n)
	}

	// Remove items from cutIndex onwards
	h.items = h.items[:cutIndex]
	return nil
}

// GetRawItems returns raw conversation items for analysis
func (h *InMemoryHistory) GetRawItems() ([]models.ConversationItem, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Return a copy to avoid concurrent modification
	result := make([]models.ConversationItem, len(h.items))
	copy(result, h.items)
	return result, nil
}

// GetTurnCount returns the number of user turns
func (h *InMemoryHistory) GetTurnCount() (int, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	count := 0
	for _, item := range h.items {
		if item.Type == models.ItemTypeUserMessage {
			count++
		}
	}
	return count, nil
}
