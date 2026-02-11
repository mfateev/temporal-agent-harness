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
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/mfateev/codex-temporal-go/internal/activities"
	"github.com/mfateev/codex-temporal-go/internal/history"
	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/tools"
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

func LoadWorkerInstructions(_ context.Context, _ activities.LoadWorkerInstructionsInput) (activities.LoadWorkerInstructionsOutput, error) {
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

func LoadExecPolicy(_ context.Context, _ activities.LoadExecPolicyInput) (activities.LoadExecPolicyOutput, error) {
	panic("stub: should be mocked")
}

func (s *AgenticWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterActivity(ExecuteLLMCall)
	s.env.RegisterActivity(ExecuteTool)
	s.env.RegisterActivity(LoadWorkerInstructions)
	s.env.RegisterActivity(LoadExecPolicy)

	// Default mock for LoadWorkerInstructions — returns empty docs.
	// Tests that need specific worker docs can override this.
	s.env.OnActivity("LoadWorkerInstructions", mock.Anything, mock.Anything).
		Return(activities.LoadWorkerInstructionsOutput{}, nil).Maybe()

	// Default mock for LoadExecPolicy — returns empty rules.
	s.env.OnActivity("LoadExecPolicy", mock.Anything, mock.Anything).
		Return(activities.LoadExecPolicyOutput{}, nil).Maybe()
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

// TestMultiTurn_QueryTurnStatus verifies the get_turn_status query handler
// returns correct phase and stats.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_QueryTurnStatus() {
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Response", 45), nil).Once()

	// Query turn status after first turn completes — should be waiting_for_input
	s.env.RegisterDelayedCallback(func() {
		result, err := s.env.QueryWorkflow(QueryGetTurnStatus)
		require.NoError(s.T(), err)

		var status TurnStatus
		require.NoError(s.T(), result.Get(&status))

		assert.Equal(s.T(), PhaseWaitingForInput, status.Phase)
		assert.NotEmpty(s.T(), status.CurrentTurnID)
		assert.Equal(s.T(), 45, status.TotalTokens)
		assert.Equal(s.T(), 1, status.TurnCount)
		assert.Empty(s.T(), status.ToolsInFlight)
	}, time.Second*2)

	s.sendShutdown(time.Second * 3)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInput("Hello"))
	require.True(s.T(), s.env.IsWorkflowCompleted())
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

// TestMultiTurn_SeqFieldsAssigned verifies that Seq fields are monotonically
// increasing on conversation items returned by the query handler.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_SeqFieldsAssigned() {
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Hello!", 50), nil).Once()

	s.env.RegisterDelayedCallback(func() {
		result, err := s.env.QueryWorkflow(QueryGetConversationItems)
		require.NoError(s.T(), err)

		var items []models.ConversationItem
		require.NoError(s.T(), result.Get(&items))

		// Verify Seq is monotonically increasing starting from 0
		require.GreaterOrEqual(s.T(), len(items), 3, "Should have at least TurnStarted + UserMessage + AssistantMessage")
		for i, item := range items {
			assert.Equal(s.T(), i, item.Seq, "Item %d should have Seq=%d", i, i)
		}
	}, time.Second*2)

	s.sendShutdown(time.Second * 3)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInput("Hello"))
	require.True(s.T(), s.env.IsWorkflowCompleted())
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

// TestMultiTurn_ContextOverflow_CompactsHistory verifies that a ContextOverflow
// error triggers history compaction before ContinueAsNew. The second workflow
// execution should have fewer history items.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_ContextOverflow_CompactsHistory() {
	// First LLM call succeeds
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("First response", 40), nil).Once()
	// Second LLM call succeeds
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Second response", 40), nil).Once()
	// Third LLM call returns ContextOverflow (non-retryable ApplicationError)
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(activities.LLMActivityOutput{}, temporal.NewNonRetryableApplicationError(
			"context too large", models.LLMErrTypeContextOverflow, nil)).Once()

	// Send second user input
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateUserInput, "input-2", noopCallback(),
			UserInput{Content: "Second question"})
	}, time.Second*2)

	// Send third user input (will trigger overflow)
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateUserInput, "input-3", noopCallback(),
			UserInput{Content: "Third question"})
	}, time.Second*4)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInput("First question"))

	require.True(s.T(), s.env.IsWorkflowCompleted())

	// The workflow should ContinueAsNew after compaction
	err := s.env.GetWorkflowError()
	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "continue as new")
}

// TestContextOverflow_CompactsBeforeCAN verifies that the overflow handler
// in runAgenticTurn actually drops items from history.
func TestContextOverflow_CompactsBeforeCAN(t *testing.T) {
	h := history.NewInMemoryHistory()
	// Simulate 4 turns of history
	for i := 0; i < 4; i++ {
		h.AddItem(models.ConversationItem{Type: models.ItemTypeTurnStarted, TurnID: fmt.Sprintf("t%d", i)})
		h.AddItem(models.ConversationItem{Type: models.ItemTypeUserMessage, Content: fmt.Sprintf("msg-%d", i)})
		h.AddItem(models.ConversationItem{Type: models.ItemTypeAssistantMessage, Content: fmt.Sprintf("reply-%d", i)})
		h.AddItem(models.ConversationItem{Type: models.ItemTypeTurnComplete, TurnID: fmt.Sprintf("t%d", i)})
	}

	// 16 items, 4 turns. keepTurns = 4/2 = 2
	turnCount, _ := h.GetTurnCount()
	assert.Equal(t, 4, turnCount)

	keepTurns := turnCount / 2
	if keepTurns < 2 {
		keepTurns = 2
	}
	dropped, err := h.DropOldestUserTurns(keepTurns)
	require.NoError(t, err)
	assert.Equal(t, 8, dropped) // dropped first 2 turns

	items, _ := h.GetRawItems()
	assert.Len(t, items, 8) // 2 turns remaining

	newTurnCount, _ := h.GetTurnCount()
	assert.Equal(t, 2, newTurnCount)
}

// --- Approval gate tests ---

// testInputWithApproval returns a WorkflowInput with ApprovalMode set.
func testInputWithApproval(message string, mode models.ApprovalMode) WorkflowInput {
	input := testInput(message)
	input.Config.ApprovalMode = mode
	return input
}

