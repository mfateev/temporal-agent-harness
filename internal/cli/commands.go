package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	enums "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"

	"github.com/mfateev/temporal-agent-harness/internal/llm"
	"github.com/mfateev/temporal-agent-harness/internal/models"
	"github.com/mfateev/temporal-agent-harness/internal/skills"
	"github.com/mfateev/temporal-agent-harness/internal/workflow"
)

// harnessWorkflowID returns a stable harness workflow ID derived from the
// working directory path. If TCX_HARNESS_ID is set, it is used directly
// (enables tests to predict the workflow ID for monitoring).
func harnessWorkflowID(cwd string) string {
	if id := os.Getenv("TCX_HARNESS_ID"); id != "" {
		return id
	}
	h := sha256.New()
	h.Write([]byte(cwd))
	return fmt.Sprintf("harness-%x", h.Sum(nil)[:8])
}

// startWorkflowCmd starts (or re-attaches to) a HarnessWorkflow and sends a
// start_session Update to obtain a child AgenticWorkflow ID. It returns
// WorkflowStartedMsg with the child session workflow ID so all subsequent TUI
// operations target the AgenticWorkflow directly.
func startWorkflowCmd(c client.Client, config Config) tea.Cmd {
	return func() tea.Msg {
		cwd := config.Cwd
		if cwd == "" {
			cwd, _ = os.Getwd()
		}

		harnessID := harnessWorkflowID(cwd)

		input := workflow.HarnessWorkflowInput{
			HarnessID: harnessID,
			Overrides: workflow.CLIOverrides{
				Provider:           config.Provider,
				Model:              config.Model,
				Permissions:        config.Permissions,
				CodexHome:          config.CodexHome,
				Cwd:                cwd,
				DisableSuggestions: config.DisableSuggestions,
				MemoryEnabled:      config.MemoryEnabled,
				MemoryDbPath:       config.MemoryDbPath,
			},
		}

		ctx := context.Background()
		_, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:                    harnessID,
			TaskQueue:             TaskQueue,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		}, "HarnessWorkflow", input)
		if err != nil {
			return WorkflowStartErrorMsg{Err: fmt.Errorf("failed to start harness workflow: %w", err)}
		}

		updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID: harnessID,
			UpdateName: workflow.UpdateStartSession,
			Args: []interface{}{workflow.StartSessionRequest{
				UserMessage: config.Message,
				// Pass per-invocation overrides so each session gets its own
				// model/approval/sandbox config, even when multiple tcx processes
				// share the same long-lived HarnessWorkflow.
				OverrideConfig: &workflow.CLIOverrides{
					Provider:           config.Provider,
					Model:              config.Model,
					Permissions:        config.Permissions,
					DisableSuggestions: config.DisableSuggestions,
					MemoryEnabled:      config.MemoryEnabled,
					MemoryDbPath:       config.MemoryDbPath,
					Cwd:                cwd,
				},
				CrewAgents:    config.CrewAgents,
				CrewMainAgent: config.CrewMainAgent,
				CrewType:      config.CrewType,
			}},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		})
		if err != nil {
			return WorkflowStartErrorMsg{Err: fmt.Errorf("failed to send start_session update: %w", err)}
		}

		var resp workflow.StartSessionResponse
		if err := updateHandle.Get(ctx, &resp); err != nil {
			return WorkflowStartErrorMsg{Err: fmt.Errorf("start_session update failed: %w", err)}
		}

		return WorkflowStartedMsg{
			WorkflowID: resp.SessionWorkflowID,
			IsResume:   false,
		}
	}
}

// resumeWorkflowCmd resumes an existing workflow and returns its current state.
func resumeWorkflowCmd(c client.Client, workflowID string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		poller := NewPoller(c, workflowID, 0)
		result := poller.Poll(ctx)
		if result.Err != nil {
			return WorkflowStartErrorMsg{Err: fmt.Errorf("failed to query workflow: %w", result.Err)}
		}

		return WorkflowStartedMsg{
			WorkflowID: workflowID,
			Items:      result.Items,
			Status:     result.Status,
			IsResume:   true,
		}
	}
}

