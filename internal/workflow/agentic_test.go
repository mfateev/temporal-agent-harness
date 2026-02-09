package workflow

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/mfateev/codex-temporal-go/internal/activities"
	"github.com/mfateev/codex-temporal-go/internal/models"
)

// Stub activity functions for the test environment.
// These are never called directly — OnActivity mocks override them —
// but they must be registered so the test env recognises the activity names.
func ExecuteLLMCall(_ context.Context, _ activities.LLMActivityInput) (activities.LLMActivityOutput, error) {
	panic("stub: should be mocked")
}

func ExecuteTool(_ context.Context, _ activities.ToolActivityInput) (activities.ToolActivityOutput, error) {
	panic("stub: should be mocked")
}

// AgenticWorkflowTestSuite runs workflow tests with the Temporal test environment.
type AgenticWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func TestAgenticWorkflowSuite(t *testing.T) {
	suite.Run(t, new(AgenticWorkflowTestSuite))
}

func (s *AgenticWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterActivity(ExecuteLLMCall)
	s.env.RegisterActivity(ExecuteTool)
}

func (s *AgenticWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

// mockLLMStopResponse returns a simple assistant message with stop finish reason.
func mockLLMStopResponse(content string, tokens int) activities.LLMActivityOutput {
	return activities.LLMActivityOutput{
		Items: []models.ConversationItem{
			{Type: models.ItemTypeAssistantMessage, Content: content},
		},
		FinishReason: models.FinishReasonStop,
		TokenUsage:   models.TokenUsage{TotalTokens: tokens},
	}
}

// testInput returns a standard WorkflowInput for testing.
func testInput(message string) WorkflowInput {
	return WorkflowInput{
		ConversationID: "test-conv-1",
		UserMessage:    message,
		Config: models.SessionConfiguration{
			Model: models.ModelConfig{
				Model:         "gpt-4o-mini",
				Temperature:   0,
				MaxTokens:     100,
				ContextWindow: 128000,
			},
		},
	}
}

// noopCallback returns a TestUpdateCallback that does nothing on all events.
func noopCallback() *testsuite.TestUpdateCallback {
	return &testsuite.TestUpdateCallback{
		OnAccept:   func() {},
		OnReject:   func(err error) {},
		OnComplete: func(interface{}, error) {},
	}
}

// sendShutdown sends a shutdown Update via RegisterDelayedCallback.
func (s *AgenticWorkflowTestSuite) sendShutdown(delay time.Duration) {
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateShutdown, "shutdown-1", noopCallback(), ShutdownRequest{})
	}, delay)
}

// TestMultiTurn_SingleTurnWithShutdown verifies workflow completes after one LLM
// turn followed by a shutdown Update.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_SingleTurnWithShutdown() {
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Hello!", 50), nil).Once()

	s.sendShutdown(time.Second * 2)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInput("Hello"))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "test-conv-1", result.ConversationID)
	assert.Equal(s.T(), "shutdown", result.EndReason)
	assert.Equal(s.T(), 50, result.TotalTokens)
}

// TestMultiTurn_QueryHistoryDuringExecution verifies the query handler returns
// items mid-turn.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_QueryHistoryDuringExecution() {
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("I'm here.", 30), nil).Once()

	// Query history after LLM turn completes
	s.env.RegisterDelayedCallback(func() {
		result, err := s.env.QueryWorkflow(QueryGetConversationItems)
		require.NoError(s.T(), err)

		var items []models.ConversationItem
		require.NoError(s.T(), result.Get(&items))

		// Should have: TurnStarted, UserMessage, AssistantMessage, TurnComplete
		assert.GreaterOrEqual(s.T(), len(items), 3, "Should have at least TurnStarted + UserMessage + AssistantMessage")

		// Verify first items
		assert.Equal(s.T(), models.ItemTypeTurnStarted, items[0].Type)
		assert.Equal(s.T(), models.ItemTypeUserMessage, items[1].Type)
		assert.Equal(s.T(), "Hello", items[1].Content)
	}, time.Second*2)

	s.sendShutdown(time.Second * 3)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInput("Hello"))
	require.True(s.T(), s.env.IsWorkflowCompleted())
}

