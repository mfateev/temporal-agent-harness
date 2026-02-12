// Package workflow contains Temporal workflow definitions.
//
// state.go manages workflow state, separated from workflow logic.
//
// Maps to: codex-rs/core/src/state/session.rs SessionState
package workflow

import (
	"github.com/mfateev/codex-temporal-go/internal/history"
	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/tools"
)

// Handler name constants for Temporal query and update handlers.
const (
	// QueryGetConversationItems returns conversation history.
	// Maps to: Codex ContextManager::raw_items()
	QueryGetConversationItems = "get_conversation_items"

	// QueryGetTurnStatus returns the current turn phase and stats.
	// Used by the interactive CLI to drive spinner/state transitions.
	QueryGetTurnStatus = "get_turn_status"

	// UpdateUserInput submits a new user message to the workflow.
	// Maps to: Codex Op::UserInput / turn/start
	UpdateUserInput = "user_input"

	// UpdateInterrupt aborts the current turn.
	// Maps to: Codex Op::Interrupt
	UpdateInterrupt = "interrupt"

	// UpdateShutdown ends the session.
	// Maps to: Codex Op::Shutdown
	UpdateShutdown = "shutdown"

	// UpdateApprovalResponse submits the user's tool approval decision.
	// Maps to: Codex approval flow (AskForApproval)
	UpdateApprovalResponse = "approval_response"

	// UpdateEscalationResponse submits the user's escalation decision (on-failure mode).
	UpdateEscalationResponse = "escalation_response"

	// UpdateUserInputQuestionResponse submits the user's answers to request_user_input questions.
	// Maps to: codex-rs/protocol/src/request_user_input.rs
	UpdateUserInputQuestionResponse = "user_input_question_response"

	// UpdateCompact triggers manual context compaction.
	UpdateCompact = "compact"

	// SignalAgentInput delivers a user message to a child agent workflow.
	// Maps to: codex-rs/core/src/agent/control.rs agent input signal
	SignalAgentInput = "agent_input"

	// SignalAgentShutdown requests a child agent workflow to shut down.
	// Maps to: codex-rs/core/src/agent/control.rs agent shutdown signal
	SignalAgentShutdown = "agent_shutdown"
)

// TurnPhase indicates the current phase of the workflow turn.
type TurnPhase string

const (
	PhaseWaitingForInput    TurnPhase = "waiting_for_input"
	PhaseLLMCalling         TurnPhase = "llm_calling"
	PhaseToolExecuting      TurnPhase = "tool_executing"
	PhaseApprovalPending    TurnPhase = "approval_pending"
	PhaseEscalationPending  TurnPhase = "escalation_pending"
	PhaseUserInputPending   TurnPhase = "user_input_pending"
	PhaseCompacting         TurnPhase = "compacting"
	PhaseWaitingForAgents   TurnPhase = "waiting_for_agents"
)

// TurnStatus is the response from the get_turn_status query.
type TurnStatus struct {
	Phase                   TurnPhase                `json:"phase"`
	CurrentTurnID           string                   `json:"current_turn_id"`
	ToolsInFlight           []string                 `json:"tools_in_flight,omitempty"`
	PendingApprovals        []PendingApproval        `json:"pending_approvals,omitempty"`
	PendingEscalations      []EscalationRequest      `json:"pending_escalations,omitempty"`
	PendingUserInputRequest *PendingUserInputRequest `json:"pending_user_input_request,omitempty"`
	IterationCount          int                      `json:"iteration_count"`
	TotalTokens             int                      `json:"total_tokens"`
	TurnCount               int                      `json:"turn_count"`
	WorkerVersion           string                   `json:"worker_version,omitempty"`
}

// WorkflowInput is the initial input to start a conversation.
//
// Maps to: codex-rs/core/src/codex.rs run_turn input
type WorkflowInput struct {
	ConversationID string                      `json:"conversation_id"`
	UserMessage    string                      `json:"user_message"`
	Config         models.SessionConfiguration `json:"config"`
	// Depth tracks subagent nesting level. 0 = top-level, 1 = child.
	// Maps to: codex-rs SubAgentSource::ThreadSpawn.depth
	Depth int `json:"depth,omitempty"`
}

// UserInput is the payload for the user_input Update.
// Maps to: codex-rs/protocol/src/user_input.rs UserInput
type UserInput struct {
	Content string `json:"content"`
}

// UserInputAccepted is returned by the user_input Update after acceptance.
// Maps to: Codex submit() return value (submission ID)
type UserInputAccepted struct {
	TurnID string `json:"turn_id"`
}

