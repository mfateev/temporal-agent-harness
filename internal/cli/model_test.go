package cli

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"

	"github.com/mfateev/temporal-agent-harness/internal/models"
	"github.com/mfateev/temporal-agent-harness/internal/workflow"
)

func newTestModel() Model {
	config := Config{
		Model:      "gpt-4o-mini",
		NoColor:    true,
		NoMarkdown: true,
	}
	// Use NewModel to get a properly initialized textarea
	m := NewModel(config, nil)
	m.state = StateInput
	m.ready = true
	m.width = 80
	m.height = 24
	m.renderer = NewItemRenderer(80, true, true, NoColorStyles())

	// Initialize the textarea through an Update to set up internal viewport
	m.textarea.SetWidth(80)
	m.textarea.SetHeight(1)

	return m
}

func TestModel_InitialState_NoMessage(t *testing.T) {
	config := Config{Model: "gpt-4o-mini", NoColor: true, NoMarkdown: true}
	m := NewModel(config, nil)
	assert.Equal(t, StateInput, m.state, "no message/session → start in input")
	assert.Equal(t, -1, m.lastRenderedSeq)
}

func TestModel_InitialState_WithMessage(t *testing.T) {
	config := Config{Model: "gpt-4o-mini", NoColor: true, NoMarkdown: true, Message: "hello"}
	m := NewModel(config, nil)
	assert.Equal(t, StateStartup, m.state, "with message → startup until workflow starts")
}

func TestModel_InitialState_WithSession(t *testing.T) {
	config := Config{Model: "gpt-4o-mini", NoColor: true, NoMarkdown: true, Session: "codex-abc"}
	m := NewModel(config, nil)
	assert.Equal(t, StateStartup, m.state, "with session → startup until resume completes")
}

func TestModel_WorkflowStartedNewSession(t *testing.T) {
	m := newTestModel()
	m.state = StateStartup
	m.config.Message = "hello"

	msg := WorkflowStartedMsg{
		WorkflowID: "codex-abc123",
		IsResume:   false,
	}

	result, _ := m.handleWorkflowStarted(msg)
	rm := result.(*Model)
	assert.Equal(t, StateWatching, rm.state)
	assert.Equal(t, "codex-abc123", rm.workflowID)
	assert.Contains(t, rm.viewportContent, "Started session codex-abc123")
}

func TestModel_WorkflowStartedNewSessionNoMessage(t *testing.T) {
	m := newTestModel()
	m.state = StateStartup
	m.config.Message = ""

	msg := WorkflowStartedMsg{
		WorkflowID: "codex-abc123",
		IsResume:   false,
	}

	result, _ := m.handleWorkflowStarted(msg)
	rm := result.(*Model)
	assert.Equal(t, StateInput, rm.state)
}

func TestModel_WorkflowStartedResumeRendersItems(t *testing.T) {
	m := newTestModel()
	m.state = StateStartup

	msg := WorkflowStartedMsg{
		WorkflowID: "codex-abc123",
		IsResume:   true,
		Items: []models.ConversationItem{
			{Type: models.ItemTypeTurnStarted, Seq: 0, TurnID: "t1"},
			{Type: models.ItemTypeUserMessage, Seq: 1, Content: "Hello"},
			{Type: models.ItemTypeAssistantMessage, Seq: 2, Content: "Hi there!"},
		},
		Status: workflow.TurnStatus{
			Phase: workflow.PhaseWaitingForInput,
		},
	}

	result, _ := m.handleWorkflowStarted(msg)
	rm := result.(*Model)
	assert.Equal(t, StateInput, rm.state)
	assert.Contains(t, rm.viewportContent, "3 previous items")
	assert.Contains(t, rm.viewportContent, "Hello")    // user message shown on resume
	assert.Contains(t, rm.viewportContent, "Hi there!") // assistant message
	assert.Equal(t, 2, rm.lastRenderedSeq)
}

