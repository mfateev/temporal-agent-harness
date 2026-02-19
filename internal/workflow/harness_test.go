// Package workflow contains Temporal workflow definitions.
package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"

	"github.com/mfateev/temporal-agent-harness/internal/activities"
)

// Stub activity functions for the manager test environment.
// These are never called directly — OnActivity mocks override them —
// but they must be registered so the test env recognises the activity names.
// The function names must match the string names used in workflow.ExecuteActivity calls.

func LoadWorkerInstructions(_ context.Context, _ activities.LoadWorkerInstructionsInput) (activities.LoadWorkerInstructionsOutput, error) {
	panic("stub: should be mocked")
}

func LoadExecPolicy(_ context.Context, _ activities.LoadExecPolicyInput) (activities.LoadExecPolicyOutput, error) {
	panic("stub: should be mocked")
}

func LoadPersonalInstructions(_ context.Context, _ activities.LoadPersonalInstructionsInput) (activities.LoadPersonalInstructionsOutput, error) {
	panic("stub: should be mocked")
}

// HarnessWorkflowTestSuite runs HarnessWorkflow tests with the Temporal test environment.
type HarnessWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func TestHarnessWorkflowSuite(t *testing.T) {
	suite.Run(t, new(HarnessWorkflowTestSuite))
}

func (s *HarnessWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()

	// Register stub activity functions so the test env recognises the activity names.
	s.env.RegisterActivity(LoadWorkerInstructions)
	s.env.RegisterActivity(LoadExecPolicy)
	s.env.RegisterActivity(LoadPersonalInstructions)

	// Default mock for LoadWorkerInstructions — returns empty docs.
	s.env.OnActivity("LoadWorkerInstructions", mock.Anything, mock.Anything).
		Return(activities.LoadWorkerInstructionsOutput{}, nil).Maybe()

	// Default mock for LoadExecPolicy — returns empty rules.
	s.env.OnActivity("LoadExecPolicy", mock.Anything, mock.Anything).
		Return(activities.LoadExecPolicyOutput{}, nil).Maybe()

	// Default mock for LoadPersonalInstructions — returns empty instructions.
	s.env.OnActivity("LoadPersonalInstructions", mock.Anything, mock.Anything).
		Return(activities.LoadPersonalInstructionsOutput{}, nil).Maybe()

	// Register AgenticWorkflow as a child workflow that completes immediately.
	s.env.RegisterWorkflow(AgenticWorkflow)
	s.env.OnWorkflow(AgenticWorkflow, mock.Anything, mock.Anything).
		Return(WorkflowResult{}, nil).Maybe()
}

func (s *HarnessWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

// managerInput returns a standard HarnessWorkflowInput for testing.
func managerInput() HarnessWorkflowInput {
	return HarnessWorkflowInput{
		ManagerID: "test-manager",
	}
}

// cancelWorkflow cancels the workflow via a delayed callback to terminate the
// manager's infinite loop. The manager loops until either cancelled or a
// ContinueAsNew timeout fires; cancellation is the simplest way to stop it
// in tests.
func (s *HarnessWorkflowTestSuite) cancelWorkflow(delay time.Duration) {
	s.env.RegisterDelayedCallback(func() {
		s.env.CancelWorkflow()
	}, delay)
}

// assertWorkflowCompleted verifies the workflow completed (regardless of reason).
// The manager's infinite loop may complete via cancellation or idle timeout.
func (s *HarnessWorkflowTestSuite) assertWorkflowCompleted() {
	require.True(s.T(), s.env.IsWorkflowCompleted(),
		"manager workflow should have completed")
}

// TestManager_StartSessionSpawnsChild verifies that sending a start_session
// Update spawns a child workflow and returns a non-empty SessionWorkflowID.
// It also queries get_sessions to confirm the session is recorded.
func (s *HarnessWorkflowTestSuite) TestManager_StartSessionSpawnsChild() {
	var sessionWorkflowID string

	// After activities resolve (~1s), send a start_session Update.
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateStartSession, "start-1", &testsuite.TestUpdateCallback{
			OnAccept: func() {},
			OnReject: func(err error) {
				s.Fail("start_session should not be rejected", err.Error())
			},
			OnComplete: func(result interface{}, err error) {
				require.NoError(s.T(), err)
				resp, ok := result.(StartSessionResponse)
				require.True(s.T(), ok, "result should be StartSessionResponse")
				assert.NotEmpty(s.T(), resp.SessionWorkflowID, "SessionWorkflowID must not be empty")
				assert.NotEmpty(s.T(), resp.SessionID, "SessionID must not be empty")
				sessionWorkflowID = resp.SessionWorkflowID
			},
		}, StartSessionRequest{UserMessage: "hello"})
	}, time.Second*1)

	// After the Update is processed, query the session list.
	s.env.RegisterDelayedCallback(func() {
		result, err := s.env.QueryWorkflow(QueryGetSessions)
		require.NoError(s.T(), err)

		var sessions []SessionEntry
		require.NoError(s.T(), result.Get(&sessions))

		require.Len(s.T(), sessions, 1, "should have exactly one session")
		assert.Equal(s.T(), sessionWorkflowID, sessions[0].WorkflowID,
			"WorkflowID in session list should match the returned SessionWorkflowID")
		// Status is either Running (if the child goroutine hasn't completed yet)
		// or Completed (if the mock child returned immediately).
		assert.Contains(s.T(),
			[]AgentStatus{AgentStatusRunning, AgentStatusCompleted},
			sessions[0].Status,
			"session status should be running or completed")
	}, time.Second*2)

	// Cancel the workflow to terminate the manager's idle loop.
	s.cancelWorkflow(time.Second * 3)

	s.env.ExecuteWorkflow(HarnessWorkflow, managerInput())

	s.assertWorkflowCompleted()
}

