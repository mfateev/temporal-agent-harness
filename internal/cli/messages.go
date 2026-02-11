package cli

import (
	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/workflow"
)

// WorkflowStartedMsg is sent when a workflow has been started or resumed.
type WorkflowStartedMsg struct {
	WorkflowID string
	Items      []models.ConversationItem // Non-nil only for resume
	Status     workflow.TurnStatus       // Non-zero only for resume
	IsResume   bool
}

// WorkflowStartErrorMsg is sent when starting/resuming a workflow fails.
type WorkflowStartErrorMsg struct {
	Err error
}

// PollResultMsg wraps a PollResult from the polling goroutine.
type PollResultMsg struct {
	Result PollResult
}

// UserInputSentMsg is sent after user input has been successfully sent.
type UserInputSentMsg struct {
	TurnID string
}

// UserInputErrorMsg is sent when sending user input fails.
type UserInputErrorMsg struct {
	Err error
}

// InterruptSentMsg is sent after an interrupt has been successfully sent.
type InterruptSentMsg struct{}

// InterruptErrorMsg is sent when sending an interrupt fails.
type InterruptErrorMsg struct {
	Err error
}

// ShutdownSentMsg is sent after a shutdown has been successfully sent.
type ShutdownSentMsg struct{}

// ShutdownErrorMsg is sent when sending a shutdown fails.
type ShutdownErrorMsg struct {
	Err error
}

// ApprovalSentMsg is sent after an approval response has been sent.
type ApprovalSentMsg struct{}

// ApprovalErrorMsg is sent when sending an approval response fails.
type ApprovalErrorMsg struct {
	Err error
}

// EscalationSentMsg is sent after an escalation response has been sent.
type EscalationSentMsg struct{}

// EscalationErrorMsg is sent when sending an escalation response fails.
type EscalationErrorMsg struct {
	Err error
}

// SessionCompletedMsg is sent when the workflow completes.
type SessionCompletedMsg struct {
	Result *workflow.WorkflowResult // nil if unavailable
}

// SessionErrorMsg is sent when the workflow encounters an unrecoverable error.
type SessionErrorMsg struct {
	Err error
}

// UserInputQuestionSentMsg is sent after a user input question response has been sent.
type UserInputQuestionSentMsg struct{}

// UserInputQuestionErrorMsg is sent when sending a user input question response fails.
type UserInputQuestionErrorMsg struct {
	Err error
}