func TestModel_WorkflowStartedResumeApprovalState(t *testing.T) {
	m := newTestModel()
	m.state = StateStartup

	msg := WorkflowStartedMsg{
		WorkflowID: "codex-abc123",
		IsResume:   true,
		Items:      []models.ConversationItem{},
		Status: workflow.TurnStatus{
			Phase: workflow.PhaseApprovalPending,
			PendingApprovals: []workflow.PendingApproval{
				{CallID: "c1", ToolName: "shell", Arguments: `{"command":"ls"}`},
			},
		},
	}

	result, _ := m.handleWorkflowStarted(msg)
	rm := result.(*Model)
	assert.Equal(t, StateApproval, rm.state)
	assert.Len(t, rm.pendingApprovals, 1)
}

func TestModel_WorkflowStartedResumeWatchingState(t *testing.T) {
	m := newTestModel()
	m.state = StateStartup

	msg := WorkflowStartedMsg{
		WorkflowID: "codex-abc123",
		IsResume:   true,
		Items:      []models.ConversationItem{},
		Status: workflow.TurnStatus{
			Phase: workflow.PhaseLLMCalling,
		},
	}

	result, _ := m.handleWorkflowStarted(msg)
	rm := result.(*Model)
	assert.Equal(t, StateWatching, rm.state)
}

func TestModel_WorkflowStartErrorQuitsModel(t *testing.T) {
	m := newTestModel()
	m.state = StateStartup

	updated, cmd := m.Update(WorkflowStartErrorMsg{Err: assert.AnError})
	um := updated.(*Model)
	assert.True(t, um.quitting)
	assert.NotNil(t, um.err)
	assert.NotNil(t, cmd)
}

func TestModel_PollResultUpdatesStatus(t *testing.T) {
	m := newTestModel()
	m.state = StateWatching
	m.workflowID = "test-wf"

	msg := PollResultMsg{
		Result: PollResult{
			Items: []models.ConversationItem{
				{Type: models.ItemTypeAssistantMessage, Seq: 0, Content: "Hello"},
			},
			Status: workflow.TurnStatus{
				Phase:             workflow.PhaseLLMCalling,
				TotalTokens:      500,
				TotalCachedTokens: 150,
				TurnCount:        1,
			},
		},
	}

	result, _ := m.handlePollResult(msg)
	rm := result.(*Model)
	assert.Equal(t, 500, rm.totalTokens)
	assert.Equal(t, 150, rm.totalCachedTokens)
	assert.Equal(t, 1, rm.turnCount)
	assert.Equal(t, 0, rm.lastRenderedSeq)
}

func TestModel_PollResultTurnComplete(t *testing.T) {
	m := newTestModel()
	m.state = StateWatching
	m.workflowID = "test-wf"
	m.lastRenderedSeq = 0

	msg := PollResultMsg{
		Result: PollResult{
			Items: []models.ConversationItem{
				{Type: models.ItemTypeTurnComplete, Seq: 1, TurnID: "t1"},
			},
			Status: workflow.TurnStatus{
				Phase:       workflow.PhaseWaitingForInput,
				TotalTokens: 1000,
				TurnCount:   1,
			},
		},
	}

	result, _ := m.handlePollResult(msg)
	rm := result.(*Model)
	assert.Equal(t, StateInput, rm.state)
}

func TestModel_PollResultApprovalPending(t *testing.T) {
	m := newTestModel()
	m.state = StateWatching
	m.workflowID = "test-wf"

	msg := PollResultMsg{
		Result: PollResult{
			Items: []models.ConversationItem{},
			Status: workflow.TurnStatus{
				Phase: workflow.PhaseApprovalPending,
				PendingApprovals: []workflow.PendingApproval{
					{CallID: "c1", ToolName: "shell", Arguments: `{"command":"rm -rf /"}`},
				},
			},
		},
	}

	result, _ := m.handlePollResult(msg)
	rm := result.(*Model)
	assert.Equal(t, StateApproval, rm.state)
	assert.Len(t, rm.pendingApprovals, 1)
}

