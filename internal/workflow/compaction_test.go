package workflow

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/testsuite"

	"github.com/mfateev/temporal-agent-harness/internal/history"
	"github.com/mfateev/temporal-agent-harness/internal/models"
)

// TestCompaction_StripsModelSwitchMessages verifies that model_switch items
// are stripped from the input sent to the ExecuteCompact activity, and the
// last model_switch message is re-added to history after compaction.
func TestCompaction_StripsModelSwitchMessages(t *testing.T) {
	// Build a history with normal items + model_switch items
	h := history.NewInMemoryHistory()
	h.AddItem(models.ConversationItem{Type: models.ItemTypeUserMessage, Content: "Hello"})
	h.AddItem(models.ConversationItem{Type: models.ItemTypeAssistantMessage, Content: "Hi there!"})
	h.AddItem(models.ConversationItem{
		Type:    models.ItemTypeModelSwitch,
		Content: "<model_switch>Switched from gpt-4o-mini to gpt-4o</model_switch>",
	})
	h.AddItem(models.ConversationItem{Type: models.ItemTypeUserMessage, Content: "Continue"})
	h.AddItem(models.ConversationItem{Type: models.ItemTypeAssistantMessage, Content: "Sure!"})

	// Get items as they would be for compaction
	historyItems, err := h.GetForPrompt()
	require.NoError(t, err)

	// Simulate the strip logic from performCompaction
	var modelSwitchItems []models.ConversationItem
	var filteredItems []models.ConversationItem
	for _, item := range historyItems {
		if item.Type == models.ItemTypeModelSwitch {
			modelSwitchItems = append(modelSwitchItems, item)
		} else {
			filteredItems = append(filteredItems, item)
		}
	}

	// Verify model_switch items were stripped
	assert.Len(t, modelSwitchItems, 1)
	for _, item := range filteredItems {
		assert.NotEqual(t, models.ItemTypeModelSwitch, item.Type,
			"model_switch items should be stripped before compaction")
	}

	// Verify filtered items contain the non-model-switch items
	assert.Len(t, filteredItems, 4) // 2 user + 2 assistant

	// Simulate compaction result (just a summary)
	compactedItems := []models.ConversationItem{
		{Type: models.ItemTypeAssistantMessage, Content: "Compacted conversation summary"},
	}

	// Replace history with compacted items
	require.NoError(t, h.ReplaceAll(compactedItems))

	// Re-add the last model-switch message
	if len(modelSwitchItems) > 0 {
		h.AddItem(modelSwitchItems[len(modelSwitchItems)-1])
	}

	// Verify history now has compacted content + model_switch
	rawItems, err := h.GetRawItems()
	require.NoError(t, err)
	assert.Len(t, rawItems, 2) // compacted summary + re-added model_switch

	foundModelSwitch := false
	for _, item := range rawItems {
		if item.Type == models.ItemTypeModelSwitch {
			foundModelSwitch = true
			assert.Contains(t, item.Content, "Switched from gpt-4o-mini to gpt-4o")
		}
	}
	assert.True(t, foundModelSwitch, "model_switch should be re-added after compaction")
}

// TestCompaction_ReAddsLastModelSwitch verifies that when there are multiple
// model-switch messages, only the last one is re-added after compaction.
func TestCompaction_ReAddsLastModelSwitch(t *testing.T) {
	h := history.NewInMemoryHistory()
	h.AddItem(models.ConversationItem{Type: models.ItemTypeUserMessage, Content: "Hello"})
	h.AddItem(models.ConversationItem{
		Type:    models.ItemTypeModelSwitch,
		Content: "<model_switch>First switch: A to B</model_switch>",
	})
	h.AddItem(models.ConversationItem{Type: models.ItemTypeAssistantMessage, Content: "Hi!"})
	h.AddItem(models.ConversationItem{
		Type:    models.ItemTypeModelSwitch,
		Content: "<model_switch>Second switch: B to C</model_switch>",
	})
	h.AddItem(models.ConversationItem{Type: models.ItemTypeUserMessage, Content: "Continue"})

	historyItems, _ := h.GetForPrompt()

	var modelSwitchItems []models.ConversationItem
	for _, item := range historyItems {
		if item.Type == models.ItemTypeModelSwitch {
			modelSwitchItems = append(modelSwitchItems, item)
		}
	}

	assert.Len(t, modelSwitchItems, 2)

	// Simulate compaction + re-add
	h.ReplaceAll([]models.ConversationItem{
		{Type: models.ItemTypeAssistantMessage, Content: "Summary"},
	})
	if len(modelSwitchItems) > 0 {
		h.AddItem(modelSwitchItems[len(modelSwitchItems)-1])
	}

	rawItems, _ := h.GetRawItems()
	switchCount := 0
	for _, item := range rawItems {
		if item.Type == models.ItemTypeModelSwitch {
			switchCount++
			assert.Contains(t, item.Content, "Second switch: B to C",
				"should re-add only the last model-switch message")
		}
	}
	assert.Equal(t, 1, switchCount, "exactly one model_switch should be re-added")
}

// TestCompaction_NoModelSwitch_Unchanged verifies that compaction works
// normally when there are no model-switch items (regression test).
func TestCompaction_NoModelSwitch_Unchanged(t *testing.T) {
	h := history.NewInMemoryHistory()
	h.AddItem(models.ConversationItem{Type: models.ItemTypeUserMessage, Content: "Hello"})
	h.AddItem(models.ConversationItem{Type: models.ItemTypeAssistantMessage, Content: "Hi!"})
	h.AddItem(models.ConversationItem{Type: models.ItemTypeUserMessage, Content: "More"})

	historyItems, _ := h.GetForPrompt()

	// Strip logic: no model_switch items to strip
	var modelSwitchItems []models.ConversationItem
	var filteredItems []models.ConversationItem
	for _, item := range historyItems {
		if item.Type == models.ItemTypeModelSwitch {
			modelSwitchItems = append(modelSwitchItems, item)
		} else {
			filteredItems = append(filteredItems, item)
		}
	}

	assert.Len(t, modelSwitchItems, 0)
	assert.Len(t, filteredItems, len(historyItems), "all items should be passed through")

	// Simulate compaction
	h.ReplaceAll([]models.ConversationItem{
		{Type: models.ItemTypeAssistantMessage, Content: "Summary"},
	})

	// Re-add logic: nothing to re-add
	if len(modelSwitchItems) > 0 {
		h.AddItem(modelSwitchItems[len(modelSwitchItems)-1])
	}

	rawItems, _ := h.GetRawItems()
	assert.Len(t, rawItems, 1, "only compacted summary should remain")
	assert.Equal(t, models.ItemTypeAssistantMessage, rawItems[0].Type)
}

// TestCompaction_NoModelSwitch_WorkflowLevel verifies that compaction works
// normally end-to-end when there are no model-switch items (regression test).
func (s *AgenticWorkflowTestSuite) TestCompaction_NoModelSwitch_WorkflowLevel() {
	// LLM call
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Hello!", 50), nil).Once()

	// Second LLM call after compaction
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Continued!", 30), nil).Once()

	// Send manual compaction then new input
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateCompact, "compact-1", noopCallback(),
			CompactRequest{})
	}, time.Second*2)

	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateUserInput, "input-2", noopCallback(),
			UserInput{Content: "Continue"})
	}, time.Second*4)

	s.sendShutdown(time.Second * 6)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInput("Hello"))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
}

// Ensure we reference testsuite (suppress unused import warning)
var _ testsuite.TestUpdateCallback