// TestManager_QuerySessionsEmpty verifies that querying get_sessions before
// any sessions are started returns an empty (non-nil) slice.
func (s *HarnessWorkflowTestSuite) TestManager_QuerySessionsEmpty() {
	s.env.RegisterDelayedCallback(func() {
		result, err := s.env.QueryWorkflow(QueryGetSessions)
		require.NoError(s.T(), err)

		var sessions []SessionEntry
		require.NoError(s.T(), result.Get(&sessions))

		// Must not be nil (query handler returns []SessionEntry{}) and must be empty.
		assert.NotNil(s.T(), sessions, "sessions should not be nil")
		assert.Empty(s.T(), sessions, "sessions should be empty before any start_session")
	}, time.Second*1)

	// Cancel the workflow to terminate the manager's idle loop.
	s.cancelWorkflow(time.Second * 2)

	s.env.ExecuteWorkflow(HarnessWorkflow, managerInput())

	s.assertWorkflowCompleted()
}

// TestManager_StartSession_EmptyMessageRejected verifies that the validator
// rejects a start_session Update with an empty UserMessage.
func (s *HarnessWorkflowTestSuite) TestManager_StartSession_EmptyMessageRejected() {
	var rejected bool

	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateStartSession, "start-empty", &testsuite.TestUpdateCallback{
			OnAccept: func() {
				s.Fail("empty user_message should not be accepted")
			},
			OnReject: func(err error) {
				require.Error(s.T(), err)
				assert.Contains(s.T(), err.Error(), "user_message must not be empty",
					"rejection error should mention user_message")
				rejected = true
			},
			OnComplete: func(interface{}, error) {},
		}, StartSessionRequest{UserMessage: ""})
	}, time.Second*1)

	// Cancel the workflow to terminate the manager's idle loop.
	s.cancelWorkflow(time.Second * 2)

	s.env.ExecuteWorkflow(HarnessWorkflow, managerInput())

	require.True(s.T(), s.env.IsWorkflowCompleted())
	assert.True(s.T(), rejected, "empty user_message Update should have been rejected")
}

// TestManager_ActivityCallsOnStart verifies that all three config-loading
// activities are called exactly once when the manager starts.
// LoadExecPolicy is only called when CodexHome is non-empty; with default
// (empty) overrides it is skipped entirely.
func (s *HarnessWorkflowTestSuite) TestManager_ActivityCallsOnStart() {
	// Track activity invocations by name using the started listener.
	callCounts := map[string]int{}
	s.env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		callCounts[info.ActivityType.Name]++
	})

	// Cancel the workflow to terminate the manager's idle loop.
	s.cancelWorkflow(time.Second * 2)

	s.env.ExecuteWorkflow(HarnessWorkflow, managerInput())

	require.True(s.T(), s.env.IsWorkflowCompleted())

	assert.Equal(s.T(), 1, callCounts["LoadWorkerInstructions"],
		"LoadWorkerInstructions should be called exactly once on start")
	assert.Equal(s.T(), 1, callCounts["LoadPersonalInstructions"],
		"LoadPersonalInstructions should be called exactly once on start")
	// LoadExecPolicy is skipped when CodexHome is empty (the default).
	assert.Equal(s.T(), 0, callCounts["LoadExecPolicy"],
		"LoadExecPolicy should not be called when CodexHome is empty")
}