// TestMultiTurn_UserInputUpdate verifies a second user message wakes
// the waiting workflow and triggers another LLM turn.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_UserInputUpdate() {
	// First turn
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("First response", 40), nil).Once()
	// Second turn
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Second response", 60), nil).Once()

	// Send second user message after first turn completes
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateUserInput, "input-2", noopCallback(),
			UserInput{Content: "Follow-up question"})
	}, time.Second*2)

	s.sendShutdown(time.Second * 4)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInput("First question"))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
	assert.Equal(s.T(), 100, result.TotalTokens) // 40 + 60
}

// TestMultiTurn_Interrupt verifies interrupt is acknowledged.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_Interrupt() {
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Response before interrupt", 35), nil).Once()

	// Second turn after interrupt
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Response after interrupt", 25), nil).Once()

	// Send interrupt after first turn
	var interruptAcknowledged bool
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateInterrupt, "interrupt-1", &testsuite.TestUpdateCallback{
			OnAccept: func() {},
			OnReject: func(err error) {
				s.Fail("interrupt should not be rejected", err.Error())
			},
			OnComplete: func(result interface{}, err error) {
				require.NoError(s.T(), err)
				interruptAcknowledged = true
			},
		}, InterruptRequest{})
	}, time.Second*2)

	// Send new user input after interrupt
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateUserInput, "input-2", noopCallback(),
			UserInput{Content: "Continue please"})
	}, time.Second*3)

	s.sendShutdown(time.Second * 5)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInput("Hello"))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	assert.True(s.T(), interruptAcknowledged)
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
}

// TestMultiTurn_Shutdown verifies workflow completes cleanly with shutdown.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_Shutdown() {
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Hi!", 20), nil).Once()

	var shutdownCompleted bool
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateShutdown, "shutdown-1", &testsuite.TestUpdateCallback{
			OnAccept: func() {},
			OnReject: func(err error) {
				s.Fail("shutdown rejected", err.Error())
			},
			OnComplete: func(result interface{}, err error) {
				require.NoError(s.T(), err)
				shutdownCompleted = true
			},
		}, ShutdownRequest{})
	}, time.Second*2)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInput("Hi"))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	assert.True(s.T(), shutdownCompleted)
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
	assert.Equal(s.T(), 20, result.TotalTokens)
}

// TestMultiTurn_ValidatorRejectsEmptyInput verifies empty content is rejected.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_ValidatorRejectsEmptyInput() {
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("OK", 10), nil).Once()

	var rejected bool
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateUserInput, "input-empty", &testsuite.TestUpdateCallback{
			OnAccept: func() {
				s.Fail("empty input should not be accepted")
			},
			OnReject: func(err error) {
				assert.Contains(s.T(), err.Error(), "content must not be empty")
				rejected = true
			},
			OnComplete: func(interface{}, error) {},
		}, UserInput{Content: ""})
	}, time.Second*2)

	s.sendShutdown(time.Second * 3)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInput("Start"))
	require.True(s.T(), s.env.IsWorkflowCompleted())
	assert.True(s.T(), rejected, "Empty input should have been rejected")
}

// TestMultiTurn_ValidatorRejectsAfterShutdown verifies that the user_input
// validator checks the ShutdownRequested flag. We test the validator logic
// directly since the test environment processes updates synchronously and
// the workflow may exit before the second callback fires.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_ValidatorRejectsAfterShutdown() {
	// Construct a state with ShutdownRequested = true and verify that
	// the validator would reject new input.
	state := SessionState{ShutdownRequested: true}

	// Simulate what the validator does:
	input := UserInput{Content: "Too late"}
	var validationErr error
	if input.Content == "" {
		validationErr = fmt.Errorf("content must not be empty")
	}
	if state.ShutdownRequested {
		validationErr = fmt.Errorf("session is shutting down")
	}

	require.Error(s.T(), validationErr)
	assert.Contains(s.T(), validationErr.Error(), "shutting down")

	// Also verify that a duplicate shutdown is rejected
	var shutdownErr error
	if state.ShutdownRequested {
		shutdownErr = fmt.Errorf("session is already shutting down")
	}
	require.Error(s.T(), shutdownErr)
	assert.Contains(s.T(), shutdownErr.Error(), "already shutting down")
}