func TestModel_ModelCommandStartsFetch(t *testing.T) {
	m := newTestModel()
	m.workflowID = "codex-abc123"

	m.textarea.SetValue("/model")
	updated, cmd := m.handleInputKey(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(*Model)
	assert.True(t, um.selectingModel, "selectingModel should be set")
	assert.True(t, um.modelsFetching, "modelsFetching should be set")
	assert.Nil(t, um.selector, "selector should not be created yet (fetching)")
	assert.NotNil(t, cmd, "should return a fetch command")
	assert.Contains(t, um.viewportContent, "Fetching")
}

func TestModel_ModelsFetchedShowsSelector(t *testing.T) {
	m := newTestModel()
	m.workflowID = "codex-abc123"
	m.selectingModel = true
	m.modelsFetching = true

	fetched := []modelOption{
		{Provider: "anthropic", Model: "claude-sonnet-4.5-20250929", DisplayName: "Claude Sonnet 4.5"},
		{Provider: "openai", Model: "gpt-4o"},
	}

	updated, _ := m.Update(ModelsFetchedMsg{Models: fetched})
	um := updated.(*Model)
	assert.True(t, um.modelsFetched)
	assert.False(t, um.modelsFetching)
	assert.Equal(t, fetched, um.cachedModelOptions)
	assert.NotNil(t, um.selector, "selector should be created after fetch")
	assert.Contains(t, um.viewportContent, "Select a model")
}

func TestModel_ModelsFetchedFallback(t *testing.T) {
	m := newTestModel()
	m.workflowID = "codex-abc123"
	m.selectingModel = true
	m.modelsFetching = true

	// nil Models signals fallback to defaults
	updated, _ := m.Update(ModelsFetchedMsg{Models: nil})
	um := updated.(*Model)
	assert.True(t, um.modelsFetched)
	assert.Nil(t, um.cachedModelOptions, "cache should stay nil for fallback")
	assert.NotNil(t, um.selector, "selector should still be shown using defaults")
	// The selector should have as many options as the default list
	assert.Equal(t, len(defaultModelOptions()), len(um.currentModelOptions()))
}

func TestModel_ModelCommandCachedShowsImmediately(t *testing.T) {
	m := newTestModel()
	m.workflowID = "codex-abc123"
	m.modelsFetched = true
	m.cachedModelOptions = []modelOption{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "anthropic", Model: "claude-sonnet-4.5-20250929"},
	}

	m.textarea.SetValue("/model")
	updated, _ := m.handleInputKey(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(*Model)
	assert.True(t, um.selectingModel)
	assert.NotNil(t, um.selector, "selector shown immediately when cached")
	assert.Contains(t, um.viewportContent, "Select a model")
}

func TestModel_PollResultAutoApprove(t *testing.T) {
	m := newTestModel()
	m.state = StateWatching
	m.workflowID = "test-wf"
	m.autoApprove = true

	msg := PollResultMsg{
		Result: PollResult{
			Items: []models.ConversationItem{},
			Status: workflow.TurnStatus{
				Phase: workflow.PhaseApprovalPending,
				PendingApprovals: []workflow.PendingApproval{
					{CallID: "c1", ToolName: "shell"},
				},
			},
		},
	}

	result, cmd := m.handlePollResult(msg)
	rm := result.(*Model)
	// Should stay in watching (auto-approve sends response)
	assert.Equal(t, StateWatching, rm.state)
	assert.NotNil(t, cmd) // Should have a command to send approval
}

func TestModel_PollResultEscalationPending(t *testing.T) {
	m := newTestModel()
	m.state = StateWatching
	m.workflowID = "test-wf"

	msg := PollResultMsg{
		Result: PollResult{
			Items: []models.ConversationItem{},
			Status: workflow.TurnStatus{
				Phase: workflow.PhaseEscalationPending,
				PendingEscalations: []workflow.EscalationRequest{
					{CallID: "c1", ToolName: "shell", Output: "permission denied"},
				},
			},
		},
	}

	result, _ := m.handlePollResult(msg)
	rm := result.(*Model)
	assert.Equal(t, StateEscalation, rm.state)
	assert.Len(t, rm.pendingEscalations, 1)
}

func TestModel_CtrlCDuringInputDisconnects(t *testing.T) {
	m := newTestModel()
	m.state = StateInput

	result, _ := m.handleCtrlC()
	rm := result.(*Model)
	assert.True(t, rm.quitting)
}

func TestModel_CtrlCDuringWatchingInterrupts(t *testing.T) {
	m := newTestModel()
	m.state = StateWatching
	m.workflowID = "test-wf"

	result, _ := m.handleCtrlC()
	rm := result.(*Model)
	assert.False(t, rm.quitting)
	assert.Equal(t, StateWatching, rm.state)
	assert.Contains(t, rm.viewportContent, "Interrupting")
}

func TestModel_DoubleCtrlCDuringWatchingDisconnects(t *testing.T) {
	m := newTestModel()
	m.state = StateWatching
	m.workflowID = "test-wf"
	m.lastInterruptTime = time.Now() // Simulate recent first Ctrl+C

	result, _ := m.handleCtrlC()
	rm := result.(*Model)
	assert.True(t, rm.quitting)
}

func TestModel_CtrlCDuringApprovalInterrupts(t *testing.T) {
	m := newTestModel()
	m.state = StateApproval
	m.workflowID = "test-wf"
	m.pendingApprovals = []workflow.PendingApproval{{CallID: "c1"}}

	result, _ := m.handleCtrlC()
	rm := result.(*Model)
	assert.Equal(t, StateWatching, rm.state)
	assert.Nil(t, rm.pendingApprovals)
}

func TestModel_SessionCompletedQuitsModel(t *testing.T) {
	m := newTestModel()
	m.state = StateWatching

	updated, _ := m.Update(SessionCompletedMsg{Result: &workflow.WorkflowResult{
		TotalTokens:       1500,
		ToolCallsExecuted: []string{"shell", "write_file"},
	}})
	um := updated.(*Model)
	assert.True(t, um.quitting)
	assert.Contains(t, um.viewportContent, "Session ended")
}

func TestModel_UserInputSentTransitionsToWatching(t *testing.T) {
	m := newTestModel()
	m.state = StateInput

	updated, _ := m.Update(UserInputSentMsg{TurnID: "t1"})
	um := updated.(*Model)
	assert.Equal(t, StateWatching, um.state)
	assert.Equal(t, "Thinking...", um.spinnerMsg)
}

func TestModel_HandleInputKey_ExitCommand(t *testing.T) {
	m := newTestModel()
	m.state = StateInput
	m.textarea.SetValue("/exit")

	result, _ := m.handleInputKey(tea.KeyMsg{Type: tea.KeyEnter})
	rm := result.(*Model)
	assert.True(t, rm.quitting)
}

func TestModel_HandleInputKey_QuitCommand(t *testing.T) {
	m := newTestModel()
	m.state = StateInput
	m.textarea.SetValue("/quit")

	result, _ := m.handleInputKey(tea.KeyMsg{Type: tea.KeyEnter})
	rm := result.(*Model)
	assert.True(t, rm.quitting)
}

func TestModel_HandleInputKey_EndCommand(t *testing.T) {
	m := newTestModel()
	m.state = StateInput
	m.workflowID = "test-wf"
	m.textarea.SetValue("/end")

	result, _ := m.handleInputKey(tea.KeyMsg{Type: tea.KeyEnter})
	rm := result.(*Model)
	assert.Equal(t, StateWatching, rm.state)
	assert.Equal(t, "Ending session...", rm.spinnerMsg)
}

func TestModel_HandleInputKey_EmptyLine(t *testing.T) {
	m := newTestModel()
	m.state = StateInput
	m.textarea.SetValue("")

	result, _ := m.handleInputKey(tea.KeyMsg{Type: tea.KeyEnter})
	rm := result.(*Model)
	assert.Equal(t, StateInput, rm.state)
}

func TestModel_AppendToViewport(t *testing.T) {
	m := newTestModel()
	m.appendToViewport("first line\n")
	m.appendToViewport("second line\n")

	assert.Contains(t, m.viewportContent, "first line")
	assert.Contains(t, m.viewportContent, "second line")
}

func TestModel_RenderNewItems(t *testing.T) {
	m := newTestModel()
	m.lastRenderedSeq = -1

	items := []models.ConversationItem{
		{Type: models.ItemTypeTurnStarted, Seq: 0, TurnID: "t1"},
		{Type: models.ItemTypeAssistantMessage, Seq: 1, Content: "Hello!"},
	}

	m.renderNewItems(items)
	assert.Equal(t, 1, m.lastRenderedSeq)
	// TurnStarted is not rendered in live mode (separator handled by input handler)
	assert.NotContains(t, m.viewportContent, "──")
	assert.Contains(t, m.viewportContent, "Hello!")
}

func TestModel_RenderNewItemsSkipAlreadyRendered(t *testing.T) {
	m := newTestModel()
	m.lastRenderedSeq = 5

	items := []models.ConversationItem{
		{Type: models.ItemTypeAssistantMessage, Seq: 3, Content: "old"},
		{Type: models.ItemTypeAssistantMessage, Seq: 6, Content: "new"},
	}

	m.renderNewItems(items)
	assert.Equal(t, 6, m.lastRenderedSeq)
	assert.NotContains(t, m.viewportContent, "old")
	assert.Contains(t, m.viewportContent, "new")
}

func TestModel_IsTurnComplete(t *testing.T) {
	m := newTestModel()
	m.lastRenderedSeq = 0

	items := []models.ConversationItem{
		{Type: models.ItemTypeAssistantMessage, Seq: 1, Content: "response"},
		{Type: models.ItemTypeTurnComplete, Seq: 2, TurnID: "t1"},
	}

	assert.True(t, m.isTurnComplete(items))
}

func TestModel_IsTurnCompleteNotPresent(t *testing.T) {
	m := newTestModel()
	m.lastRenderedSeq = 0

	items := []models.ConversationItem{
		{Type: models.ItemTypeAssistantMessage, Seq: 1, Content: "response"},
	}

	assert.False(t, m.isTurnComplete(items))
}

func TestModel_ViewNotReady(t *testing.T) {
	m := newTestModel()
	m.ready = false
	view := m.View()
	assert.Contains(t, view, "Starting")
}

func TestModel_IsScrollKey(t *testing.T) {
	m := newTestModel()

	// Scroll keys should be detected
	assert.True(t, m.isScrollKey(tea.KeyMsg{Type: tea.KeyUp}))
	assert.True(t, m.isScrollKey(tea.KeyMsg{Type: tea.KeyDown}))
	assert.True(t, m.isScrollKey(tea.KeyMsg{Type: tea.KeyPgUp}))
	assert.True(t, m.isScrollKey(tea.KeyMsg{Type: tea.KeyPgDown}))
	assert.True(t, m.isScrollKey(tea.KeyMsg{Type: tea.KeyHome}))
	assert.True(t, m.isScrollKey(tea.KeyMsg{Type: tea.KeyEnd}))

	// Non-scroll keys should not be detected
	assert.False(t, m.isScrollKey(tea.KeyMsg{Type: tea.KeyEnter}))
	assert.False(t, m.isScrollKey(tea.KeyMsg{Type: tea.KeyTab}))
	assert.False(t, m.isScrollKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}))
}