// InterruptRequest is the payload for the interrupt Update.
// Maps to: codex-rs/protocol/src/protocol.rs Op::Interrupt
type InterruptRequest struct{}

// InterruptResponse is returned by the interrupt Update.
// Maps to: Codex EventMsg::TurnAborted
type InterruptResponse struct {
	Acknowledged bool `json:"acknowledged"`
}

// ShutdownRequest is the payload for the shutdown Update.
// Maps to: codex-rs/protocol/src/protocol.rs Op::Shutdown
type ShutdownRequest struct {
	Reason string `json:"reason,omitempty"`
}

// ShutdownResponse is returned by the shutdown Update.
// Maps to: Codex EventMsg::ShutdownComplete
type ShutdownResponse struct {
	Acknowledged bool `json:"acknowledged"`
}

// PendingApproval describes a tool call awaiting user approval.
// Maps to: Codex approval flow (tool call needing confirmation)
type PendingApproval struct {
	CallID    string `json:"call_id"`
	ToolName  string `json:"tool_name"`
	Arguments string `json:"arguments"` // Raw JSON string of arguments
	Reason    string `json:"reason,omitempty"` // Why approval is needed (from policy justification or heuristic)
}

// ApprovalResponse is the user's decision on pending tool approvals.
// Maps to: Codex approval flow response
type ApprovalResponse struct {
	Approved []string `json:"approved"` // CallIDs the user approved
	Denied   []string `json:"denied"`   // CallIDs the user denied
}

// ApprovalResponseAck is returned by the approval_response Update after acceptance.
type ApprovalResponseAck struct{}

// EscalationRequest describes a failed sandboxed tool call awaiting user escalation.
// Maps to: Codex on-failure mode escalation
type EscalationRequest struct {
	CallID    string `json:"call_id"`
	ToolName  string `json:"tool_name"`
	Arguments string `json:"arguments"`
	Output    string `json:"output"`     // Failed output from sandboxed execution
	Reason    string `json:"reason"`     // Why escalation is needed
}

// EscalationResponse is the user's decision on escalation.
type EscalationResponse struct {
	Approved []string `json:"approved"` // CallIDs to re-execute without sandbox
	Denied   []string `json:"denied"`   // CallIDs to reject
}

// EscalationResponseAck is returned by the escalation_response Update.
type EscalationResponseAck struct{}

// RequestUserInputQuestionOption describes a single option for a user input question.
// Maps to: codex-rs/protocol/src/request_user_input.rs QuestionOption
type RequestUserInputQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// RequestUserInputQuestion describes a single question for the user.
// Maps to: codex-rs/protocol/src/request_user_input.rs Question
type RequestUserInputQuestion struct {
	ID       string                           `json:"id"`
	Header   string                           `json:"header,omitempty"`
	Question string                           `json:"question"`
	IsOther  bool                             `json:"is_other,omitempty"`
	Options  []RequestUserInputQuestionOption `json:"options"`
}

// PendingUserInputRequest describes a request_user_input call awaiting user response.
type PendingUserInputRequest struct {
	CallID    string                     `json:"call_id"`
	Questions []RequestUserInputQuestion `json:"questions"`
}

// UserInputQuestionAnswer holds the selected answers for a single question.
type UserInputQuestionAnswer struct {
	Answers []string `json:"answers"`
}

// UserInputQuestionResponse is the user's response to a request_user_input call.
type UserInputQuestionResponse struct {
	Answers map[string]UserInputQuestionAnswer `json:"answers"`
}

// UserInputQuestionResponseAck is returned by the user_input_question_response Update.
type UserInputQuestionResponseAck struct{}

// CompactRequest is the payload for the compact Update.
type CompactRequest struct{}

// CompactResponse is returned by the compact Update.
type CompactResponse struct {
	Acknowledged bool `json:"acknowledged"`
}

// AgentInputSignal is the payload for the agent_input signal.
// Sent from parent to child workflow via SignalExternalWorkflow.
// Maps to: codex-rs/core/src/agent/control.rs AgentInputSignal
type AgentInputSignal struct {
	Content   string `json:"content"`
	Interrupt bool   `json:"interrupt"`
}