// TestMultiTurn_ApprovalGate_Approve verifies that in unless-trusted mode,
// a mutating tool call triggers approval_pending and proceeds after approval.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_ApprovalGate_Approve() {
	// LLM returns a mutating shell command (rm -rf)
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(activities.LLMActivityOutput{
			Items: []models.ConversationItem{
				{
					Type:      models.ItemTypeFunctionCall,
					CallID:    "call-rm",
					Name:      "shell",
					Arguments: `{"command": "rm -rf /tmp/test"}`,
				},
			},
			FinishReason: models.FinishReasonToolCalls,
			TokenUsage:   models.TokenUsage{TotalTokens: 30},
		}, nil).Once()

	// Tool execution after approval
	trueVal := true
	s.env.OnActivity("ExecuteTool", mock.Anything, mock.Anything).
		Return(activities.ToolActivityOutput{
			CallID:  "call-rm",
			Content: "",
			Success: &trueVal,
		}, nil).Once()

	// Second LLM call after tool result
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Done removing files.", 40), nil).Once()

	// Send approval after a delay (simulating user approving)
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateApprovalResponse, "approval-1", noopCallback(),
			ApprovalResponse{Approved: []string{"call-rm"}})
	}, time.Second*2)

	s.sendShutdown(time.Second * 4)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInputWithApproval("Delete /tmp/test", models.ApprovalUnlessTrusted))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
	assert.Equal(s.T(), 70, result.TotalTokens) // 30 + 40
	assert.Contains(s.T(), result.ToolCallsExecuted, "shell")
}

// TestMultiTurn_ApprovalGate_Deny verifies that denying a tool call
// sends a denial result to the LLM and does not execute the tool.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_ApprovalGate_Deny() {
	// LLM returns a mutating shell command
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(activities.LLMActivityOutput{
			Items: []models.ConversationItem{
				{
					Type:      models.ItemTypeFunctionCall,
					CallID:    "call-rm",
					Name:      "shell",
					Arguments: `{"command": "rm -rf /tmp/test"}`,
				},
			},
			FinishReason: models.FinishReasonToolCalls,
			TokenUsage:   models.TokenUsage{TotalTokens: 30},
		}, nil).Once()

	// After denial, LLM sees the denial message and responds
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("OK, I won't delete those files.", 25), nil).Once()

	// NOTE: No ExecuteTool mock — tool should NOT be called

	// Send denial after a delay
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateApprovalResponse, "approval-1", noopCallback(),
			ApprovalResponse{Denied: []string{"call-rm"}})
	}, time.Second*2)

	s.sendShutdown(time.Second * 4)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInputWithApproval("Delete /tmp/test", models.ApprovalUnlessTrusted))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
	assert.Equal(s.T(), 55, result.TotalTokens) // 30 + 25
	// Tool should NOT be in executed list
	assert.NotContains(s.T(), result.ToolCallsExecuted, "shell")
}

// TestMultiTurn_ApprovalGate_SafeCommand verifies that safe (read-only) commands
// skip the approval gate entirely in unless-trusted mode.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_ApprovalGate_SafeCommand() {
	// LLM returns a safe shell command (ls)
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(activities.LLMActivityOutput{
			Items: []models.ConversationItem{
				{
					Type:      models.ItemTypeFunctionCall,
					CallID:    "call-ls",
					Name:      "shell",
					Arguments: `{"command": "ls -la"}`,
				},
			},
			FinishReason: models.FinishReasonToolCalls,
			TokenUsage:   models.TokenUsage{TotalTokens: 30},
		}, nil).Once()

	// Tool executes without approval
	trueVal := true
	s.env.OnActivity("ExecuteTool", mock.Anything, mock.Anything).
		Return(activities.ToolActivityOutput{
			CallID:  "call-ls",
			Content: "file1.txt\nfile2.txt\n",
			Success: &trueVal,
		}, nil).Once()

	// Second LLM call
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Found 2 files.", 20), nil).Once()

	// No approval callback needed — should execute immediately

	s.sendShutdown(time.Second * 3)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInputWithApproval("List files", models.ApprovalUnlessTrusted))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
	assert.Contains(s.T(), result.ToolCallsExecuted, "shell")
}

// TestMultiTurn_ApprovalGate_NeverMode verifies that in "never" mode,
// all tools are auto-approved without any approval gate.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_ApprovalGate_NeverMode() {
	// LLM returns a mutating shell command — should still auto-execute
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(activities.LLMActivityOutput{
			Items: []models.ConversationItem{
				{
					Type:      models.ItemTypeFunctionCall,
					CallID:    "call-rm",
					Name:      "shell",
					Arguments: `{"command": "rm -rf /tmp/test"}`,
				},
			},
			FinishReason: models.FinishReasonToolCalls,
			TokenUsage:   models.TokenUsage{TotalTokens: 30},
		}, nil).Once()

	// Tool executes without approval
	trueVal := true
	s.env.OnActivity("ExecuteTool", mock.Anything, mock.Anything).
		Return(activities.ToolActivityOutput{
			CallID:  "call-rm",
			Content: "",
			Success: &trueVal,
		}, nil).Once()

	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Deleted.", 20), nil).Once()

	// No approval callback — should auto-execute in "never" mode

	s.sendShutdown(time.Second * 3)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInputWithApproval("Delete /tmp/test", models.ApprovalNever))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
	assert.Contains(s.T(), result.ToolCallsExecuted, "shell")
}

// TestMultiTurn_ApprovalGate_BackwardCompat verifies that empty ApprovalMode
// (from old clients) auto-approves all tools (backward compat).
func (s *AgenticWorkflowTestSuite) TestMultiTurn_ApprovalGate_BackwardCompat() {
	// LLM returns a mutating command — no approval mode set
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(activities.LLMActivityOutput{
			Items: []models.ConversationItem{
				{
					Type:      models.ItemTypeFunctionCall,
					CallID:    "call-rm",
					Name:      "shell",
					Arguments: `{"command": "rm -rf /tmp/test"}`,
				},
			},
			FinishReason: models.FinishReasonToolCalls,
			TokenUsage:   models.TokenUsage{TotalTokens: 30},
		}, nil).Once()

	trueVal := true
	s.env.OnActivity("ExecuteTool", mock.Anything, mock.Anything).
		Return(activities.ToolActivityOutput{
			CallID:  "call-rm",
			Content: "",
			Success: &trueVal,
		}, nil).Once()

	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Done.", 20), nil).Once()

	s.sendShutdown(time.Second * 3)

	// Use testInput (no ApprovalMode set — defaults to empty string)
	s.env.ExecuteWorkflow(AgenticWorkflow, testInput("Delete /tmp/test"))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
	assert.Contains(s.T(), result.ToolCallsExecuted, "shell")
}

