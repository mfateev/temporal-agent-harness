// Package workflow contains Temporal workflow definitions.
//
// state.go manages workflow state, separated from workflow logic.
//
// Maps to: codex-rs/core/src/state/session.rs SessionState
package workflow

import (
	"github.com/mfateev/temporal-agent-harness/internal/history"
	"github.com/mfateev/temporal-agent-harness/internal/models"
	"github.com/mfateev/temporal-agent-harness/internal/tools"
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

	// UpdatePlanRequest spawns a planner child workflow directly (no LLM round-trip).
	// The CLI sends this when the user types /plan <message>.
	UpdatePlanRequest = "plan_request"

	// UpdateModel updates the session's model configuration.
	// Used by the CLI /model command.
	UpdateModel = "update_model"
)

// UpdateModelRequest is the payload for the update_model Update.
type UpdateModelRequest struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	ContextWindow int    `json:"context_window,omitempty"` // Explicit context window override; 0 = resolve from profile
}

// UpdateModelResponse is returned by the update_model Update.
type UpdateModelResponse struct {
	Acknowledged bool `json:"acknowledged"`
}

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
	ChildAgents             []ChildAgentSummary      `json:"child_agents,omitempty"`
	IterationCount          int                      `json:"iteration_count"`
	TotalTokens             int                      `json:"total_tokens"`
	TotalCachedTokens       int                      `json:"total_cached_tokens"`
	TurnCount               int                      `json:"turn_count"`
	WorkerVersion           string                   `json:"worker_version,omitempty"`
	Suggestion              string                   `json:"suggestion,omitempty"`
	Plan                    *PlanState               `json:"plan,omitempty"`
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

// PlanRequest is the payload for the plan_request Update.
// Sent by the CLI when the user types /plan <message>.
type PlanRequest struct {
	Message string `json:"message"`
}

// PlanRequestAccepted is returned by the plan_request Update after the planner
// child workflow has been started. Contains the child's workflow ID so the CLI
// can communicate with it directly.
type PlanRequestAccepted struct {
	AgentID    string `json:"agent_id"`
	WorkflowID string `json:"workflow_id"`
}

// ChildAgentSummary is a lightweight view of a child agent for the get_turn_status query.
type ChildAgentSummary struct {
	AgentID    string      `json:"agent_id"`
	WorkflowID string     `json:"workflow_id"`
	Role       AgentRole   `json:"role"`
	Status     AgentStatus `json:"status"`
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
// SessionState holds only serializable agent state: history, config, tool specs,
// exec policy, cumulative stats, and ContinueAsNew counters. All Temporal
// coordination state (phase, response slots, user input flags) lives in
// LoopControl, which is constructed fresh each workflow run.
//
// Corresponds to: codex-rs/core/src/state/session.rs SessionState
type SessionState struct {
	ConversationID string                      `json:"conversation_id"`
	History        history.ContextManager      `json:"-"`             // Not serialized directly; see note below
	HistoryItems   []models.ConversationItem   `json:"history_items"` // Serialized form for ContinueAsNew
	ToolSpecs       []tools.ToolSpec            `json:"tool_specs"`
	Config          models.SessionConfiguration `json:"config"`
	ResolvedProfile models.ResolvedProfile      `json:"resolved_profile"`

	// Iteration tracking
	IterationCount int `json:"iteration_count"`
	MaxIterations  int `json:"max_iterations"`

	// Exec policy rules (serialized text, persists across ContinueAsNew)
	ExecPolicyRules string `json:"exec_policy_rules,omitempty"`

	// Total iterations across all turns (persists across ContinueAsNew).
	// Used to trigger ContinueAsNew when history grows too large.
	TotalIterationsForCAN int `json:"total_iterations_for_can"`

	// OpenAI Responses API: last response ID for incremental sends.
	// Persists across CAN to enable chaining across workflow continuations.
	LastResponseID string `json:"last_response_id,omitempty"`

	// Transient: tracks how many history items were sent in the last LLM call,
	// enabling incremental sends (only new items after this index).
	// Reset on history modification (compaction, DropOldestUserTurns).
	lastSentHistoryLen int `json:"-"`

	// Context compaction tracking
	CompactionCount   int  `json:"compaction_count"` // How many times compaction has occurred
	compactedThisTurn bool `json:"-"`                // Prevents double compaction in one turn

	// Model switch tracking (persists across ContinueAsNew except modelSwitched)
	PreviousModel         string `json:"previous_model,omitempty"`          // Model before last switch
	PreviousContextWindow int    `json:"previous_context_window,omitempty"` // Context window before last switch
	modelSwitched         bool   `json:"-"`                                 // Transient: set on model switch, consumed by maybeCompactBeforeLLM

	// Repeated tool call detection (transient — not serialized)
	lastToolKey string `json:"-"`
	repeatCount int    `json:"-"`

	// Cumulative stats (persist across ContinueAsNew)
	TotalTokens       int      `json:"total_tokens"`
	TotalCachedTokens int      `json:"total_cached_tokens"`
	ToolCallsExecuted []string `json:"tool_calls_executed"`

	// Plan maintained by the LLM via the update_plan intercepted tool.
	// Persists across ContinueAsNew and is exposed via get_turn_status.
	Plan *PlanState `json:"plan,omitempty"`

	// Subagent control — manages child workflow lifecycles.
	// Maps to: codex-rs/core/src/agent/control.rs AgentControl
	AgentCtl *AgentControl `json:"agent_ctl,omitempty"`
}

// PlanStepStatus indicates the status of a single step in a plan.
// Maps to: Codex update_plan tool status enum
type PlanStepStatus string

const (
	PlanStepPending    PlanStepStatus = "pending"
	PlanStepInProgress PlanStepStatus = "in_progress"
	PlanStepCompleted  PlanStepStatus = "completed"
)

// PlanStep is a single step in a plan created by the LLM.
// Maps to: Codex update_plan tool step schema
type PlanStep struct {
	Step   string         `json:"step"`
	Status PlanStepStatus `json:"status"`
}

// PlanState holds the current plan maintained by the LLM via update_plan.
// Maps to: Codex update_plan tool state
type PlanState struct {
	Explanation string     `json:"explanation,omitempty"`
	Steps       []PlanStep `json:"steps"`
}

// WorkflowResult is the final result of the workflow.
type WorkflowResult struct {
	ConversationID    string   `json:"conversation_id"`
	TotalIterations   int      `json:"total_iterations"`
	TotalTokens       int      `json:"total_tokens"`
	TotalCachedTokens int      `json:"total_cached_tokens"`
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
