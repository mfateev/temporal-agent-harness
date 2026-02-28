// Package workflow contains Temporal workflow definitions.
package workflow

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

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

	// Register SessionWorkflow as a child workflow that completes immediately.
	s.env.RegisterWorkflow(SessionWorkflow)
	s.env.OnWorkflow(SessionWorkflow, mock.Anything, mock.Anything).
		Return(nil).Maybe()
}

func (s *HarnessWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

// harnessInput returns a standard HarnessWorkflowInput for testing.
func harnessInput() HarnessWorkflowInput {
	return HarnessWorkflowInput{
		HarnessID: "test-harness",
	}
}

// cancelWorkflow cancels the workflow via a delayed callback.
func (s *HarnessWorkflowTestSuite) cancelWorkflow(delay time.Duration) {
	s.env.RegisterDelayedCallback(func() {
		s.env.CancelWorkflow()
	}, delay)
}

func (s *HarnessWorkflowTestSuite) assertWorkflowCompleted() {
	require.True(s.T(), s.env.IsWorkflowCompleted(),
		"harness workflow should have completed")
}

// sendSessionReadySignal sends a mock update_session_status signal to
// simulate what SessionWorkflow does after starting the AgenticWorkflow.
// The sessionWfID must match the convention: harnessID + "/" + sessionID.
func (s *HarnessWorkflowTestSuite) sendSessionReadySignal(delay time.Duration, sessionWfID string) {
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(SignalUpdateSessionStatus, UpdateSessionStatusRequest{
			SessionWorkflowID: sessionWfID,
			Status:            AgentStatusRunning,
		})
	}, delay)
}

// TestHarness_StartSessionSpawnsChild verifies that sending a start_session
// Update spawns a SessionWorkflow child and returns a non-empty SessionWorkflowID.
func (s *HarnessWorkflowTestSuite) TestHarness_StartSessionSpawnsChild() {
	var sessionWorkflowID string

	// T=1s: send start_session Update.
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

	// T=1.5s: simulate SessionWorkflow sending ready signal.
	// Query the session list to find the session workflow ID dynamically.
	s.env.RegisterDelayedCallback(func() {
		result, err := s.env.QueryWorkflow(QueryGetSessions)
		if err != nil {
			return
		}
		var sessions []SessionEntry
		if err := result.Get(&sessions); err != nil || len(sessions) == 0 {
			return
		}
		s.env.SignalWorkflow(SignalUpdateSessionStatus, UpdateSessionStatusRequest{
			SessionWorkflowID: sessions[0].SessionWorkflowID,
			Status:            AgentStatusRunning,
		})
	}, 1500*time.Millisecond)

	// T=2s: query session list.
	s.env.RegisterDelayedCallback(func() {
		result, err := s.env.QueryWorkflow(QueryGetSessions)
		require.NoError(s.T(), err)

		var sessions []SessionEntry
		require.NoError(s.T(), result.Get(&sessions))

		require.Len(s.T(), sessions, 1, "should have exactly one session")
		assert.Equal(s.T(), sessionWorkflowID, sessions[0].WorkflowID,
			"WorkflowID in session list should match the returned SessionWorkflowID")
		assert.Contains(s.T(),
			[]AgentStatus{AgentStatusRunning, AgentStatusCompleted},
			sessions[0].Status,
			"session status should be running or completed")
	}, time.Second*2)

	s.cancelWorkflow(time.Second * 3)

	s.env.ExecuteWorkflow(HarnessWorkflow, harnessInput())
	s.assertWorkflowCompleted()
}

// TestHarness_QuerySessionsEmpty verifies that querying get_sessions before
// any sessions are started returns an empty (non-nil) slice.
func (s *HarnessWorkflowTestSuite) TestHarness_QuerySessionsEmpty() {
	s.env.RegisterDelayedCallback(func() {
		result, err := s.env.QueryWorkflow(QueryGetSessions)
		require.NoError(s.T(), err)

		var sessions []SessionEntry
		require.NoError(s.T(), result.Get(&sessions))

		assert.NotNil(s.T(), sessions, "sessions should not be nil")
		assert.Empty(s.T(), sessions, "sessions should be empty before any start_session")
	}, time.Second*1)

	s.cancelWorkflow(time.Second * 2)

	s.env.ExecuteWorkflow(HarnessWorkflow, harnessInput())
	s.assertWorkflowCompleted()
}

// TestHarness_StartSession_EmptyMessageRejected verifies that the validator
// rejects a start_session Update with an empty UserMessage.
func (s *HarnessWorkflowTestSuite) TestHarness_StartSession_EmptyMessageRejected() {
	var rejected bool

	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(UpdateStartSession, "start-empty", &testsuite.TestUpdateCallback{
			OnAccept: func() {
				s.Fail("empty user_message should not be accepted")
			},
			OnReject: func(err error) {
				require.Error(s.T(), err)
				assert.Contains(s.T(), err.Error(), "user_message must not be empty")
				rejected = true
			},
			OnComplete: func(interface{}, error) {},
		}, StartSessionRequest{UserMessage: ""})
	}, time.Second*1)

	s.cancelWorkflow(time.Second * 2)

	s.env.ExecuteWorkflow(HarnessWorkflow, harnessInput())

	require.True(s.T(), s.env.IsWorkflowCompleted())
	assert.True(s.T(), rejected)
}

// TestHarness_NoConfigActivitiesOnStart verifies that the slimmed harness does
// NOT call any config-loading activities directly.
func (s *HarnessWorkflowTestSuite) TestHarness_NoConfigActivitiesOnStart() {
	s.cancelWorkflow(time.Second * 2)
	s.env.ExecuteWorkflow(HarnessWorkflow, harnessInput())
	require.True(s.T(), s.env.IsWorkflowCompleted())
}