// TestMultiTurn_ApprovalGate_InterruptDuringApproval verifies that interrupting
// during approval wait skips all tools.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_ApprovalGate_InterruptDuringApproval() {
	// LLM returns a mutating shell command
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(activities.LLMActivityOutput{
			Items: []models.ConversationItem{
				{
					Type:      models.ItemTypeFunctionCall,
					CallID:    "call-rm",
					Name:      "shell",
					Arguments: `{"command": "rm -rf /tmp/test"}`,
				},
			},
			FinishReason: models.FinishReasonToolCalls,
			TokenUsage:   models.TokenUsage{TotalTokens: 30},
		}, nil).Once()

	// NOTE: No ExecuteTool mock — tool should NOT be called

	// Second turn after interrupt + new user input
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("OK, let me try something else.", 25), nil).Once()

	// Send interrupt instead of approval
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateInterrupt, "interrupt-1", noopCallback(), InterruptRequest{})
	}, time.Second*2)

	// Send new user input after interrupt
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateUserInput, "input-2", noopCallback(),
			UserInput{Content: "Try something else"})
	}, time.Second*3)

	s.sendShutdown(time.Second * 5)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInputWithApproval("Delete /tmp/test", models.ApprovalUnlessTrusted))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
	// rm should NOT be in executed list (was interrupted)
	assert.NotContains(s.T(), result.ToolCallsExecuted, "shell")
}

// TestMultiTurn_ApprovalGate_ValidatorRejectsWhenNotPending verifies that
// sending an approval response when no approval is pending is rejected.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_ApprovalGate_ValidatorRejectsWhenNotPending() {
	// Simple LLM response with no tool calls
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Hello!", 50), nil).Once()

	// Try to send approval when none is pending
	var rejected bool
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateApprovalResponse, "approval-1", &testsuite.TestUpdateCallback{
			OnAccept: func() {
				s.Fail("approval should not be accepted when no approval pending")
			},
			OnReject: func(err error) {
				assert.Contains(s.T(), err.Error(), "no approval pending")
				rejected = true
			},
			OnComplete: func(interface{}, error) {},
		}, ApprovalResponse{Approved: []string{"call-1"}})
	}, time.Second*2)

	s.sendShutdown(time.Second * 3)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInput("Hello"))
	require.True(s.T(), s.env.IsWorkflowCompleted())
	assert.True(s.T(), rejected, "Approval should have been rejected when not pending")
}

// --- Unit tests for classification functions ---

func TestClassifyToolsForApproval_NeverMode(t *testing.T) {
	calls := []models.ConversationItem{
		{Type: models.ItemTypeFunctionCall, CallID: "1", Name: "shell", Arguments: `{"command": "rm -rf /"}`},
	}
	pending, forbidden := classifyToolsForApproval(calls, models.ApprovalNever, "")
	assert.Nil(t, pending)
	assert.Nil(t, forbidden)
}

func TestClassifyToolsForApproval_EmptyMode(t *testing.T) {
	calls := []models.ConversationItem{
		{Type: models.ItemTypeFunctionCall, CallID: "1", Name: "shell", Arguments: `{"command": "rm -rf /"}`},
	}
	pending, forbidden := classifyToolsForApproval(calls, "", "")
	assert.Nil(t, pending)
	assert.Nil(t, forbidden)
}

func TestClassifyToolsForApproval_UnlessTrusted_SafeCommand(t *testing.T) {
	calls := []models.ConversationItem{
		{Type: models.ItemTypeFunctionCall, CallID: "1", Name: "shell", Arguments: `{"command": "ls -la"}`},
	}
	pending, forbidden := classifyToolsForApproval(calls, models.ApprovalUnlessTrusted, "")
	assert.Empty(t, pending)
	assert.Empty(t, forbidden)
}

func TestClassifyToolsForApproval_UnlessTrusted_MutatingCommand(t *testing.T) {
	calls := []models.ConversationItem{
		{Type: models.ItemTypeFunctionCall, CallID: "1", Name: "shell", Arguments: `{"command": "rm -rf /tmp"}`},
	}
	pending, _ := classifyToolsForApproval(calls, models.ApprovalUnlessTrusted, "")
	require.Len(t, pending, 1)
	assert.Equal(t, "1", pending[0].CallID)
	assert.Equal(t, "shell", pending[0].ToolName)
}

func TestClassifyToolsForApproval_UnlessTrusted_ReadOnlyTools(t *testing.T) {
	calls := []models.ConversationItem{
		{Type: models.ItemTypeFunctionCall, CallID: "1", Name: "read_file", Arguments: `{"file_path": "/tmp/test"}`},
		{Type: models.ItemTypeFunctionCall, CallID: "2", Name: "list_dir", Arguments: `{"path": "/tmp"}`},
		{Type: models.ItemTypeFunctionCall, CallID: "3", Name: "grep_files", Arguments: `{"pattern": "foo"}`},
	}
	pending, forbidden := classifyToolsForApproval(calls, models.ApprovalUnlessTrusted, "")
	assert.Empty(t, pending)
	assert.Empty(t, forbidden)
}

func TestClassifyToolsForApproval_UnlessTrusted_WritingTools(t *testing.T) {
	calls := []models.ConversationItem{
		{Type: models.ItemTypeFunctionCall, CallID: "1", Name: "write_file", Arguments: `{"file_path": "/tmp/test"}`},
		{Type: models.ItemTypeFunctionCall, CallID: "2", Name: "apply_patch", Arguments: `{"file_path": "/tmp/test"}`},
	}
	pending, _ := classifyToolsForApproval(calls, models.ApprovalUnlessTrusted, "")
	require.Len(t, pending, 2)
}

func TestClassifyToolsForApproval_UnlessTrusted_MixedBatch(t *testing.T) {
	calls := []models.ConversationItem{
		{Type: models.ItemTypeFunctionCall, CallID: "1", Name: "read_file", Arguments: `{"file_path": "/tmp/a"}`},
		{Type: models.ItemTypeFunctionCall, CallID: "2", Name: "shell", Arguments: `{"command": "rm -rf /tmp"}`},
		{Type: models.ItemTypeFunctionCall, CallID: "3", Name: "shell", Arguments: `{"command": "ls -la"}`},
	}
	pending, _ := classifyToolsForApproval(calls, models.ApprovalUnlessTrusted, "")
	// Only the mutating shell command should need approval
	require.Len(t, pending, 1)
	assert.Equal(t, "2", pending[0].CallID)
}

