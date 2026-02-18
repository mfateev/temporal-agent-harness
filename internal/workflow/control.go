// Package workflow contains Temporal workflow definitions.
//
// control.go defines LoopControl, which separates Temporal coordination concerns
// from SessionState. LoopControl owns all synchronization between handlers and
// the agentic loop: response slots, phase tracking, and user input queue flags.
//
// NOTE: Temporal-specific addition (not in Codex Rust). This type decouples
// Temporal signal/update coordination from the serializable agent state in
// SessionState. Each workflow run constructs a fresh LoopControl; it is never
// serialized through ContinueAsNew.
package workflow

import (
	"fmt"

	"go.temporal.io/sdk/workflow"
)

// ResponseSlot holds a single awaitable response of type T.
// It replaces the raw bool+*T field pairs previously scattered in SessionState.
type ResponseSlot[T any] struct {
	received bool
	value    *T
}

// Deliver stores a response and marks the slot as ready.
func (s *ResponseSlot[T]) Deliver(v T) {
	s.value = &v
	s.received = true
}

// Ready returns true if a response has been delivered.
func (s *ResponseSlot[T]) Ready() bool { return s.received }

// Take retrieves the response and resets the slot to empty. Returns nil if not ready.
func (s *ResponseSlot[T]) Take() *T {
	v := s.value
	s.received = false
	s.value = nil
	return v
}

// clear resets the slot to the initial empty state.
func (s *ResponseSlot[T]) clear() {
	s.received = false
	s.value = nil
}

// LoopControl owns all Temporal coordination state for the agentic workflow.
// It holds response slots for each blocking wait point, phase tracking, and
// user input queue flags.
//
// LoopControl is constructed fresh each workflow run and is never serialized
// through ContinueAsNew. Its transient slots are simply reset on each
// continuation.
//
// NOTE: Temporal-specific addition (not in Codex Rust).
type LoopControl struct {
	// User input / lifecycle flags
	pendingUserInput  bool
	shutdownRequested bool
	interrupted       bool
	compactRequested  bool
	currentTurnID     string

	// Observable state for get_turn_status query
	phase               TurnPhase
	toolsInFlight       []string
	pendingApprovals    []PendingApproval
	pendingEscalations  []EscalationRequest
	pendingUserInputReq *PendingUserInputRequest
	suggestion          string

	// Response slots — replace raw bool+*T field pairs in SessionState.
	approvalSlot   ResponseSlot[ApprovalResponse]
	escalationSlot ResponseSlot[EscalationResponse]
	userInputQSlot ResponseSlot[UserInputQuestionResponse]
}

// --- Delivery methods (called by update handlers) ---

// DeliverApproval stores an approval response and clears visible pending state.
// Called by the approval_response update handler.
func (ctrl *LoopControl) DeliverApproval(resp ApprovalResponse) {
	ctrl.approvalSlot.Deliver(resp)
	ctrl.pendingApprovals = nil // clear immediately so query handler reflects the response
}

// DeliverEscalation stores an escalation response and clears visible pending state.
// Called by the escalation_response update handler.
func (ctrl *LoopControl) DeliverEscalation(resp EscalationResponse) {
	ctrl.escalationSlot.Deliver(resp)
	ctrl.pendingEscalations = nil
}

// DeliverUserInputQ stores a user-input-question response and clears visible
// pending state. Called by the user_input_question_response update handler.
func (ctrl *LoopControl) DeliverUserInputQ(resp UserInputQuestionResponse) {
	ctrl.userInputQSlot.Deliver(resp)
	ctrl.pendingUserInputReq = nil
}

// --- Lifecycle setters (called by handlers) ---

// SetPendingUserInput records a new user-input turn with the given ID.
// Sets both the current turn ID and the pending-input flag.
func (ctrl *LoopControl) SetPendingUserInput(turnID string) {
	ctrl.currentTurnID = turnID
	ctrl.pendingUserInput = true
}

// SetInterrupted marks the current turn as interrupted.
func (ctrl *LoopControl) SetInterrupted() {
	ctrl.interrupted = true
}

// SetShutdown marks the session as shut down and interrupts the current turn.
func (ctrl *LoopControl) SetShutdown() {
	ctrl.shutdownRequested = true
	ctrl.interrupted = true
}

// SetCompactRequested requests a manual context compaction.
func (ctrl *LoopControl) SetCompactRequested() {
	ctrl.compactRequested = true
}

// --- Phase / tool tracking (called by loop and turn code) ---

// SetPhase updates the current turn phase (visible via get_turn_status).
func (ctrl *LoopControl) SetPhase(p TurnPhase) { ctrl.phase = p }

// Phase returns the current turn phase.
func (ctrl *LoopControl) Phase() TurnPhase { return ctrl.phase }

// SetToolsInFlight records the names of currently executing tools.
func (ctrl *LoopControl) SetToolsInFlight(tools []string) { ctrl.toolsInFlight = tools }

// ClearToolsInFlight clears the in-flight tool list.
func (ctrl *LoopControl) ClearToolsInFlight() { ctrl.toolsInFlight = nil }

// SetSuggestion stores the post-turn prompt suggestion.
func (ctrl *LoopControl) SetSuggestion(s string) { ctrl.suggestion = s }

// CurrentTurnID returns the active turn ID.
func (ctrl *LoopControl) CurrentTurnID() string { return ctrl.currentTurnID }

// --- Observable state accessors (for query handlers) ---

// ToolsInFlight returns the currently in-flight tool names.
func (ctrl *LoopControl) ToolsInFlight() []string { return ctrl.toolsInFlight }

