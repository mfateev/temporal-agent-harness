// Package activities implements Temporal activities.
//
// session.go provides the WaitForSessionReady activity used by HarnessWorkflow
// to block until a SessionWorkflow has started its AgenticWorkflow child.
package activities

import (
	"context"
	"fmt"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
)

// SessionActivities provides session-lifecycle activities.
type SessionActivities struct {
	client client.Client
}

// NewSessionActivities creates a new SessionActivities with the given Temporal client.
func NewSessionActivities(c client.Client) *SessionActivities {
	return &SessionActivities{client: c}
}

// WaitForSessionReadyInput is the input for the WaitForSessionReady activity.
type WaitForSessionReadyInput struct {
	// SessionWorkflowID is the workflow ID of the SessionWorkflow to poll.
	SessionWorkflowID string `json:"session_workflow_id"`
}

// WaitForSessionReadyOutput is the output of the WaitForSessionReady activity.
type WaitForSessionReadyOutput struct {
	// AgentWorkflowID is the workflow ID of the AgenticWorkflow started
	// by the SessionWorkflow. Empty string means the session is not ready.
	AgentWorkflowID string `json:"agent_workflow_id"`
}

// WaitForSessionReady polls the SessionWorkflow's get_agent_workflow_id query
// until it returns a non-empty agent workflow ID, indicating that the session's
// AgenticWorkflow has been started and is ready for TUI interaction.
func (a *SessionActivities) WaitForSessionReady(ctx context.Context, input WaitForSessionReadyInput) (WaitForSessionReadyOutput, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return WaitForSessionReadyOutput{}, ctx.Err()
		case <-ticker.C:
			activity.RecordHeartbeat(ctx, "polling for session readiness")

			resp, err := a.client.QueryWorkflow(ctx, input.SessionWorkflowID, "", "get_agent_workflow_id")
			if err != nil {
				// SessionWorkflow may not have started yet — retry.
				continue
			}

			var agentWfID string
			if err := resp.Get(&agentWfID); err != nil {
				continue
			}

			if agentWfID != "" {
				return WaitForSessionReadyOutput{AgentWorkflowID: agentWfID}, nil
			}
		}
	}
}

// StartSessionWorkflowInput is the input for the StartSessionWorkflow activity.
type StartSessionWorkflowInput struct {
	SessionWorkflowID string `json:"session_workflow_id"`
	TaskQueue         string `json:"task_queue"`
}

// StartSessionWorkflowOutput is the output of the StartSessionWorkflow activity.
type StartSessionWorkflowOutput struct {
	WorkflowID string `json:"workflow_id"`
	RunID      string `json:"run_id"`
}

// StartSessionWorkflow starts a SessionWorkflow as a top-level workflow.
// This is an alternative to the child workflow pattern — currently unused
// but available for future use when full CAN-safety is needed.
func (a *SessionActivities) StartSessionWorkflow(ctx context.Context, input StartSessionWorkflowInput) (StartSessionWorkflowOutput, error) {
	run, err := a.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        input.SessionWorkflowID,
		TaskQueue: input.TaskQueue,
	}, "SessionWorkflow")
	if err != nil {
		return StartSessionWorkflowOutput{}, fmt.Errorf("failed to start session workflow: %w", err)
	}
	return StartSessionWorkflowOutput{
		WorkflowID: run.GetID(),
		RunID:      run.GetRunID(),
	}, nil
}