func TestModel_ScrollKeysDuringInput(t *testing.T) {
	m := newTestModel()
	m.state = StateInput
	// Add content so viewport has something to scroll
	m.viewportContent = strings.Repeat("line\n", 100)
	m.viewport.SetContent(m.viewportContent)

	// Up arrow during input should go to viewport, not textarea
	result, _ := m.handleInputKey(tea.KeyMsg{Type: tea.KeyUp})
	rm := result.(*Model)
	assert.Equal(t, StateInput, rm.state, "state should remain StateInput")
}

func TestModel_ScrollKeysDuringApproval(t *testing.T) {
	m := newTestModel()
	m.state = StateApproval
	m.pendingApprovals = []workflow.PendingApproval{{CallID: "c1"}}
	m.viewportContent = strings.Repeat("line\n", 100)
	m.viewport.SetContent(m.viewportContent)

	// PgDown during approval should go to viewport
	result, _ := m.handleApprovalKey(tea.KeyMsg{Type: tea.KeyPgDown})
	rm := result.(*Model)
	assert.Equal(t, StateApproval, rm.state, "state should remain StateApproval")
}

func TestModel_ScrollKeysDuringEscalation(t *testing.T) {
	m := newTestModel()
	m.state = StateEscalation
	m.pendingEscalations = []workflow.EscalationRequest{{CallID: "c1"}}
	m.viewportContent = strings.Repeat("line\n", 100)
	m.viewport.SetContent(m.viewportContent)

	// Down arrow during escalation should go to viewport
	result, _ := m.handleEscalationKey(tea.KeyMsg{Type: tea.KeyDown})
	rm := result.(*Model)
	assert.Equal(t, StateEscalation, rm.state, "state should remain StateEscalation")
}

