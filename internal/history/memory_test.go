package history

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mfateev/codex-temporal-go/internal/models"
)

// buildHistory creates a history with the given number of user turns.
// Each turn consists of: TurnStarted, UserMessage, AssistantMessage, TurnComplete.
func buildHistory(turns int) *InMemoryHistory {
	h := NewInMemoryHistory()
	for i := 0; i < turns; i++ {
		h.AddItem(models.ConversationItem{Type: models.ItemTypeTurnStarted, TurnID: "turn"})
		h.AddItem(models.ConversationItem{Type: models.ItemTypeUserMessage, Content: "msg"})
		h.AddItem(models.ConversationItem{Type: models.ItemTypeAssistantMessage, Content: "reply"})
		h.AddItem(models.ConversationItem{Type: models.ItemTypeTurnComplete, TurnID: "turn"})
	}
	return h
}

func TestDropOldestUserTurns_KeepHalf(t *testing.T) {
	h := buildHistory(4) // 16 items total
	dropped, err := h.DropOldestUserTurns(2)
	require.NoError(t, err)
	assert.Equal(t, 8, dropped) // dropped first 2 turns (8 items)

	items, _ := h.GetRawItems()
	assert.Len(t, items, 8) // 2 turns remaining

	// Verify Seq renumbering
	for i, item := range items {
		assert.Equal(t, i, item.Seq, "item %d should have Seq=%d", i, i)
	}

	// Verify first remaining item is TurnStarted
	assert.Equal(t, models.ItemTypeTurnStarted, items[0].Type)
}

func TestDropOldestUserTurns_KeepAll(t *testing.T) {
	h := buildHistory(3)
	dropped, err := h.DropOldestUserTurns(3)
	require.NoError(t, err)
	assert.Equal(t, 0, dropped) // nothing to drop, keeping all 3

	items, _ := h.GetRawItems()
	assert.Len(t, items, 12)
}

func TestDropOldestUserTurns_KeepMoreThanExists(t *testing.T) {
	h := buildHistory(2)
	dropped, err := h.DropOldestUserTurns(5)
	require.NoError(t, err)
	assert.Equal(t, 0, dropped) // can't find 5th turn from end, nothing dropped

	items, _ := h.GetRawItems()
	assert.Len(t, items, 8)
}

func TestDropOldestUserTurns_KeepOne(t *testing.T) {
	h := buildHistory(3) // 12 items
	dropped, err := h.DropOldestUserTurns(1)
	require.NoError(t, err)
	assert.Equal(t, 8, dropped)

	items, _ := h.GetRawItems()
	assert.Len(t, items, 4) // 1 turn remaining
}

func TestDropOldestUserTurns_ZeroKeep(t *testing.T) {
	h := buildHistory(2)
	dropped, err := h.DropOldestUserTurns(0)
	require.NoError(t, err)
	assert.Equal(t, 0, dropped)
}

func TestDropOldestUserTurns_EmptyHistory(t *testing.T) {
	h := NewInMemoryHistory()
	dropped, err := h.DropOldestUserTurns(2)
	require.NoError(t, err)
	assert.Equal(t, 0, dropped)
}

func TestGetTurnCount(t *testing.T) {
	h := buildHistory(3)
	count, err := h.GetTurnCount()
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

func TestDropOldestUserTurns_PreservesContent(t *testing.T) {
	h := NewInMemoryHistory()
	// Turn 1
	h.AddItem(models.ConversationItem{Type: models.ItemTypeTurnStarted, TurnID: "t1"})
	h.AddItem(models.ConversationItem{Type: models.ItemTypeUserMessage, Content: "first"})
	h.AddItem(models.ConversationItem{Type: models.ItemTypeAssistantMessage, Content: "reply1"})
	h.AddItem(models.ConversationItem{Type: models.ItemTypeTurnComplete, TurnID: "t1"})
	// Turn 2
	h.AddItem(models.ConversationItem{Type: models.ItemTypeTurnStarted, TurnID: "t2"})
	h.AddItem(models.ConversationItem{Type: models.ItemTypeUserMessage, Content: "second"})
	h.AddItem(models.ConversationItem{Type: models.ItemTypeAssistantMessage, Content: "reply2"})
	h.AddItem(models.ConversationItem{Type: models.ItemTypeTurnComplete, TurnID: "t2"})

	dropped, err := h.DropOldestUserTurns(1)
	require.NoError(t, err)
	assert.Equal(t, 4, dropped)

	items, _ := h.GetRawItems()
	assert.Len(t, items, 4)
	assert.Equal(t, "second", items[1].Content) // user message from turn 2
	assert.Equal(t, "reply2", items[2].Content)
}