// sendUserInputCmd sends user input to the workflow.
func sendUserInputCmd(c client.Client, workflowID, content string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   workflowID,
			UpdateName:   workflow.UpdateUserInput,
			Args:         []interface{}{workflow.UserInput{Content: content}},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		})
		if err != nil {
			return UserInputErrorMsg{Err: err}
		}

		var resp workflow.StateUpdateResponse
		if err := updateHandle.Get(ctx, &resp); err != nil {
			return UserInputErrorMsg{Err: err}
		}

		return UserInputSentMsg{Response: resp}
	}
}

// sendInterruptCmd sends an interrupt signal to the workflow.
func sendInterruptCmd(c client.Client, workflowID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   workflowID,
			UpdateName:   workflow.UpdateInterrupt,
			Args:         []interface{}{workflow.InterruptRequest{}},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		})
		if err != nil {
			return InterruptErrorMsg{Err: err}
		}

		var resp workflow.InterruptResponse
		if err := updateHandle.Get(ctx, &resp); err != nil {
			return InterruptErrorMsg{Err: err}
		}

		return InterruptSentMsg{}
	}
}

// sendShutdownCmd sends a shutdown signal to the workflow.
func sendShutdownCmd(c client.Client, workflowID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   workflowID,
			UpdateName:   workflow.UpdateShutdown,
			Args:         []interface{}{workflow.ShutdownRequest{}},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		})
		if err != nil {
			return ShutdownErrorMsg{Err: err}
		}

		var resp workflow.ShutdownResponse
		if err := updateHandle.Get(ctx, &resp); err != nil {
			return ShutdownErrorMsg{Err: err}
		}

		return ShutdownSentMsg{}
	}
}

// sendApprovalResponseCmd sends an approval response to the workflow.
func sendApprovalResponseCmd(c client.Client, workflowID string, resp workflow.ApprovalResponse) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   workflowID,
			UpdateName:   workflow.UpdateApprovalResponse,
			Args:         []interface{}{resp},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		})
		if err != nil {
			return ApprovalErrorMsg{Err: err}
		}

		var ack workflow.ApprovalResponseAck
		if err := updateHandle.Get(ctx, &ack); err != nil {
			return ApprovalErrorMsg{Err: err}
		}

		return ApprovalSentMsg{}
	}
}

// sendEscalationResponseCmd sends an escalation response to the workflow.
func sendEscalationResponseCmd(c client.Client, workflowID string, resp workflow.EscalationResponse) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   workflowID,
			UpdateName:   workflow.UpdateEscalationResponse,
			Args:         []interface{}{resp},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		})
		if err != nil {
			return EscalationErrorMsg{Err: err}
		}

		var ack workflow.EscalationResponseAck
		if err := updateHandle.Get(ctx, &ack); err != nil {
			return EscalationErrorMsg{Err: err}
		}

		return EscalationSentMsg{}
	}
}

// sendUserInputQuestionResponseCmd sends a user input question response to the workflow.
func sendUserInputQuestionResponseCmd(c client.Client, workflowID string, resp workflow.UserInputQuestionResponse) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   workflowID,
			UpdateName:   workflow.UpdateUserInputQuestionResponse,
			Args:         []interface{}{resp},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		})
		if err != nil {
			return UserInputQuestionErrorMsg{Err: err}
		}

		var ack workflow.UserInputQuestionResponseAck
		if err := updateHandle.Get(ctx, &ack); err != nil {
			return UserInputQuestionErrorMsg{Err: err}
		}

		return UserInputQuestionSentMsg{}
	}
}

// sendCompactCmd sends a compact request to the workflow.
func sendCompactCmd(c client.Client, workflowID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   workflowID,
			UpdateName:   workflow.UpdateCompact,
			Args:         []interface{}{workflow.CompactRequest{}},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		})
		if err != nil {
			return CompactErrorMsg{Err: err}
		}

		var resp workflow.CompactResponse
		if err := updateHandle.Get(ctx, &resp); err != nil {
			return CompactErrorMsg{Err: err}
		}

		return CompactSentMsg{}
	}
}