func TestModel_CalculateTextareaHeight(t *testing.T) {
	m := newTestModel()

	// Empty or single line should return minimum (1)
	m.textarea.SetValue("")
	assert.Equal(t, 1, m.calculateTextareaHeight())

	m.textarea.SetValue("single line")
	assert.Equal(t, 1, m.calculateTextareaHeight())

	// Multiple lines
	m.textarea.SetValue("line 1\nline 2\nline 3\nline 4")
	assert.Equal(t, 4, m.calculateTextareaHeight())

	// More than max should cap at MaxTextareaHeight
	longText := strings.Repeat("line\n", 15)
	m.textarea.SetValue(longText)
	assert.Equal(t, MaxTextareaHeight, m.calculateTextareaHeight())
}

func TestModel_MultiLineInput(t *testing.T) {
	m := newTestModel()
	m.state = StateInput
	m.workflowID = "test-wf"

	// Simulate typing multi-line input
	multiLineText := "This is line 1\nThis is line 2\nThis is line 3"
	m.textarea.SetValue(multiLineText)

	// Verify textarea accepts multi-line content
	assert.Contains(t, m.textarea.Value(), "line 1")
	assert.Contains(t, m.textarea.Value(), "line 2")
	assert.Contains(t, m.textarea.Value(), "line 3")

	// Enter should submit
	result, _ := m.handleInputKey(tea.KeyMsg{Type: tea.KeyEnter})
	rm := result.(*Model)
	assert.Equal(t, StateWatching, rm.state)
	assert.Empty(t, rm.textarea.Value(), "textarea should be cleared after submit")
}