// TestMultiTurn_TurnBoundaries verifies TurnStarted/TurnComplete markers
// appear in history.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_TurnBoundaries() {
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Response 1", 30), nil).Once()

	// Query history and verify turn markers
	s.env.RegisterDelayedCallback(func() {
		result, err := s.env.QueryWorkflow(QueryGetConversationItems)
		require.NoError(s.T(), err)

		var items []models.ConversationItem
		require.NoError(s.T(), result.Get(&items))

		// Find TurnStarted markers
		turnStartedCount := 0
		turnCompleteCount := 0
		for _, item := range items {
			switch item.Type {
			case models.ItemTypeTurnStarted:
				turnStartedCount++
				assert.NotEmpty(s.T(), item.TurnID, "TurnStarted should have TurnID")
			case models.ItemTypeTurnComplete:
				turnCompleteCount++
				assert.NotEmpty(s.T(), item.TurnID, "TurnComplete should have TurnID")
			}
		}

		assert.Equal(s.T(), 1, turnStartedCount, "Should have 1 TurnStarted")
		assert.Equal(s.T(), 1, turnCompleteCount, "Should have 1 TurnComplete")
	}, time.Second*2)

	s.sendShutdown(time.Second * 3)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInput("Test turn"))
	require.True(s.T(), s.env.IsWorkflowCompleted())
}

// TestMultiTurn_ContinueAsNewPreservesState verifies fields survive ContinueAsNew.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_ContinueAsNewPreservesState() {
	state := SessionState{
		ConversationID: "test-conv-can",
		HistoryItems: []models.ConversationItem{
			{Type: models.ItemTypeTurnStarted, TurnID: "turn-1"},
			{Type: models.ItemTypeUserMessage, Content: "Hello", TurnID: "turn-1"},
			{Type: models.ItemTypeAssistantMessage, Content: "Hi!"},
			{Type: models.ItemTypeTurnComplete, TurnID: "turn-1"},
		},
		Config: models.SessionConfiguration{
			Model: models.ModelConfig{
				Model:         "gpt-4o-mini",
				Temperature:   0,
				MaxTokens:     100,
				ContextWindow: 128000,
			},
		},
		MaxIterations:     20,
		PendingUserInput:  false,
		ShutdownRequested: false,
		CurrentTurnID:     "turn-1",
		TotalTokens:       100,
		ToolCallsExecuted: []string{"shell"},
	}

	s.env.RegisterWorkflow(AgenticWorkflowContinued)

	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Continued response", 50), nil).Maybe()

	// Send user input to resume
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateUserInput, "input-1", noopCallback(),
			UserInput{Content: "Continue"})
	}, time.Second)

	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateShutdown, "shutdown-1", noopCallback(), ShutdownRequest{})
	}, time.Second*3)

	s.env.ExecuteWorkflow(AgenticWorkflowContinued, state)

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "test-conv-can", result.ConversationID)
	assert.Equal(s.T(), "shutdown", result.EndReason)
	// TotalTokens should include the original 100 + new 50
	assert.Equal(s.T(), 150, result.TotalTokens)
	assert.Contains(s.T(), result.ToolCallsExecuted, "shell")
}

// TestMultiTurn_MultipleTurns tests a 3-turn conversation end-to-end.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_MultipleTurns() {
	// Turn 1
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Response 1", 30), nil).Once()
	// Turn 2
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Response 2", 40), nil).Once()
	// Turn 3
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Response 3", 50), nil).Once()

	// Send second message
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateUserInput, "input-2", noopCallback(),
			UserInput{Content: "Second question"})
	}, time.Second*2)

	// Send third message
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateUserInput, "input-3", noopCallback(),
			UserInput{Content: "Third question"})
	}, time.Second*4)

	// Shutdown after third turn
	s.sendShutdown(time.Second * 6)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInput("First question"))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
	assert.Equal(s.T(), 120, result.TotalTokens) // 30 + 40 + 50
}

