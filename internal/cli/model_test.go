package cli

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"

	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/workflow"
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

func TestModel_InitialState(t *testing.T) {
	config := Config{Model: "gpt-4o-mini", NoColor: true, NoMarkdown: true}
	m := NewModel(config, nil)
	assert.Equal(t, StateStartup, m.state)
	assert.Equal(t, -1, m.lastRenderedSeq)
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
	assert.Contains(t, rm.viewportContent, "Session: codex-abc123")
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
	um := updated.(Model)
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
				Phase:       workflow.PhaseLLMCalling,
				TotalTokens: 500,
				TurnCount:   1,
			},
		},
	}

	result, _ := m.handlePollResult(msg)
	rm := result.(*Model)
	assert.Equal(t, 500, rm.totalTokens)
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
	um := updated.(Model)
	assert.True(t, um.quitting)
	assert.Contains(t, um.viewportContent, "Session ended")
}

func TestModel_UserInputSentTransitionsToWatching(t *testing.T) {
	m := newTestModel()
	m.state = StateInput

	updated, _ := m.Update(UserInputSentMsg{TurnID: "t1"})
	um := updated.(Model)
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
	assert.Contains(t, m.viewportContent, "t1")
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