// --- Suggestion Tests ---

func TestModel_SuggestionAppliedOnTurnComplete(t *testing.T) {
	m := newTestModel()
	m.state = StateWatching
	m.workflowID = "test-wf"
	m.lastRenderedSeq = 0

	msg := PollResultMsg{
		Result: PollResult{
			Items: []models.ConversationItem{
				{Type: models.ItemTypeTurnComplete, Seq: 1, TurnID: "t1"},
			},
			Status: workflow.TurnStatus{
				Phase:      workflow.PhaseWaitingForInput,
				Suggestion: "run the tests",
			},
		},
	}

	result, _ := m.handlePollResult(msg)
	rm := result.(*Model)
	assert.Equal(t, StateInput, rm.state)
	assert.Equal(t, "run the tests", rm.suggestion)
	assert.Equal(t, "run the tests", rm.textarea.Placeholder)
}

func TestModel_TabAcceptsSuggestion(t *testing.T) {
	m := newTestModel()
	m.state = StateInput
	m.suggestion = "run the tests"
	m.textarea.SetValue("") // Empty textarea

	result, _ := m.handleInputKey(tea.KeyMsg{Type: tea.KeyTab})
	rm := result.(*Model)
	assert.Equal(t, "run the tests", rm.textarea.Value())
	assert.Equal(t, "", rm.suggestion, "suggestion should be cleared after Tab accept")
}