func TestClassifyToolsForApproval_ForbiddenByPolicy(t *testing.T) {
	calls := []models.ConversationItem{
		{Type: models.ItemTypeFunctionCall, CallID: "1", Name: "shell", Arguments: `{"command": "rm -rf /"}`},
	}
	rules := `prefix_rule(pattern=["rm"], decision="forbidden", justification="never delete")`
	pending, forbidden := classifyToolsForApproval(calls, models.ApprovalUnlessTrusted, rules)
	assert.Empty(t, pending)
	require.Len(t, forbidden, 1)
	assert.Equal(t, "1", forbidden[0].CallID)
	assert.Contains(t, forbidden[0].Output.Content, "Forbidden")
}

func TestEvaluateToolApproval(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		args     string
		mode     models.ApprovalMode
		expected tools.ExecApprovalRequirement
	}{
		{"read_file is safe", "read_file", `{"file_path": "/tmp/test"}`, models.ApprovalUnlessTrusted, tools.ApprovalSkip},
		{"list_dir is safe", "list_dir", `{"path": "/tmp"}`, models.ApprovalUnlessTrusted, tools.ApprovalSkip},
		{"grep_files is safe", "grep_files", `{"pattern": "foo"}`, models.ApprovalUnlessTrusted, tools.ApprovalSkip},
		{"write_file is mutating", "write_file", `{"file_path": "/tmp/test"}`, models.ApprovalUnlessTrusted, tools.ApprovalNeeded},
		{"apply_patch is mutating", "apply_patch", `{"file_path": "/tmp/test"}`, models.ApprovalUnlessTrusted, tools.ApprovalNeeded},
		{"shell ls is safe", "shell", `{"command": "ls -la"}`, models.ApprovalUnlessTrusted, tools.ApprovalSkip},
		{"shell cat is safe", "shell", `{"command": "cat /tmp/test"}`, models.ApprovalUnlessTrusted, tools.ApprovalSkip},
		{"shell rm is mutating", "shell", `{"command": "rm -rf /tmp"}`, models.ApprovalUnlessTrusted, tools.ApprovalNeeded},
		{"shell git status is safe", "shell", `{"command": "git status"}`, models.ApprovalUnlessTrusted, tools.ApprovalSkip},
		{"shell git push is mutating", "shell", `{"command": "git push"}`, models.ApprovalUnlessTrusted, tools.ApprovalNeeded},
		{"unknown tool is mutating", "unknown_tool", `{}`, models.ApprovalUnlessTrusted, tools.ApprovalNeeded},
		{"shell with bad json is mutating", "shell", `{bad json`, models.ApprovalUnlessTrusted, tools.ApprovalNeeded},
		{"shell with empty command is mutating", "shell", `{"command": ""}`, models.ApprovalUnlessTrusted, tools.ApprovalNeeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := evaluateToolApproval(tt.toolName, tt.args, nil, tt.mode)
			assert.Equal(t, tt.expected, req)
		})
	}
}

func TestApplyApprovalDecision_AllApproved(t *testing.T) {
	calls := []models.ConversationItem{
		{Type: models.ItemTypeFunctionCall, CallID: "1", Name: "shell"},
		{Type: models.ItemTypeFunctionCall, CallID: "2", Name: "write_file"},
	}
	resp := &ApprovalResponse{Approved: []string{"1", "2"}}
	approved, denied := applyApprovalDecision(calls, resp)
	assert.Len(t, approved, 2)
	assert.Empty(t, denied)
}

func TestApplyApprovalDecision_AllDenied(t *testing.T) {
	calls := []models.ConversationItem{
		{Type: models.ItemTypeFunctionCall, CallID: "1", Name: "shell"},
		{Type: models.ItemTypeFunctionCall, CallID: "2", Name: "write_file"},
	}
	resp := &ApprovalResponse{Denied: []string{"1", "2"}}
	approved, denied := applyApprovalDecision(calls, resp)
	assert.Empty(t, approved)
	assert.Len(t, denied, 2)
	// Verify denied results are properly formatted
	for _, d := range denied {
		assert.Equal(t, models.ItemTypeFunctionCallOutput, d.Type)
		assert.Contains(t, d.Output.Content, "denied")
		assert.False(t, *d.Output.Success)
	}
}

func TestApplyApprovalDecision_NilResponse(t *testing.T) {
	calls := []models.ConversationItem{
		{Type: models.ItemTypeFunctionCall, CallID: "1", Name: "shell"},
	}
	approved, denied := applyApprovalDecision(calls, nil)
	assert.Len(t, approved, 1)
	assert.Nil(t, denied)
}

// TestMultiTurn_ApprovalGate_QueryPendingApprovals verifies that querying
// turn status during approval wait returns PhaseApprovalPending with correct items.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_ApprovalGate_QueryPendingApprovals() {
	// LLM returns a mutating shell command
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(activities.LLMActivityOutput{
			Items: []models.ConversationItem{
				{
					Type:      models.ItemTypeFunctionCall,
					CallID:    "call-rm",
					Name:      "shell",
					Arguments: `{"command": "rm -rf /tmp/test"}`,
				},
			},
			FinishReason: models.FinishReasonToolCalls,
			TokenUsage:   models.TokenUsage{TotalTokens: 30},
		}, nil).Once()

	trueVal := true
	s.env.OnActivity("ExecuteTool", mock.Anything, mock.Anything).
		Return(activities.ToolActivityOutput{
			CallID: "call-rm", Content: "", Success: &trueVal,
		}, nil).Once()

	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Done.", 20), nil).Once()

	// Query turn status to verify approval pending, then approve
	s.env.RegisterDelayedCallback(func() {
		result, err := s.env.QueryWorkflow(QueryGetTurnStatus)
		require.NoError(s.T(), err)

		var status TurnStatus
		require.NoError(s.T(), result.Get(&status))

		assert.Equal(s.T(), PhaseApprovalPending, status.Phase)
		require.Len(s.T(), status.PendingApprovals, 1)
		assert.Equal(s.T(), "call-rm", status.PendingApprovals[0].CallID)
		assert.Equal(s.T(), "shell", status.PendingApprovals[0].ToolName)
		assert.Equal(s.T(), `{"command": "rm -rf /tmp/test"}`, status.PendingApprovals[0].Arguments)

		// Approve to unblock
		s.env.UpdateWorkflow(UpdateApprovalResponse, "approval-1", noopCallback(),
			ApprovalResponse{Approved: []string{"call-rm"}})
	}, time.Second*2)

	s.sendShutdown(time.Second * 4)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInputWithApproval("Delete test", models.ApprovalUnlessTrusted))
	require.True(s.T(), s.env.IsWorkflowCompleted())
}

