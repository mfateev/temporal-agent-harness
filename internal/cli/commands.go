package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
	"go.temporal.io/sdk/client"

	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/workflow"
)

// startWorkflowCmd starts a new workflow and returns WorkflowStartedMsg.
func startWorkflowCmd(c client.Client, config Config) tea.Cmd {
	return func() tea.Msg {
		workflowID := fmt.Sprintf("codex-%s", uuid.New().String()[:8])

		cwd := config.Cwd
		if cwd == "" {
			cwd, _ = os.Getwd()
		}

		input := workflow.WorkflowInput{
			ConversationID: workflowID,
			UserMessage:    config.Message,
			Config: models.SessionConfiguration{
				Model: models.ModelConfig{
					Provider:      config.Provider,
					Model:         config.Model,
					Temperature:   0.7,
					MaxTokens:     4096,
					ContextWindow: 128000,
				},
				Tools:                    models.DefaultToolsConfig(),
				AutoCompactTokenLimit:    128000 * 4 / 5, // 80% of context window
				ApprovalMode:             config.ApprovalMode,
				CodexHome:                config.CodexHome,
				SandboxMode:              config.SandboxMode,
				SandboxWritableRoots:     config.SandboxWritableRoots,
				SandboxNetworkAccess:     config.SandboxNetworkAccess,
				Cwd:                      cwd,
				SessionSource:            "interactive-cli",
				CLIProjectDocs:           config.CLIProjectDocs,
				UserPersonalInstructions: config.UserPersonalInstructions,
			},
		}

		ctx := context.Background()
		_, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: TaskQueue,
		}, "AgenticWorkflow", input)
		if err != nil {
			return WorkflowStartErrorMsg{Err: fmt.Errorf("failed to start workflow: %w", err)}
		}

		return WorkflowStartedMsg{
			WorkflowID: workflowID,
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