// SessionState is passed through ContinueAsNew.
// Uses ContextManager interface to allow pluggable storage backends.
//
// Corresponds to: codex-rs/core/src/state/session.rs SessionState
type SessionState struct {
	ConversationID string                      `json:"conversation_id"`
	History        history.ContextManager      `json:"-"`             // Not serialized directly; see note below
	HistoryItems   []models.ConversationItem   `json:"history_items"` // Serialized form for ContinueAsNew
	ToolSpecs      []tools.ToolSpec            `json:"tool_specs"`
	Config         models.SessionConfiguration `json:"config"`

	// Iteration tracking
	IterationCount int `json:"iteration_count"`
	MaxIterations  int `json:"max_iterations"`

	// Multi-turn state
	PendingUserInput  bool   `json:"pending_user_input"`   // New user input waiting
	ShutdownRequested bool   `json:"shutdown_requested"`   // Session shutdown requested
	Interrupted       bool   `json:"interrupted"`          // Current turn interrupted
	CurrentTurnID     string `json:"current_turn_id"`      // Active turn ID

	// Turn phase tracking (for CLI polling)
	Phase            TurnPhase         `json:"phase"`
	ToolsInFlight    []string          `json:"tools_in_flight,omitempty"`
	PendingApprovals []PendingApproval `json:"pending_approvals,omitempty"`

	// Approval transient state (not serialized — lost on ContinueAsNew)
	ApprovalReceived bool              `json:"-"`
	ApprovalResponse *ApprovalResponse `json:"-"`

	// Escalation transient state (on-failure mode)
	PendingEscalations  []EscalationRequest  `json:"pending_escalations,omitempty"`
	EscalationReceived  bool                 `json:"-"`
	EscalationResponse  *EscalationResponse  `json:"-"`

	// User input question transient state (request_user_input interception)
	PendingUserInputReq   *PendingUserInputRequest   `json:"pending_user_input_request,omitempty"`
	UserInputQReceived    bool                       `json:"-"`
	UserInputQResponse    *UserInputQuestionResponse `json:"-"`

	// Transient: user requested manual compaction via /compact command
	CompactRequested bool `json:"-"`

	// Exec policy rules (serialized text, persists across ContinueAsNew)
	ExecPolicyRules string `json:"exec_policy_rules,omitempty"`

	// Total iterations across all turns (persists across ContinueAsNew).
	// Used to trigger ContinueAsNew when history grows too large.
	TotalIterationsForCAN int `json:"total_iterations_for_can"`

	// OpenAI Responses API: last response ID for incremental sends
	// Persists across CAN to enable chaining across workflow continuations.
	LastResponseID string `json:"last_response_id,omitempty"`

	// Transient: tracks how many history items were sent in the last LLM call,
	// enabling incremental sends (only new items after this index).
	// Reset on history modification (compaction, DropOldestUserTurns).
	lastSentHistoryLen int `json:"-"`

	// Context compaction tracking
	CompactionCount   int  `json:"compaction_count"`   // How many times compaction has occurred
	compactedThisTurn bool `json:"-"`                  // Prevents double compaction in one turn

	// Repeated tool call detection (transient — not serialized)
	lastToolKey string `json:"-"`
	repeatCount int    `json:"-"`

	// Cumulative stats (persist across ContinueAsNew)
	TotalTokens       int      `json:"total_tokens"`
	ToolCallsExecuted []string `json:"tool_calls_executed"`

	// Subagent control — manages child workflow lifecycles.
	// Maps to: codex-rs/core/src/agent/control.rs AgentControl
	AgentCtl *AgentControl `json:"agent_ctl,omitempty"`
}

// WorkflowResult is the final result of the workflow.
type WorkflowResult struct {
	ConversationID    string   `json:"conversation_id"`
	TotalIterations   int      `json:"total_iterations"`
	TotalTokens       int      `json:"total_tokens"`
	ToolCallsExecuted []string `json:"tool_calls_executed"`
	EndReason         string   `json:"end_reason,omitempty"` // "shutdown", "error"
	// FinalMessage is the last assistant message from the workflow.
	// Used by parent workflows to get the child's result.
	// Maps to: codex-rs AgentStatus::Completed(Option<String>)
	FinalMessage string `json:"final_message,omitempty"`
}

// initHistory initializes the History field from HistoryItems.
// Called after deserialization (ContinueAsNew) to restore the interface.
func (s *SessionState) initHistory() {
	h := history.NewInMemoryHistory()
	for _, item := range s.HistoryItems {
		h.AddItem(item)
	}
	s.History = h
}

// syncHistoryItems copies history to HistoryItems for serialization.
// Called before ContinueAsNew to persist state.
func (s *SessionState) syncHistoryItems() {
	items, _ := s.History.GetRawItems()
	s.HistoryItems = items
}