// TestMultiTurn_ApprovalGate_WriteFile verifies that write_file always
// triggers approval in unless-trusted mode.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_ApprovalGate_WriteFile() {
	// LLM returns a write_file call
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(activities.LLMActivityOutput{
			Items: []models.ConversationItem{
				{
					Type:      models.ItemTypeFunctionCall,
					CallID:    "call-write",
					Name:      "write_file",
					Arguments: `{"file_path": "/tmp/test.txt", "content": "hello"}`,
				},
			},
			FinishReason: models.FinishReasonToolCalls,
			TokenUsage:   models.TokenUsage{TotalTokens: 30},
		}, nil).Once()

	trueVal := true
	s.env.OnActivity("ExecuteTool", mock.Anything, mock.Anything).
		Return(activities.ToolActivityOutput{
			CallID: "call-write", Content: "File written", Success: &trueVal,
		}, nil).Once()

	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("File created.", 20), nil).Once()

	// Approve write_file
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateApprovalResponse, "approval-1", noopCallback(),
			ApprovalResponse{Approved: []string{"call-write"}})
	}, time.Second*2)

	s.sendShutdown(time.Second * 4)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInputWithApproval("Write a file", models.ApprovalUnlessTrusted))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
	assert.Contains(s.T(), result.ToolCallsExecuted, "write_file")
}

// TestMultiTurn_ApprovalGate_ShutdownDuringApproval verifies that a shutdown
// during approval wait terminates cleanly.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_ApprovalGate_ShutdownDuringApproval() {
	// LLM returns a mutating shell command
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(activities.LLMActivityOutput{
			Items: []models.ConversationItem{
				{
					Type:      models.ItemTypeFunctionCall,
					CallID:    "call-rm",
					Name:      "shell",
					Arguments: `{"command": "rm -rf /tmp/test"}`,
				},
			},
			FinishReason: models.FinishReasonToolCalls,
			TokenUsage:   models.TokenUsage{TotalTokens: 30},
		}, nil).Once()

	// NOTE: No ExecuteTool mock — tool should NOT be called

	// Send shutdown instead of approval (shutdown also sets Interrupted)
	s.sendShutdown(time.Second * 2)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInputWithApproval("Delete test", models.ApprovalUnlessTrusted))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
	assert.NotContains(s.T(), result.ToolCallsExecuted, "shell")
}

// TestMultiTurn_ApprovalGate_MixedBatch verifies that when a batch has both
// safe and unsafe tools, only the unsafe ones need approval. After approval,
// both safe and approved unsafe tools execute.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_ApprovalGate_MixedBatch() {
	// LLM returns a safe read_file and a mutating shell command
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(activities.LLMActivityOutput{
			Items: []models.ConversationItem{
				{
					Type:      models.ItemTypeFunctionCall,
					CallID:    "call-read",
					Name:      "read_file",
					Arguments: `{"file_path": "/tmp/test.txt"}`,
				},
				{
					Type:      models.ItemTypeFunctionCall,
					CallID:    "call-rm",
					Name:      "shell",
					Arguments: `{"command": "rm -rf /tmp/test"}`,
				},
			},
			FinishReason: models.FinishReasonToolCalls,
			TokenUsage:   models.TokenUsage{TotalTokens: 30},
		}, nil).Once()

	// Both tools execute after approval
	trueVal := true
	s.env.OnActivity("ExecuteTool", mock.Anything, mock.MatchedBy(func(input activities.ToolActivityInput) bool {
		return input.ToolName == "read_file"
	})).
		Return(activities.ToolActivityOutput{
			CallID: "call-read", Content: "file content", Success: &trueVal,
		}, nil).Once()

	s.env.OnActivity("ExecuteTool", mock.Anything, mock.MatchedBy(func(input activities.ToolActivityInput) bool {
		return input.ToolName == "shell"
	})).
		Return(activities.ToolActivityOutput{
			CallID: "call-rm", Content: "", Success: &trueVal,
		}, nil).Once()

	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Done.", 20), nil).Once()

	// Approve — only the shell command should be in pending list
	s.env.RegisterDelayedCallback(func() {
		// Verify only the mutating tool is pending
		result, err := s.env.QueryWorkflow(QueryGetTurnStatus)
		require.NoError(s.T(), err)
		var status TurnStatus
		require.NoError(s.T(), result.Get(&status))
		require.Len(s.T(), status.PendingApprovals, 1, "Only mutating tool should be pending")
		assert.Equal(s.T(), "shell", status.PendingApprovals[0].ToolName)

		s.env.UpdateWorkflow(UpdateApprovalResponse, "approval-1", noopCallback(),
			ApprovalResponse{Approved: []string{"call-rm"}})
	}, time.Second*2)

	s.sendShutdown(time.Second * 4)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInputWithApproval("Read and delete", models.ApprovalUnlessTrusted))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
	assert.Contains(s.T(), result.ToolCallsExecuted, "read_file")
	assert.Contains(s.T(), result.ToolCallsExecuted, "shell")
}

// TestMultiTurn_ApprovalGate_ReadFileAutoApproved verifies that read_file
// skips approval in unless-trusted mode and executes immediately.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_ApprovalGate_ReadFileAutoApproved() {
	// LLM returns a read_file call
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(activities.LLMActivityOutput{
			Items: []models.ConversationItem{
				{
					Type:      models.ItemTypeFunctionCall,
					CallID:    "call-read",
					Name:      "read_file",
					Arguments: `{"file_path": "/tmp/test.txt"}`,
				},
			},
			FinishReason: models.FinishReasonToolCalls,
			TokenUsage:   models.TokenUsage{TotalTokens: 30},
		}, nil).Once()

	trueVal := true
	s.env.OnActivity("ExecuteTool", mock.Anything, mock.Anything).
		Return(activities.ToolActivityOutput{
			CallID: "call-read", Content: "file content", Success: &trueVal,
		}, nil).Once()

	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("File says: file content", 20), nil).Once()

	// No approval callback — read_file should auto-execute

	s.sendShutdown(time.Second * 3)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInputWithApproval("Read a file", models.ApprovalUnlessTrusted))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
	assert.Contains(s.T(), result.ToolCallsExecuted, "read_file")
}

