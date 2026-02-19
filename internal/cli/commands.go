package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	enums "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"github.com/mfateev/temporal-agent-harness/internal/llm"
	"github.com/mfateev/temporal-agent-harness/internal/models"
	"github.com/mfateev/temporal-agent-harness/internal/workflow"
)

// harnessWorkflowID returns a stable harness workflow ID derived from the
// working directory path.
func harnessWorkflowID(cwd string) string {
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
				Provider:             config.Provider,
				Model:                config.Model,
				ApprovalMode:         config.ApprovalMode,
				SandboxMode:          config.SandboxMode,
				SandboxWritableRoots: config.SandboxWritableRoots,
				SandboxNetworkAccess: config.SandboxNetworkAccess,
				CodexHome:            config.CodexHome,
				Cwd:                  cwd,
				DisableSuggestions:   config.DisableSuggestions,
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
					Provider:             config.Provider,
					Model:                config.Model,
					ApprovalMode:         config.ApprovalMode,
					SandboxMode:          config.SandboxMode,
					SandboxWritableRoots: config.SandboxWritableRoots,
					SandboxNetworkAccess: config.SandboxNetworkAccess,
					DisableSuggestions:   config.DisableSuggestions,
					Cwd:                  cwd,
				},
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
		poller := NewPoller(c, workflowID, PollInterval)
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

		var accepted workflow.UserInputAccepted
		if err := updateHandle.Get(ctx, &accepted); err != nil {
			return UserInputErrorMsg{Err: err}
		}

		return UserInputSentMsg{TurnID: accepted.TurnID}
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