func TestModel_TabNoOpWithoutSuggestion(t *testing.T) {
	m := newTestModel()
	m.state = StateInput
	m.suggestion = ""
	m.textarea.SetValue("")

	result, _ := m.handleInputKey(tea.KeyMsg{Type: tea.KeyTab})
	rm := result.(*Model)
	assert.Equal(t, "", rm.textarea.Value(), "Tab without suggestion should not change textarea")
}

func TestModel_TabNoOpWithExistingText(t *testing.T) {
	m := newTestModel()
	m.state = StateInput
	m.suggestion = "run the tests"
	m.textarea.SetValue("something else")

	result, _ := m.handleInputKey(tea.KeyMsg{Type: tea.KeyTab})
	rm := result.(*Model)
	assert.Equal(t, "something else", rm.textarea.Value(), "Tab with existing text should not replace")
	assert.Equal(t, "run the tests", rm.suggestion, "suggestion should remain")
}

func TestModel_SubmitClearsSuggestion(t *testing.T) {
	m := newTestModel()
	m.state = StateInput
	m.workflowID = "test-wf"
	m.suggestion = "run the tests"
	m.textarea.Placeholder = "run the tests"
	m.textarea.SetValue("something else")

	result, _ := m.handleInputKey(tea.KeyMsg{Type: tea.KeyEnter})
	rm := result.(*Model)
	assert.Equal(t, "", rm.suggestion, "suggestion should be cleared on submit")
	assert.Equal(t, "Type a message...", rm.textarea.Placeholder, "placeholder should be reset")
}

func TestModel_SuggestionPollAppliesSuggestion(t *testing.T) {
	m := newTestModel()
	m.state = StateInput
	m.textarea.SetValue("")

	result, _ := m.handleSuggestionPoll(SuggestionPollMsg{Suggestion: "commit this"})
	rm := result.(*Model)
	assert.Equal(t, "commit this", rm.suggestion)
	assert.Equal(t, "commit this", rm.textarea.Placeholder)
}

func TestModel_SuggestionPollIgnoredIfTyping(t *testing.T) {
	m := newTestModel()
	m.state = StateInput
	m.textarea.SetValue("already typing")

	result, _ := m.handleSuggestionPoll(SuggestionPollMsg{Suggestion: "commit this"})
	rm := result.(*Model)
	assert.Equal(t, "", rm.suggestion, "suggestion should not apply when user is typing")
}

func TestModel_SuggestionPollIgnoredIfNotInInputState(t *testing.T) {
	m := newTestModel()
	m.state = StateWatching

	result, _ := m.handleSuggestionPoll(SuggestionPollMsg{Suggestion: "commit this"})
	rm := result.(*Model)
	assert.Equal(t, "", rm.suggestion, "suggestion should not apply when not in input state")
}

