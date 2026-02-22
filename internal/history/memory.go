package history

import (
	"fmt"
	"sync"

	"github.com/mfateev/temporal-agent-harness/internal/models"
)

// InMemoryHistory is a simple in-memory implementation of ContextManager.
//
// Maps to: codex-rs/core/src/state/session.rs SessionState history field
type InMemoryHistory struct {
	items []models.ConversationItem
	mu    sync.RWMutex
}

// NewInMemoryHistory creates a new in-memory history.
func NewInMemoryHistory() *InMemoryHistory {
	return &InMemoryHistory{
		items: make([]models.ConversationItem, 0),
	}
}

// AddItem adds a new conversation item to history.
// Assigns a monotonically increasing Seq number before appending.
func (h *InMemoryHistory) AddItem(item models.ConversationItem) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	item.Seq = len(h.items)
	h.items = append(h.items, item)
	return nil
}

// GetForPrompt returns conversation items formatted for LLM prompt.
func (h *InMemoryHistory) GetForPrompt() ([]models.ConversationItem, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]models.ConversationItem, len(h.items))
	copy(result, h.items)
	return result, nil
}

// EstimateTokenCount estimates the total token count using a simple heuristic.
// Uses 4 characters per token as a rough estimate.
func (h *InMemoryHistory) EstimateTokenCount() (int, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	totalChars := 0
	for _, item := range h.items {
		totalChars += len(item.Content)
		totalChars += len(item.Name)
		totalChars += len(item.Arguments)
		if item.Output != nil {
			totalChars += len(item.Output.Content)
		}
	}

	return totalChars / 4, nil
}

// DropLastNUserTurns removes the last N user turns from history.
func (h *InMemoryHistory) DropLastNUserTurns(n int) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if n <= 0 {
		return nil
	}

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

	h.items = h.items[:cutIndex]
	return nil
}

// DropOldestUserTurns keeps only the last keepN user turns and their
// associated items. Everything before the Nth-from-last user message is removed.
// Returns the number of items dropped.
func (h *InMemoryHistory) DropOldestUserTurns(keepN int) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if keepN <= 0 {
		return 0, nil
	}

	// Count backwards to find the start of the Nth-from-last user message
	userCount := 0
	cutIndex := 0
	for i := len(h.items) - 1; i >= 0; i-- {
		if h.items[i].Type == models.ItemTypeUserMessage {
			userCount++
			if userCount == keepN {
				cutIndex = i
				// Include the TurnStarted marker that precedes this user message
				if cutIndex > 0 && h.items[cutIndex-1].Type == models.ItemTypeTurnStarted {
					cutIndex = cutIndex - 1
				}
				break
			}
		}
	}

	if cutIndex == 0 {
		return 0, nil // nothing to drop
	}

	dropped := cutIndex
	h.items = h.items[cutIndex:]
	// Re-assign Seq numbers
	for i := range h.items {
		h.items[i].Seq = i
	}
	return dropped, nil
}

// ReplaceAll replaces all history items with the given items.
// Re-assigns Seq numbers starting from 0.
func (h *InMemoryHistory) ReplaceAll(items []models.ConversationItem) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.items = make([]models.ConversationItem, len(items))
	copy(h.items, items)
	for i := range h.items {
		h.items[i].Seq = i
	}
	return nil
}

// GetRawItems returns raw conversation items for analysis.
func (h *InMemoryHistory) GetRawItems() ([]models.ConversationItem, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]models.ConversationItem, len(h.items))
	copy(result, h.items)
	return result, nil
}

// GetItemsSince returns items with Seq > sinceSeq.
// Since Seq == array index (assigned in AddItem), this is simply items[sinceSeq+1:].
// If sinceSeq >= len(items), it means compaction has reset the sequence space,
// so we return all items with compacted=true.
func (h *InMemoryHistory) GetItemsSince(sinceSeq int) ([]models.ConversationItem, bool, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if sinceSeq >= len(h.items) {
		// sinceSeq is beyond our current range — compaction must have occurred.
		// Return all items so the caller can re-sync.
		result := make([]models.ConversationItem, len(h.items))
		copy(result, h.items)
		return result, true, nil
	}

	startIdx := sinceSeq + 1
	if startIdx < 0 {
		startIdx = 0
	}

	result := make([]models.ConversationItem, len(h.items)-startIdx)
	copy(result, h.items[startIdx:])
	return result, false, nil
}

// GetLatestSeq returns the Seq of the most recent item, or -1 if empty.
func (h *InMemoryHistory) GetLatestSeq() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.items) == 0 {
		return -1
	}
	return len(h.items) - 1
}

// GetTurnCount returns the number of user turns.
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