// PendingApprovals returns the current pending approval list.
func (ctrl *LoopControl) PendingApprovals() []PendingApproval { return ctrl.pendingApprovals }

// PendingEscalations returns the current pending escalation list.
func (ctrl *LoopControl) PendingEscalations() []EscalationRequest { return ctrl.pendingEscalations }

// PendingUserInputReq returns the current pending user-input question request.
func (ctrl *LoopControl) PendingUserInputReq() *PendingUserInputRequest {
	return ctrl.pendingUserInputReq
}

// Suggestion returns the post-turn prompt suggestion (best-effort).
func (ctrl *LoopControl) Suggestion() string { return ctrl.suggestion }

// --- Flag accessors ---

// HasPendingWork returns true if the loop has work to do without waiting.
func (ctrl *LoopControl) HasPendingWork() bool {
	return ctrl.pendingUserInput || ctrl.shutdownRequested || ctrl.compactRequested
}

// IsShutdown returns true if a shutdown has been requested.
func (ctrl *LoopControl) IsShutdown() bool { return ctrl.shutdownRequested }

// IsInterrupted returns true if the current turn has been interrupted.
func (ctrl *LoopControl) IsInterrupted() bool { return ctrl.interrupted }

// IsCompactRequested returns true if manual compaction was requested.
func (ctrl *LoopControl) IsCompactRequested() bool { return ctrl.compactRequested }

// --- Turn lifecycle ---

// StartTurn resets per-turn flags. Called at the start of each agentic turn,
// not during compaction or other loop-level operations.
func (ctrl *LoopControl) StartTurn() {
	ctrl.pendingUserInput = false
	ctrl.interrupted = false
	ctrl.suggestion = ""
}

// ClearCompactRequested marks the compact request as handled.
func (ctrl *LoopControl) ClearCompactRequested() {
	ctrl.compactRequested = false
}

// --- Blocking wait methods (encapsulate workflow.Await calls) ---

// WaitForInput blocks until user input, shutdown, or compact is requested,
// or the idle timeout fires. Returns (timedOut, error).
func (ctrl *LoopControl) WaitForInput(ctx workflow.Context) (bool, error) {
	return awaitWithIdleTimeout(ctx, func() bool {
		return ctrl.pendingUserInput || ctrl.shutdownRequested || ctrl.compactRequested
	})
}

// AwaitApproval sets approval-pending state, blocks until a response arrives
// or the turn is interrupted, then returns the response.
// Returns nil if interrupted or shutdown before a response arrived.
func (ctrl *LoopControl) AwaitApproval(ctx workflow.Context, needsApproval []PendingApproval) (*ApprovalResponse, error) {
	logger := workflow.GetLogger(ctx)

	ctrl.phase = PhaseApprovalPending
	ctrl.pendingApprovals = needsApproval
	ctrl.approvalSlot.clear()

	logger.Info("Waiting for tool approval", "count", len(needsApproval))

	err := workflow.Await(ctx, func() bool {
		return ctrl.approvalSlot.Ready() || ctrl.interrupted || ctrl.shutdownRequested
	})
	if err != nil {
		return nil, fmt.Errorf("approval await failed: %w", err)
	}

	ctrl.pendingApprovals = nil

	if ctrl.interrupted || ctrl.shutdownRequested {
		logger.Info("Approval wait interrupted")
		return nil, nil
	}
	return ctrl.approvalSlot.Take(), nil
}

// AwaitEscalation sets escalation-pending state, blocks until a response
// arrives or the turn is interrupted, then returns the response.
// Returns nil if interrupted or shutdown before a response arrived.
func (ctrl *LoopControl) AwaitEscalation(ctx workflow.Context, escalations []EscalationRequest) (*EscalationResponse, error) {
	logger := workflow.GetLogger(ctx)

	ctrl.phase = PhaseEscalationPending
	ctrl.pendingEscalations = escalations
	ctrl.escalationSlot.clear()

	logger.Info("Waiting for escalation decision", "failed_count", len(escalations))

	err := workflow.Await(ctx, func() bool {
		return ctrl.escalationSlot.Ready() || ctrl.interrupted || ctrl.shutdownRequested
	})
	if err != nil {
		return nil, fmt.Errorf("escalation await failed: %w", err)
	}

	ctrl.pendingEscalations = nil

	if ctrl.interrupted || ctrl.shutdownRequested {
		logger.Info("Escalation wait interrupted")
		return nil, nil
	}
	return ctrl.escalationSlot.Take(), nil
}

// AwaitUserInputQuestion sets user-input-pending state, blocks until a
// response arrives or the turn is interrupted, then returns the response.
// Returns nil if interrupted or shutdown before a response arrived.
func (ctrl *LoopControl) AwaitUserInputQuestion(ctx workflow.Context, req *PendingUserInputRequest) (*UserInputQuestionResponse, error) {
	logger := workflow.GetLogger(ctx)

	ctrl.phase = PhaseUserInputPending
	ctrl.pendingUserInputReq = req
	ctrl.userInputQSlot.clear()

	logger.Info("Waiting for user input response", "question_count", len(req.Questions))

	err := workflow.Await(ctx, func() bool {
		return ctrl.userInputQSlot.Ready() || ctrl.interrupted || ctrl.shutdownRequested
	})
	if err != nil {
		return nil, fmt.Errorf("user input await failed: %w", err)
	}

	ctrl.pendingUserInputReq = nil

	if ctrl.interrupted || ctrl.shutdownRequested {
		logger.Info("User input wait interrupted")
		return nil, nil
	}
	return ctrl.userInputQSlot.Take(), nil
}