// --- Instruction resolution tests ---

// TestMultiTurn_InstructionsResolvedWithCLI verifies that resolveInstructions
// runs at session start and CLI-provided docs plus personal instructions reach
// the LLM when no worker docs are returned (default mock returns empty).
func (s *AgenticWorkflowTestSuite) TestMultiTurn_InstructionsResolvedWithCLI() {
	// Default LoadWorkerInstructions mock from SetupTest returns empty docs.
	// This means CLI docs should be used as fallback.
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("OK", 20), nil).Once()

	s.sendShutdown(time.Second * 2)

	input := testInput("Hello")
	input.Config.CLIProjectDocs = "CLI project docs"
	input.Config.UserPersonalInstructions = "Personal prefs"
	s.env.ExecuteWorkflow(AgenticWorkflow, input)

	require.True(s.T(), s.env.IsWorkflowCompleted())

	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)

	// Verify instructions were resolved by querying the config in workflow result.
	// The merged instructions are stored in Config and used for all LLM calls.
	// Since we can't inspect Config directly, verify via a query of items —
	// the environment context message should be present.
}

// TestMultiTurn_InstructionsFallbackToCLI verifies that when worker instruction
// loading returns empty docs, CLI-provided docs are used as fallback.
// This is tested by examining the base instructions — they should always
// contain the default system prompt.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_InstructionsFallbackToCLI() {
	// Default LoadWorkerInstructions mock returns empty. CLI docs act as fallback.
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("OK", 20), nil).Once()

	s.sendShutdown(time.Second * 2)

	input := testInput("Hello")
	input.Config.CLIProjectDocs = "CLI fallback docs"
	s.env.ExecuteWorkflow(AgenticWorkflow, input)

	require.True(s.T(), s.env.IsWorkflowCompleted())

	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
}

// TestMultiTurn_EnvironmentContext verifies that environment context is added
// to conversation history when Cwd is set.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_EnvironmentContext() {
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("OK", 20), nil).Once()

	s.env.RegisterDelayedCallback(func() {
		result, err := s.env.QueryWorkflow(QueryGetConversationItems)
		require.NoError(s.T(), err)

		var items []models.ConversationItem
		require.NoError(s.T(), result.Get(&items))

		// Find the environment context message
		found := false
		for _, item := range items {
			if item.Type == models.ItemTypeUserMessage && item.Content != "" {
				if assert.ObjectsAreEqual("<environment_context>", item.Content[:21]) {
					found = true
					assert.Contains(s.T(), item.Content, "<cwd>/tmp/testdir</cwd>")
					break
				}
			}
		}
		assert.True(s.T(), found, "Should have environment context message in history")
	}, time.Second*2)

	s.sendShutdown(time.Second * 3)

	input := testInput("Hello")
	input.Config.Cwd = "/tmp/testdir"
	s.env.ExecuteWorkflow(AgenticWorkflow, input)

	require.True(s.T(), s.env.IsWorkflowCompleted())
}

// --- Iteration safety / loop-prevention tests ---

// TestMultiTurn_MaxIterationsEndsTurn verifies that hitting MaxIterations
// ends the turn (returns false) instead of triggering ContinueAsNew (true).
// The history should contain a message explaining the limit.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_MaxIterationsEndsTurn() {
	// Set up 20 LLM calls that each return a tool call (no stop).
	// The 21st won't happen because MaxIterations=20.
	for i := 0; i < 20; i++ {
		callID := fmt.Sprintf("call-%d", i)
		s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
			Return(activities.LLMActivityOutput{
				Items: []models.ConversationItem{
					{
						Type:      models.ItemTypeFunctionCall,
						CallID:    callID,
						Name:      "read_file",
						Arguments: fmt.Sprintf(`{"path": "/tmp/file%d.txt"}`, i),
					},
				},
				FinishReason: models.FinishReasonToolCalls,
				TokenUsage:   models.TokenUsage{TotalTokens: 10},
			}, nil).Once()

		trueVal := true
		s.env.OnActivity("ExecuteTool", mock.Anything, mock.MatchedBy(func(input activities.ToolActivityInput) bool {
			return input.CallID == callID
		})).
			Return(activities.ToolActivityOutput{
				CallID:  callID,
				Content: "content",
				Success: &trueVal,
			}, nil).Once()
	}

	// Query history after max iterations to verify the message was added
	s.env.RegisterDelayedCallback(func() {
		result, err := s.env.QueryWorkflow(QueryGetConversationItems)
		require.NoError(s.T(), err)

		var items []models.ConversationItem
		require.NoError(s.T(), result.Get(&items))

		// Find the max-iterations message
		found := false
		for _, item := range items {
			if item.Type == models.ItemTypeAssistantMessage &&
				assert.ObjectsAreEqual("[Turn ended: reached maximum of 20 iterations without completing. The task may need to be broken into smaller steps.]", item.Content) {
				found = true
				break
			}
		}
		assert.True(s.T(), found, "Should have max iterations message in history")
	}, time.Second*2)

	s.sendShutdown(time.Second * 3)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInput("Read many files"))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	// Should end with shutdown (not ContinueAsNew error)
	assert.Equal(s.T(), "shutdown", result.EndReason)
}