// sendPlanRequestCmd sends a plan_request Update to the parent workflow, which
// spawns a planner child workflow and returns its workflow ID.
func sendPlanRequestCmd(c client.Client, workflowID, message string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   workflowID,
			UpdateName:   workflow.UpdatePlanRequest,
			Args:         []interface{}{workflow.PlanRequest{Message: message}},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		})
		if err != nil {
			return PlanRequestErrorMsg{Err: err}
		}

		var accepted workflow.PlanRequestAccepted
		if err := updateHandle.Get(ctx, &accepted); err != nil {
			return PlanRequestErrorMsg{Err: err}
		}

		return PlanRequestAcceptedMsg{
			AgentID:    accepted.AgentID,
			WorkflowID: accepted.WorkflowID,
		}
	}
}

// sendUpdateModelCmd sends an update_model Update to the workflow.
func sendUpdateModelCmd(c client.Client, workflowID, provider, model string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   workflowID,
			UpdateName:   workflow.UpdateModel,
			Args:         []interface{}{workflow.UpdateModelRequest{Provider: provider, Model: model}},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		})
		if err != nil {
			return ModelUpdateErrorMsg{Err: err}
		}

		var resp workflow.UpdateModelResponse
		if err := updateHandle.Get(ctx, &resp); err != nil {
			return ModelUpdateErrorMsg{Err: err}
		}

		return ModelUpdateSentMsg{Provider: provider, Model: model}
	}
}

// startNewSessionCmd sends a start_session Update to the harness workflow to
// spawn a new child AgenticWorkflow. Returns NewSessionStartedMsg on success.
func startNewSessionCmd(c client.Client, harnessID, message string, config Config) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		cwd := config.Cwd
		if cwd == "" {
			cwd, _ = os.Getwd()
		}

		updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID: harnessID,
			UpdateName: workflow.UpdateStartSession,
			Args: []interface{}{workflow.StartSessionRequest{
				UserMessage: message,
				OverrideConfig: &workflow.CLIOverrides{
					Provider:           config.Provider,
					Model:              config.Model,
					Permissions:        config.Permissions,
					DisableSuggestions: config.DisableSuggestions,
					MemoryEnabled:      config.MemoryEnabled,
					MemoryDbPath:       config.MemoryDbPath,
					Cwd:                cwd,
				},
				CrewAgents:    config.CrewAgents,
				CrewMainAgent: config.CrewMainAgent,
				CrewType:      config.CrewType,
			}},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		})
		if err != nil {
			return NewSessionErrorMsg{Err: fmt.Errorf("failed to send start_session: %w", err)}
		}

		var resp workflow.StartSessionResponse
		if err := updateHandle.Get(ctx, &resp); err != nil {
			return NewSessionErrorMsg{Err: fmt.Errorf("start_session failed: %w", err)}
		}

		return NewSessionStartedMsg{WorkflowID: resp.SessionWorkflowID}
	}
}

// sendUpdatePersonalityCmd sends an update_personality Update to the workflow.
func sendUpdatePersonalityCmd(c client.Client, workflowID, personality string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   workflowID,
			UpdateName:   workflow.UpdatePersonality,
			Args:         []interface{}{workflow.UpdatePersonalityRequest{Personality: personality}},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		})
		if err != nil {
			return PersonalityUpdateErrorMsg{Err: err}
		}

		var resp workflow.UpdatePersonalityResponse
		if err := updateHandle.Get(ctx, &resp); err != nil {
			return PersonalityUpdateErrorMsg{Err: err}
		}

		return PersonalityUpdateSentMsg{Personality: personality}
	}
}

// sendUpdateApprovalModeCmd sends an update_approval_mode Update to the workflow.
func sendUpdateApprovalModeCmd(c client.Client, workflowID, mode string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   workflowID,
			UpdateName:   workflow.UpdateApprovalMode,
			Args:         []interface{}{workflow.UpdateApprovalModeRequest{ApprovalMode: mode}},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		})
		if err != nil {
			return ApprovalModeUpdateErrorMsg{Err: err}
		}

		var resp workflow.UpdateApprovalModeResponse
		if err := updateHandle.Get(ctx, &resp); err != nil {
			return ApprovalModeUpdateErrorMsg{Err: err}
		}

		return ApprovalModeUpdateSentMsg{Mode: mode}
	}
}