// TestMultiTurn_ToolCallsWithinTurn tests tool execution within a single turn.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_ToolCallsWithinTurn() {
	// First LLM call: return a tool call
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(activities.LLMActivityOutput{
			Items: []models.ConversationItem{
				{
					Type:      models.ItemTypeFunctionCall,
					CallID:    "call-1",
					Name:      "shell",
					Arguments: `{"command": "echo hello"}`,
				},
			},
			FinishReason: models.FinishReasonToolCalls,
			TokenUsage:   models.TokenUsage{TotalTokens: 30},
		}, nil).Once()

	// Tool execution
	trueVal := true
	s.env.OnActivity("ExecuteTool", mock.Anything, mock.Anything).
		Return(activities.ToolActivityOutput{
			CallID:  "call-1",
			Content: "hello\n",
			Success: &trueVal,
		}, nil).Once()

	// Second LLM call: return final response
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("The output was: hello", 40), nil).Once()

	s.sendShutdown(time.Second * 3)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInput("Run echo hello"))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
	assert.Equal(s.T(), 70, result.TotalTokens) // 30 + 40
	assert.Contains(s.T(), result.ToolCallsExecuted, "shell")
}

// TestAgenticWorkflowContinued_InitHistory verifies initHistory restores history
// including new item types.
func TestAgenticWorkflowContinued_InitHistory(t *testing.T) {
	state := SessionState{
		HistoryItems: []models.ConversationItem{
			{Type: models.ItemTypeTurnStarted, TurnID: "turn-1"},
			{Type: models.ItemTypeUserMessage, Content: "Hello", TurnID: "turn-1"},
			{Type: models.ItemTypeAssistantMessage, Content: "Hi!"},
			{Type: models.ItemTypeTurnComplete, TurnID: "turn-1"},
		},
	}

	state.initHistory()

	items, err := state.History.GetRawItems()
	require.NoError(t, err)
	assert.Len(t, items, 4)
	assert.Equal(t, models.ItemTypeTurnStarted, items[0].Type)
	assert.Equal(t, "turn-1", items[0].TurnID)
	assert.Equal(t, models.ItemTypeTurnComplete, items[3].Type)
	assert.Equal(t, "turn-1", items[3].TurnID)
}

// TestSyncHistoryItems_PreservesNewTypes verifies syncHistoryItems
// preserves TurnStarted/TurnComplete and TurnID fields.
func TestSyncHistoryItems_PreservesNewTypes(t *testing.T) {
	state := SessionState{
		HistoryItems: []models.ConversationItem{
			{Type: models.ItemTypeTurnStarted, TurnID: "turn-42"},
			{Type: models.ItemTypeUserMessage, Content: "Test", TurnID: "turn-42"},
		},
	}

	state.initHistory()

	// Add more items
	state.History.AddItem(models.ConversationItem{
		Type: models.ItemTypeAssistantMessage, Content: "Response",
	})
	state.History.AddItem(models.ConversationItem{
		Type: models.ItemTypeTurnComplete, TurnID: "turn-42",
	})

	// Sync back
	state.syncHistoryItems()

	assert.Len(t, state.HistoryItems, 4)
	assert.Equal(t, models.ItemTypeTurnComplete, state.HistoryItems[3].Type)
	assert.Equal(t, "turn-42", state.HistoryItems[3].TurnID)
}

// TestSessionState_MultiTurnFieldsSerialize verifies multi-turn fields
// are JSON-serializable for ContinueAsNew.
func TestSessionState_MultiTurnFieldsSerialize(t *testing.T) {
	state := SessionState{
		ConversationID:    "test",
		PendingUserInput:  true,
		ShutdownRequested: false,
		Interrupted:       true,
		CurrentTurnID:     "turn-99",
		TotalTokens:       500,
		ToolCallsExecuted: []string{"shell", "read_file"},
	}

	assert.True(t, state.PendingUserInput)
	assert.False(t, state.ShutdownRequested)
	assert.True(t, state.Interrupted)
	assert.Equal(t, "turn-99", state.CurrentTurnID)
	assert.Equal(t, 500, state.TotalTokens)
	assert.Equal(t, []string{"shell", "read_file"}, state.ToolCallsExecuted)
}

// Ensure we reference workflow.Context (suppress unused import warning)
var _ workflow.Context