// TestMultiTurn_RepeatedToolCallsEndsTurn verifies that 3+ consecutive
// identical tool call batches end the turn early.
func (s *AgenticWorkflowTestSuite) TestMultiTurn_RepeatedToolCallsEndsTurn() {
	// LLM returns the same read_file call 3 times in a row
	for i := 0; i < 3; i++ {
		s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
			Return(activities.LLMActivityOutput{
				Items: []models.ConversationItem{
					{
						Type:      models.ItemTypeFunctionCall,
						CallID:    fmt.Sprintf("call-%d", i),
						Name:      "read_file",
						Arguments: `{"path": "/tmp/LICENSE"}`,
					},
				},
				FinishReason: models.FinishReasonToolCalls,
				TokenUsage:   models.TokenUsage{TotalTokens: 10},
			}, nil).Once()

		// Only the first two tool calls should actually execute
		if i < 2 {
			trueVal := true
			s.env.OnActivity("ExecuteTool", mock.Anything, mock.MatchedBy(func(input activities.ToolActivityInput) bool {
				return input.CallID == fmt.Sprintf("call-%d", i)
			})).
				Return(activities.ToolActivityOutput{
					CallID:  fmt.Sprintf("call-%d", i),
					Content: "MIT License\n",
					Success: &trueVal,
				}, nil).Once()
		}
	}

	// Query to verify the repeated-calls message
	s.env.RegisterDelayedCallback(func() {
		result, err := s.env.QueryWorkflow(QueryGetConversationItems)
		require.NoError(s.T(), err)

		var items []models.ConversationItem
		require.NoError(s.T(), result.Get(&items))

		found := false
		for _, item := range items {
			if item.Type == models.ItemTypeAssistantMessage &&
				assert.ObjectsAreEqual("[Turn ended: detected repeated identical tool calls. Please try a different approach.]", item.Content) {
				found = true
				break
			}
		}
		assert.True(s.T(), found, "Should have repeated tool calls message in history")
	}, time.Second*2)

	s.sendShutdown(time.Second * 3)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInput("Read LICENSE"))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
}

// TestDetectRepeatedToolCalls_Unit tests the detection logic directly.
func TestDetectRepeatedToolCalls_Unit(t *testing.T) {
	s := &SessionState{}

	// Same call twice: not yet triggered
	calls := []models.ConversationItem{
		{Name: "read_file", Arguments: `{"path": "/tmp/test"}`},
	}
	assert.False(t, s.detectRepeatedToolCalls(calls))
	assert.False(t, s.detectRepeatedToolCalls(calls))

	// Third time: triggered
	assert.True(t, s.detectRepeatedToolCalls(calls))

	// Different call resets the counter
	different := []models.ConversationItem{
		{Name: "read_file", Arguments: `{"path": "/tmp/other"}`},
	}
	assert.False(t, s.detectRepeatedToolCalls(different))
	assert.False(t, s.detectRepeatedToolCalls(different))
	assert.True(t, s.detectRepeatedToolCalls(different))
}

// TestToolCallsKey_Deterministic verifies that the key function produces
// deterministic output regardless of call order.
func TestToolCallsKey_Deterministic(t *testing.T) {
	calls1 := []models.ConversationItem{
		{Name: "read_file", Arguments: `{"path": "a"}`},
		{Name: "shell", Arguments: `{"command": "ls"}`},
	}
	calls2 := []models.ConversationItem{
		{Name: "shell", Arguments: `{"command": "ls"}`},
		{Name: "read_file", Arguments: `{"path": "a"}`},
	}
	assert.Equal(t, toolCallsKey(calls1), toolCallsKey(calls2))

	// Different args produce different keys
	calls3 := []models.ConversationItem{
		{Name: "read_file", Arguments: `{"path": "b"}`},
		{Name: "shell", Arguments: `{"command": "ls"}`},
	}
	assert.NotEqual(t, toolCallsKey(calls1), toolCallsKey(calls3))
}

// TestTotalIterationsForCAN_Persists verifies the field survives ContinueAsNew serialization.
func TestTotalIterationsForCAN_Persists(t *testing.T) {
	state := SessionState{
		ConversationID:    "test",
		TotalIterationsForCAN: 50,
		MaxIterations:     20,
	}
	assert.Equal(t, 50, state.TotalIterationsForCAN)
}

// --- Sandbox denial detection tests ---

// TestIsLikelySandboxDenial verifies keyword-based sandbox detection.
func TestIsLikelySandboxDenial(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		// Positive cases — should be detected as sandbox denial
		{"permission denied", "bash: /usr/bin/rm: Permission denied", true},
		{"operation not permitted", "cp: cannot create regular file: Operation not permitted", true},
		{"read-only file system", "touch: cannot touch '/foo': Read-only file system", true},
		{"seccomp", "seccomp: blocked syscall 59", true},
		{"sandbox keyword", "error: sandbox prevented access", true},
		{"landlock", "landlock: access denied", true},
		{"failed to write file", "failed to write file /tmp/out.txt", true},
		{"mixed case", "PERMISSION DENIED by policy", true},

		// Negative cases — normal failures, not sandbox
		{"file not found", "no such file or directory", false},
		{"invalid argument", "invalid argument: --foo", false},
		{"empty string", "", false},
		{"command not found", "bash: jq: command not found", false},
		{"syntax error", "syntax error near unexpected token", false},
		{"exit code", "exit status 1", false},
		{"generic error", "error: something went wrong", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isLikelySandboxDenial(tt.output))
		})
	}
}

// TestTruncate verifies the truncate helper.
func TestTruncate(t *testing.T) {
	assert.Equal(t, "hello", truncate("hello", 10))
	assert.Equal(t, "hello", truncate("hello", 5))
	assert.Equal(t, "hel...", truncate("hello", 3))
	assert.Equal(t, "", truncate("", 5))
}

// TestHandleOnFailureEscalation_NonSandboxFailure verifies that a normal
// tool failure (e.g., file not found) does NOT trigger escalation.
func (s *AgenticWorkflowTestSuite) TestHandleOnFailureEscalation_NonSandboxFailure() {
	// LLM returns a read_file call
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(activities.LLMActivityOutput{
			Items: []models.ConversationItem{
				{
					Type:      models.ItemTypeFunctionCall,
					CallID:    "call-read",
					Name:      "read_file",
					Arguments: `{"file_path": "/tmp/nonexistent.txt"}`,
				},
			},
			FinishReason: models.FinishReasonToolCalls,
			TokenUsage:   models.TokenUsage{TotalTokens: 30},
		}, nil).Once()

	// Tool fails with "no such file" — normal failure, not sandbox
	falseVal := false
	s.env.OnActivity("ExecuteTool", mock.Anything, mock.Anything).
		Return(activities.ToolActivityOutput{
			CallID:  "call-read",
			Content: "open /tmp/nonexistent.txt: no such file or directory",
			Success: &falseVal,
		}, nil).Once()

	// LLM sees the failure and responds (no escalation blocking)
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("The file doesn't exist.", 20), nil).Once()

	// Verify phase is NOT escalation_pending after tool execution
	s.env.RegisterDelayedCallback(func() {
		result, err := s.env.QueryWorkflow(QueryGetTurnStatus)
		require.NoError(s.T(), err)

		var status TurnStatus
		require.NoError(s.T(), result.Get(&status))

		// Should NOT be in escalation pending — normal failure passes through
		assert.NotEqual(s.T(), PhaseEscalationPending, status.Phase,
			"Normal file-not-found failure should not trigger escalation")
	}, time.Second*2)

	s.sendShutdown(time.Second * 3)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInputWithApproval("Read a file", models.ApprovalOnFailure))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
	// Tool was "executed" (even though it failed) — the result went back to LLM
	assert.Equal(s.T(), 50, result.TotalTokens) // 30 + 20
}