// queryMcpToolsCmd queries the workflow for its MCP tool lookup table.
func queryMcpToolsCmd(c client.Client, workflowID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		resp, err := c.QueryWorkflow(ctx, workflowID, "", workflow.QueryGetMcpTools)
		if err != nil {
			return McpToolsErrorMsg{Err: err}
		}

		var tools []workflow.McpToolSummary
		if err := resp.Get(&tools); err != nil {
			return McpToolsErrorMsg{Err: err}
		}

		return McpToolsResultMsg{Tools: tools}
	}
}

// queryExecSessionsCmd sends a list_exec_sessions Update to the workflow.
func queryExecSessionsCmd(c client.Client, workflowID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   workflowID,
			UpdateName:   workflow.UpdateListExecSessions,
			Args:         []interface{}{workflow.ListExecSessionsRequest{}},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		})
		if err != nil {
			return ExecSessionsErrorMsg{Err: err}
		}

		var resp workflow.ListExecSessionsResponse
		if err := updateHandle.Get(ctx, &resp); err != nil {
			return ExecSessionsErrorMsg{Err: err}
		}

		return ExecSessionsResultMsg{Sessions: resp.Sessions}
	}
}

// cleanExecSessionsCmd sends a clean_exec_sessions Update to the workflow.
func cleanExecSessionsCmd(c client.Client, workflowID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   workflowID,
			UpdateName:   workflow.UpdateCleanExecSessions,
			Args:         []interface{}{workflow.CleanExecSessionsRequest{}},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		})
		if err != nil {
			return CleanExecSessionsErrorMsg{Err: err}
		}

		var resp workflow.CleanExecSessionsResponse
		if err := updateHandle.Get(ctx, &resp); err != nil {
			return CleanExecSessionsErrorMsg{Err: err}
		}

		return CleanExecSessionsResultMsg{Closed: resp.Closed}
	}
}

// queryChildConversationItems queries a child workflow's conversation items
// and extracts the last assistant message (the plan text).
func queryChildConversationItems(c client.Client, childWorkflowID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		resp, err := c.QueryWorkflow(ctx, childWorkflowID, "", workflow.QueryGetConversationItems)
		if err != nil {
			return PlannerCompletedMsg{PlanText: ""}
		}

		var items []models.ConversationItem
		if err := resp.Get(&items); err != nil {
			return PlannerCompletedMsg{PlanText: ""}
		}

		// Extract the last assistant message as the plan
		for i := len(items) - 1; i >= 0; i-- {
			if items[i].Type == models.ItemTypeAssistantMessage && items[i].Content != "" {
				return PlannerCompletedMsg{PlanText: items[i].Content}
			}
		}

		return PlannerCompletedMsg{PlanText: ""}
	}
}

// fetchModelsCmd fetches the list of available models from all configured
// providers and returns a ModelsFetchedMsg. Uses a 10-second timeout.
func fetchModelsCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		available, err := llm.FetchAvailableModels(ctx)
		if err != nil {
			return ModelsFetchedMsg{Err: err}
		}
		if available == nil {
			return ModelsFetchedMsg{} // nil Models signals fallback
		}

		opts := make([]modelOption, 0, len(available))
		for _, m := range available {
			opts = append(opts, modelOption{
				Provider:    m.Provider,
				Model:       m.ID,
				DisplayName: m.DisplayName,
			})
		}
		return ModelsFetchedMsg{Models: opts}
	}
}

// fetchSessionsCmd lists sessions for the session picker via the Temporal
// visibility API. This is fast and works even without a running harness.
func fetchSessionsCmd(c client.Client, harnessID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		query := fmt.Sprintf(
			`WorkflowType = 'AgenticWorkflow' AND WorkflowId STARTS_WITH '%s/' AND ExecutionStatus = 'Running'`,
			harnessID,
		)
		resp, err := c.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Query:    query,
			PageSize: 10,
		})
		if err != nil {
			return HarnessSessionsListMsg{Err: err}
		}

		var entries []SessionListEntry
		for _, exec := range resp.GetExecutions() {
			if exec.GetExecution() == nil {
				continue
			}
			entries = append(entries, SessionListEntry{
				WorkflowID: exec.GetExecution().GetWorkflowId(),
				StartTime:  exec.GetStartTime().AsTime(),
				Status:     mapWorkflowStatus(exec.GetStatus()),
			})
		}
		return HarnessSessionsListMsg{Entries: entries}
	}
}