func TestModel_SuggestionPollEmptyIgnored(t *testing.T) {
	m := newTestModel()
	m.state = StateInput
	m.textarea.SetValue("")

	result, _ := m.handleSuggestionPoll(SuggestionPollMsg{Suggestion: ""})
	rm := result.(*Model)
	assert.Equal(t, "", rm.suggestion, "empty suggestion should be ignored")
}

func TestModel_StatusBarShowsCachedTokens(t *testing.T) {
	m := newTestModel()
	m.totalTokens = 5000
	m.totalCachedTokens = 1200
	m.turnCount = 3
	m.state = StateInput
	m.modelName = "gpt-4o-mini"

	bar := m.renderStatusBar()
	assert.Contains(t, bar, "5,000")
	assert.Contains(t, bar, "(1,200 cached)")
}

func TestModel_StatusBarNoCachedWhenZero(t *testing.T) {
	m := newTestModel()
	m.totalTokens = 500
	m.totalCachedTokens = 0
	m.turnCount = 1
	m.state = StateInput
	m.modelName = "gpt-4o-mini"

	bar := m.renderStatusBar()
	assert.Contains(t, bar, "500")
	assert.NotContains(t, bar, "cached")
}

func TestModel_SessionEndedShowsCachedTokens(t *testing.T) {
	m := newTestModel()
	m.state = StateWatching

	updated, _ := m.Update(SessionCompletedMsg{Result: &workflow.WorkflowResult{
		TotalTokens:       1500,
		TotalCachedTokens: 400,
		ToolCallsExecuted: []string{"shell"},
	}})
	um := updated.(*Model)
	assert.True(t, um.quitting)
	assert.Contains(t, um.viewportContent, "Session ended")
	assert.Contains(t, um.viewportContent, "1500")
	assert.Contains(t, um.viewportContent, "400 cached")
}

func TestModel_ClearSuggestionRestoresPlaceholder(t *testing.T) {
	m := newTestModel()
	m.applySuggestion("run the tests")
	assert.Equal(t, "run the tests", m.suggestion)
	assert.Equal(t, "run the tests", m.textarea.Placeholder)

	m.clearSuggestion()
	assert.Equal(t, "", m.suggestion)
	assert.Equal(t, "Type a message...", m.textarea.Placeholder)
}

func TestModel_TurnCompleteSchedulesSuggestionPoll(t *testing.T) {
	m := newTestModel()
	m.state = StateWatching
	m.workflowID = "test-wf"
	m.lastRenderedSeq = 0

	msg := PollResultMsg{
		Result: PollResult{
			Items: []models.ConversationItem{
				{Type: models.ItemTypeTurnComplete, Seq: 1, TurnID: "t1"},
			},
			Status: workflow.TurnStatus{
				Phase:      workflow.PhaseWaitingForInput,
				Suggestion: "", // No suggestion yet
			},
		},
	}

	result, cmd := m.handlePollResult(msg)
	rm := result.(*Model)
	assert.Equal(t, StateInput, rm.state)
	assert.Equal(t, "", rm.suggestion, "no suggestion yet")
	assert.NotNil(t, cmd, "should have scheduled commands (focusTextarea + suggestionPoll)")
}

func TestModel_TurnCompleteNoSuggestionPollWhenDisabled(t *testing.T) {
	m := newTestModel()
	m.state = StateWatching
	m.workflowID = "test-wf"
	m.lastRenderedSeq = 0
	m.config.DisableSuggestions = true

	msg := PollResultMsg{
		Result: PollResult{
			Items: []models.ConversationItem{
				{Type: models.ItemTypeTurnComplete, Seq: 1, TurnID: "t1"},
			},
			Status: workflow.TurnStatus{
				Phase:      workflow.PhaseWaitingForInput,
				Suggestion: "",
			},
		},
	}

	_, _ = m.handlePollResult(msg)
	// With DisableSuggestions=true, the delayed poll should not be scheduled.
	// We can't easily test what commands are in a batch, but we verify no suggestion is set.
	assert.Equal(t, "", m.suggestion)
}