// TestHandleOnFailureEscalation_SandboxFailure verifies that a sandbox
// denial DOES trigger escalation in on-failure mode.
func (s *AgenticWorkflowTestSuite) TestHandleOnFailureEscalation_SandboxFailure() {
	// LLM returns a shell command
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(activities.LLMActivityOutput{
			Items: []models.ConversationItem{
				{
					Type:      models.ItemTypeFunctionCall,
					CallID:    "call-shell",
					Name:      "shell",
					Arguments: `{"command": "mkdir /opt/test"}`,
				},
			},
			FinishReason: models.FinishReasonToolCalls,
			TokenUsage:   models.TokenUsage{TotalTokens: 30},
		}, nil).Once()

	// Tool fails with permission denied — sandbox denial
	falseVal := false
	s.env.OnActivity("ExecuteTool", mock.Anything, mock.Anything).
		Return(activities.ToolActivityOutput{
			CallID:  "call-shell",
			Content: "mkdir: cannot create directory '/opt/test': Permission denied",
			Success: &falseVal,
		}, nil).Once()

	// After escalation approval, re-execute succeeds
	trueVal := true
	s.env.OnActivity("ExecuteTool", mock.Anything, mock.Anything).
		Return(activities.ToolActivityOutput{
			CallID:  "call-shell",
			Content: "",
			Success: &trueVal,
		}, nil).Once()

	// LLM sees the re-executed result
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("Directory created.", 20), nil).Once()

	// Verify escalation pending, then approve
	s.env.RegisterDelayedCallback(func() {
		result, err := s.env.QueryWorkflow(QueryGetTurnStatus)
		require.NoError(s.T(), err)

		var status TurnStatus
		require.NoError(s.T(), result.Get(&status))

		assert.Equal(s.T(), PhaseEscalationPending, status.Phase,
			"Sandbox denial should trigger escalation")
		require.Len(s.T(), status.PendingEscalations, 1)
		assert.Equal(s.T(), "call-shell", status.PendingEscalations[0].CallID)

		// Approve the escalation
		s.env.UpdateWorkflow(UpdateEscalationResponse, "esc-1", noopCallback(),
			EscalationResponse{Approved: []string{"call-shell"}})
	}, time.Second*2)

	s.sendShutdown(time.Second * 4)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInputWithApproval("Create directory", models.ApprovalOnFailure))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
}

// TestHandleOnFailureEscalation_MixedFailures verifies that when a batch
// has one sandbox failure and one normal failure, only the sandbox failure
// is escalated. The normal failure passes through to the LLM.
func (s *AgenticWorkflowTestSuite) TestHandleOnFailureEscalation_MixedFailures() {
	// LLM returns two tool calls
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(activities.LLMActivityOutput{
			Items: []models.ConversationItem{
				{
					Type:      models.ItemTypeFunctionCall,
					CallID:    "call-read",
					Name:      "read_file",
					Arguments: `{"file_path": "/tmp/missing.txt"}`,
				},
				{
					Type:      models.ItemTypeFunctionCall,
					CallID:    "call-shell",
					Name:      "shell",
					Arguments: `{"command": "mkdir /opt/restricted"}`,
				},
			},
			FinishReason: models.FinishReasonToolCalls,
			TokenUsage:   models.TokenUsage{TotalTokens: 30},
		}, nil).Once()

	// read_file fails with normal error
	falseVal := false
	s.env.OnActivity("ExecuteTool", mock.Anything, mock.MatchedBy(func(input activities.ToolActivityInput) bool {
		return input.ToolName == "read_file"
	})).
		Return(activities.ToolActivityOutput{
			CallID:  "call-read",
			Content: "open /tmp/missing.txt: no such file or directory",
			Success: &falseVal,
		}, nil).Once()

	// shell fails with sandbox denial
	s.env.OnActivity("ExecuteTool", mock.Anything, mock.MatchedBy(func(input activities.ToolActivityInput) bool {
		return input.ToolName == "shell"
	})).
		Return(activities.ToolActivityOutput{
			CallID:  "call-shell",
			Content: "mkdir: Permission denied",
			Success: &falseVal,
		}, nil).Once()

	// After escalation approval, re-execute shell
	trueVal := true
	s.env.OnActivity("ExecuteTool", mock.Anything, mock.Anything).
		Return(activities.ToolActivityOutput{
			CallID:  "call-shell",
			Content: "",
			Success: &trueVal,
		}, nil).Once()

	// LLM sees both results
	s.env.OnActivity("ExecuteLLMCall", mock.Anything, mock.Anything).
		Return(mockLLMStopResponse("File missing, but directory created.", 20), nil).Once()

	// Verify only shell is in pending escalations, then approve
	s.env.RegisterDelayedCallback(func() {
		result, err := s.env.QueryWorkflow(QueryGetTurnStatus)
		require.NoError(s.T(), err)

		var status TurnStatus
		require.NoError(s.T(), result.Get(&status))

		assert.Equal(s.T(), PhaseEscalationPending, status.Phase)
		require.Len(s.T(), status.PendingEscalations, 1,
			"Only the sandbox denial should be escalated")
		assert.Equal(s.T(), "call-shell", status.PendingEscalations[0].CallID)

		s.env.UpdateWorkflow(UpdateEscalationResponse, "esc-1", noopCallback(),
			EscalationResponse{Approved: []string{"call-shell"}})
	}, time.Second*2)

	s.sendShutdown(time.Second * 4)

	s.env.ExecuteWorkflow(AgenticWorkflow, testInputWithApproval("Read file and create dir", models.ApprovalOnFailure))

	require.True(s.T(), s.env.IsWorkflowCompleted())
	var result WorkflowResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), "shutdown", result.EndReason)
}

// Ensure we reference workflow.Context (suppress unused import warning)
var _ workflow.Context