// mapWorkflowStatus converts a Temporal WorkflowExecutionStatus enum to a
// human-readable string for display in the session picker.
func mapWorkflowStatus(status enums.WorkflowExecutionStatus) string {
	switch status {
	case enums.WORKFLOW_EXECUTION_STATUS_RUNNING:
		return "running"
	case enums.WORKFLOW_EXECUTION_STATUS_COMPLETED:
		return "completed"
	case enums.WORKFLOW_EXECUTION_STATUS_FAILED:
		return "failed"
	case enums.WORKFLOW_EXECUTION_STATUS_CANCELED:
		return "canceled"
	case enums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		return "timed_out"
	case enums.WORKFLOW_EXECUTION_STATUS_TERMINATED:
		return "terminated"
	default:
		return "unknown"
	}
}

// waitForCompletionCmd waits for a workflow to complete after shutdown.
func waitForCompletionCmd(c client.Client, workflowID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		run := c.GetWorkflow(ctx, workflowID, "")
		var result workflow.WorkflowResult
		if err := run.Get(ctx, &result); err != nil {
			return SessionErrorMsg{Err: err}
		}

		return SessionCompletedMsg{Result: &result}
	}
}

// querySkillsCmd queries the workflow for the list of discovered skills.
func querySkillsCmd(c client.Client, workflowID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		resp, err := c.QueryWorkflow(ctx, workflowID, "", workflow.QueryListSkills)
		if err != nil {
			return SkillsListErrorMsg{Err: err}
		}

		var skillsList []skills.SkillMetadata
		if err := resp.Get(&skillsList); err != nil {
			return SkillsListErrorMsg{Err: err}
		}

		return SkillsListResultMsg{Skills: skillsList}
	}
}

// sendToggleSkillCmd sends a toggle_skill Update to the workflow.
func sendToggleSkillCmd(c client.Client, workflowID, skillPath string, enabled bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   workflowID,
			UpdateName:   workflow.UpdateToggleSkill,
			Args:         []interface{}{workflow.ToggleSkillRequest{SkillPath: skillPath, Enabled: enabled}},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		})
		if err != nil {
			return SkillToggleErrorMsg{Err: err}
		}

		var resp workflow.ToggleSkillResponse
		if err := updateHandle.Get(ctx, &resp); err != nil {
			return SkillToggleErrorMsg{Err: err}
		}

		return SkillToggleSentMsg{SkillPath: skillPath, Enabled: enabled}
	}
}

// sendUpdateReasoningEffortCmd sends an update_reasoning_effort Update to the workflow.
func sendUpdateReasoningEffortCmd(c client.Client, workflowID, effort string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   workflowID,
			UpdateName:   workflow.UpdateReasoningEffort,
			Args:         []interface{}{workflow.UpdateReasoningEffortRequest{Effort: effort}},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		})
		if err != nil {
			return ReasoningEffortUpdateErrorMsg{Err: err}
		}

		var resp workflow.UpdateReasoningEffortResponse
		if err := updateHandle.Get(ctx, &resp); err != nil {
			return ReasoningEffortUpdateErrorMsg{Err: err}
		}

		return ReasoningEffortUpdateSentMsg{Effort: resp.Effort}
	}
}

// sendSetSessionNameCmd sends a set_session_name Update to the workflow.
func sendSetSessionNameCmd(c client.Client, workflowID, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		updateHandle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   workflowID,
			UpdateName:   workflow.UpdateSessionName,
			Args:         []interface{}{workflow.SetSessionNameRequest{Name: name}},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		})
		if err != nil {
			return SessionNameErrorMsg{Err: err}
		}

		var resp workflow.SetSessionNameResponse
		if err := updateHandle.Get(ctx, &resp); err != nil {
			return SessionNameErrorMsg{Err: err}
		}

		return SessionNameSentMsg{Name: name}
	}
}
