package history

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mfateev/temporal-agent-harness/internal/models"
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

// --- ReplaceAll tests ---

func TestReplaceAll_ReplacesItems(t *testing.T) {
	h := buildHistory(3) // 12 items

	newItems := []models.ConversationItem{
		{Type: models.ItemTypeCompaction, Content: "compacted"},
		{Type: models.ItemTypeAssistantMessage, Content: "summary"},
		{Type: models.ItemTypeUserMessage, Content: "recent msg"},
	}

	err := h.ReplaceAll(newItems)
	require.NoError(t, err)

	items, _ := h.GetRawItems()
	assert.Len(t, items, 3)
	assert.Equal(t, models.ItemTypeCompaction, items[0].Type)
	assert.Equal(t, "compacted", items[0].Content)
	assert.Equal(t, "summary", items[1].Content)
	assert.Equal(t, "recent msg", items[2].Content)
}

func TestReplaceAll_ReassignsSeq(t *testing.T) {
	h := buildHistory(2) // 8 items

	newItems := []models.ConversationItem{
		{Type: models.ItemTypeCompaction, Content: "c", Seq: 99},
		{Type: models.ItemTypeAssistantMessage, Content: "s", Seq: 100},
	}

	err := h.ReplaceAll(newItems)
	require.NoError(t, err)

	items, _ := h.GetRawItems()
	assert.Len(t, items, 2)
	assert.Equal(t, 0, items[0].Seq, "first item should have Seq=0")
	assert.Equal(t, 1, items[1].Seq, "second item should have Seq=1")
}

func TestReplaceAll_EmptyInput(t *testing.T) {
	h := buildHistory(2)

	err := h.ReplaceAll(nil)
	require.NoError(t, err)

	items, _ := h.GetRawItems()
	assert.Len(t, items, 0)
}

func TestReplaceAll_DoesNotMutateInput(t *testing.T) {
	h := NewInMemoryHistory()

	input := []models.ConversationItem{
		{Type: models.ItemTypeUserMessage, Content: "msg", Seq: 42},
	}

	err := h.ReplaceAll(input)
	require.NoError(t, err)

	// Original input should not be modified
	assert.Equal(t, 42, input[0].Seq, "input should not be mutated")

	// History should have re-assigned Seq
	items, _ := h.GetRawItems()
	assert.Equal(t, 0, items[0].Seq)
}

// --- GetItemsSince tests ---

func TestGetItemsSince_ReturnsNewItems(t *testing.T) {
	h := buildHistory(2) // 8 items, Seq 0-7

	items, compacted, err := h.GetItemsSince(3) // items after Seq 3
	require.NoError(t, err)
	assert.False(t, compacted)
	assert.Len(t, items, 4) // Seq 4,5,6,7
	assert.Equal(t, 4, items[0].Seq)
	assert.Equal(t, 7, items[3].Seq)
}

func TestGetItemsSince_NegativeOne_ReturnsAll(t *testing.T) {
	h := buildHistory(2) // 8 items

	items, compacted, err := h.GetItemsSince(-1) // everything
	require.NoError(t, err)
	assert.False(t, compacted)
	assert.Len(t, items, 8)
	assert.Equal(t, 0, items[0].Seq)
}

func TestGetItemsSince_AtLastSeq_ReturnsEmpty(t *testing.T) {
	h := buildHistory(2) // 8 items, last Seq=7

	items, compacted, err := h.GetItemsSince(7) // caught up
	require.NoError(t, err)
	assert.False(t, compacted)
	assert.Len(t, items, 0)
}

func TestGetItemsSince_StaleAfterCompaction(t *testing.T) {
	h := buildHistory(3) // 12 items, Seq 0-11

	// Compact to 2 items
	err := h.ReplaceAll([]models.ConversationItem{
		{Type: models.ItemTypeCompaction, Content: "summary"},
		{Type: models.ItemTypeUserMessage, Content: "recent"},
	})
	require.NoError(t, err)

	// sinceSeq=10 is now stale (only 2 items, Seq 0-1)
	items, compacted, err := h.GetItemsSince(10)
	require.NoError(t, err)
	assert.True(t, compacted, "should detect compaction")
	assert.Len(t, items, 2, "should return all items")
	assert.Equal(t, 0, items[0].Seq)
}

func TestGetItemsSince_EmptyHistory(t *testing.T) {
	h := NewInMemoryHistory()

	items, compacted, err := h.GetItemsSince(-1)
	require.NoError(t, err)
	assert.False(t, compacted)
	assert.Len(t, items, 0)
}

// --- GetLatestSeq tests ---

func TestGetLatestSeq_Empty(t *testing.T) {
	h := NewInMemoryHistory()
	assert.Equal(t, -1, h.GetLatestSeq())
}

func TestGetLatestSeq_WithItems(t *testing.T) {
	h := buildHistory(2) // 8 items
	assert.Equal(t, 7, h.GetLatestSeq())
}

func TestGetLatestSeq_AfterReplaceAll(t *testing.T) {
	h := buildHistory(3)
	h.ReplaceAll([]models.ConversationItem{
		{Type: models.ItemTypeCompaction, Content: "c"},
	})
	assert.Equal(t, 0, h.GetLatestSeq())
}
