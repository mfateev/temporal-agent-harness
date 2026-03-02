// Package workflow contains Temporal workflow definitions.
//
// harness.go implements HarnessWorkflow — a long-lived orchestrator that
// manages a session registry on behalf of a single user identity.
// Config resolution and initialization have been moved to SessionWorkflow;
// the harness is a pure registry with signals, queries, and updates.
package workflow

import (
	"fmt"
	"time"

	enums "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/mfateev/temporal-agent-harness/internal/models"
)

// Handler name constants for HarnessWorkflow.
const (
	// QueryGetSessions returns the list of active/completed sessions.
	QueryGetSessions = "get_sessions"

	// UpdateStartSession starts a new agentic session via SessionWorkflow.
	UpdateStartSession = "start_session"
)

// CLIOverrides carries CLI-level arguments that override file-based config.
// Only primitive override values — no file content.
type CLIOverrides struct {
	// Cwd is the working directory for tool execution.
	Cwd string `json:"cwd,omitempty"`

	// CodexHome overrides the default ~/.codex directory.
	CodexHome string `json:"codex_home,omitempty"`

	// Model overrides the model name.
	Model string `json:"model,omitempty"`

	// Provider overrides the model provider.
	Provider string `json:"provider,omitempty"`

	// Permissions overrides (approval, sandbox, env).
	Permissions models.Permissions `json:"permissions,omitempty"`

	// SessionTaskQueue overrides the task queue for session activities.
	SessionTaskQueue string `json:"session_task_queue,omitempty"`

	// DisableSuggestions disables prompt suggestions after turn completion.
	DisableSuggestions bool `json:"disable_suggestions,omitempty"`

	// MemoryEnabled enables the cross-session memory subsystem.
	MemoryEnabled bool `json:"memory_enabled,omitempty"`

	// MemoryDbPath overrides the default memory SQLite DB path.
	MemoryDbPath string `json:"memory_db_path,omitempty"`
}

// HarnessWorkflowInput is the initial input for HarnessWorkflow.
type HarnessWorkflowInput struct {
	// HarnessID is a stable identifier for this harness instance.
	// Used as a prefix for child workflow IDs.
	HarnessID string `json:"harness_id"`

	// Overrides contains CLI-level config overrides.
	Overrides CLIOverrides `json:"overrides,omitempty"`
}

// StartSessionRequest is the payload for the UpdateStartSession update.
type StartSessionRequest struct {
	// UserMessage is the initial message for the new session. Required.
	UserMessage string `json:"user_message"`

	// OverrideConfig applies per-session CLI overrides on top of the
	// harness-level overrides. Optional.
	OverrideConfig *CLIOverrides `json:"override_config,omitempty"`

	// CrewAgents carries interpolated crew agent definitions (from start-crew).
	CrewAgents map[string]models.CrewAgentDef `json:"crew_agents,omitempty"`

	// CrewMainAgent is the name of the main agent in the crew.
	CrewMainAgent string `json:"crew_main_agent,omitempty"`

	// CrewType is the crew template name (for display in session list).
	CrewType string `json:"crew_type,omitempty"`
}

// StartSessionResponse is returned by the UpdateStartSession update.
type StartSessionResponse struct {
	// SessionID is a short stable ID for the session (e.g. "sess-00000001").
	SessionID string `json:"session_id"`

	// SessionWorkflowID is the Temporal workflow ID of the AgenticWorkflow
	// that the TUI should target for user_input/polling.
	SessionWorkflowID string `json:"session_workflow_id"`
}

// SessionEntry tracks a single child session spawned by HarnessWorkflow.
type SessionEntry struct {
	// SessionID is the harness-assigned short identifier.
	SessionID string `json:"session_id"`

	// SessionWorkflowID is the Temporal workflow ID of the SessionWorkflow.
	SessionWorkflowID string `json:"session_workflow_id"`

	// WorkflowID is the Temporal workflow ID of the AgenticWorkflow
	// (child of SessionWorkflow). The TUI targets this workflow.
	WorkflowID string `json:"workflow_id"`

	// UserMessage is the initial message that started the session.
	UserMessage string `json:"user_message"`

	// Name is the user-assigned session name (set via /rename). Optional.
	Name string `json:"name,omitempty"`

	// Model is the model identifier for this session.
	Model string `json:"model,omitempty"`

	// Status is the current lifecycle status of the child workflow.
	Status AgentStatus `json:"status"`

	// StartedAt is the time the session was started (workflow time).
	StartedAt time.Time `json:"started_at"`

	// CrewType is the name of the crew template used to start this session (if any).
	CrewType string `json:"crew_type,omitempty"`
}

// HarnessWorkflowState is passed through ContinueAsNew.
type HarnessWorkflowState struct {
	// HarnessID is preserved across ContinueAsNew.
	HarnessID string `json:"harness_id"`

	// Overrides are preserved across ContinueAsNew.
	Overrides CLIOverrides `json:"overrides,omitempty"`

	// Sessions is the list of all sessions (active and completed).
	Sessions []SessionEntry `json:"sessions,omitempty"`

	// SessionCounter is incremented for each new session to generate unique IDs.
	SessionCounter uint64 `json:"session_counter"`
}

// HarnessWorkflow is the long-lived harness orchestrator entry point.
// Accepts HarnessWorkflowInput and delegates to runHarnessLoop.
func HarnessWorkflow(ctx workflow.Context, input HarnessWorkflowInput) error {
	state := HarnessWorkflowState{
		HarnessID: input.HarnessID,
		Overrides: input.Overrides,
	}
	return runHarnessLoop(ctx, &state)
}

// HarnessWorkflowContinued is the ContinueAsNew re-entry point.
// Accepts serialized state and delegates to runHarnessLoop.
func HarnessWorkflowContinued(ctx workflow.Context, state HarnessWorkflowState) error {
	return runHarnessLoop(ctx, &state)
}

// runHarnessLoop is the core harness event loop shared by both entry points.
// It registers handlers and loops until idle timeout triggers ContinueAsNew.
// The harness is a pure registry — no config resolution.
func runHarnessLoop(ctx workflow.Context, state *HarnessWorkflowState) error {
	logger := workflow.GetLogger(ctx)

	// Register query handler for session list.
	if err := workflow.SetQueryHandler(ctx, QueryGetSessions, func() ([]SessionEntry, error) {
		if state.Sessions == nil {
			return []SessionEntry{}, nil
		}
		return state.Sessions, nil
	}); err != nil {
		return fmt.Errorf("failed to register %s query: %w", QueryGetSessions, err)
	}

	// Register signal handler for session status updates from SessionWorkflow.
	updateStatusCh := workflow.GetSignalChannel(ctx, SignalUpdateSessionStatus)
	workflow.Go(ctx, func(gCtx workflow.Context) {
		for {
			var req UpdateSessionStatusRequest
			updateStatusCh.Receive(gCtx, &req)
			updateSessionStatusByWorkflowID(state, req)
		}
	})

	// Register update handler for starting new sessions.
	if err := workflow.SetUpdateHandlerWithOptions(
		ctx,
		UpdateStartSession,
		func(ctx workflow.Context, req StartSessionRequest) (StartSessionResponse, error) {
			return handleStartSession(ctx, state, req)
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, req StartSessionRequest) error {
				if req.UserMessage == "" {
					return temporal.NewApplicationError("user_message must not be empty", "InvalidRequest")
				}
				return nil
			},
		},
	); err != nil {
		return fmt.Errorf("failed to register %s update: %w", UpdateStartSession, err)
	}

	// Main idle loop — wait for updates or timeout to trigger ContinueAsNew.
	for {
		// ok=true means condition was satisfied; ok=false means timed out.
		ok, err := workflow.AwaitWithTimeout(ctx, IdleTimeout, func() bool {
			return false // no wake-up condition; rely solely on the timeout
		})
		if err != nil {
			return fmt.Errorf("harness await failed: %w", err)
		}
		if !ok {
			// Timed out — trigger ContinueAsNew to keep history bounded.
			logger.Info("Harness idle timeout reached, triggering ContinueAsNew")
			_ = workflow.Await(ctx, func() bool {
				return workflow.AllHandlersFinished(ctx)
			})
			return workflow.NewContinueAsNewError(ctx, HarnessWorkflowContinued, *state)
		}
	}
}

// handleStartSession starts a SessionWorkflow child (with ABANDON policy) and
// polls until the AgenticWorkflow is ready. Returns the AgenticWorkflow ID
// so the TUI can target it directly.
func handleStartSession(
	ctx workflow.Context,
	state *HarnessWorkflowState,
	req StartSessionRequest,
) (StartSessionResponse, error) {
	// Generate a time+counter composite session ID.
	t := workflow.Now(ctx)
	state.SessionCounter++
	sessionID := fmt.Sprintf("sess-%s-%d", t.UTC().Format("20060102-150405"), state.SessionCounter)
	sessionWfID := state.HarnessID + "/" + sessionID

	// Merge harness-level overrides with per-session overrides.
	overrides := mergeCLIOverrides(state.Overrides, req.OverrideConfig)

	// Build SessionWorkflow input.
	sessionInput := SessionWorkflowInput{
		SessionID:     sessionID,
		HarnessID:     state.HarnessID,
		UserMessage:   req.UserMessage,
		Overrides:     overrides,
		CrewAgents:    req.CrewAgents,
		CrewMainAgent: req.CrewMainAgent,
	}

	// Determine model name for the registry (best-effort from overrides).
	model := overrides.Model
	if model == "" {
		model = state.Overrides.Model
	}

	// Agent workflow ID is derived by convention from the session workflow ID.
	agentWfID := sessionWfID + "/main"

	// Record the session entry immediately with PendingInit status.
	// The update_session_status signal from SessionWorkflow will flip it to Running.
	entry := SessionEntry{
		SessionID:         sessionID,
		SessionWorkflowID: sessionWfID,
		WorkflowID:        agentWfID,
		UserMessage:       req.UserMessage,
		Model:             model,
		Status:            AgentStatusPendingInit,
		StartedAt:         workflow.Now(ctx),
		CrewType:          req.CrewType,
	}
	state.Sessions = append(state.Sessions, entry)

	// Start SessionWorkflow as child with ABANDON policy so the harness
	// can ContinueAsNew without terminating running sessions.
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:        sessionWfID,
		ParentClosePolicy: enums.PARENT_CLOSE_POLICY_ABANDON,
	})
	future := workflow.ExecuteChildWorkflow(childCtx, SessionWorkflow, sessionInput)

	// Wait for SessionWorkflow to actually start.
	var exec workflow.Execution
	if err := future.GetChildWorkflowExecution().Get(ctx, &exec); err != nil {
		return StartSessionResponse{}, fmt.Errorf("failed to start SessionWorkflow %s: %w", sessionWfID, err)
	}

	// Wait for the update_session_status signal from SessionWorkflow to
	// flip status from PendingInit → Running (meaning AgenticWorkflow is up).
	// This avoids an activity-based polling loop that bloats harness history.
	if err := workflow.Await(ctx, func() bool {
		for _, s := range state.Sessions {
			if s.SessionID == sessionID {
				return s.Status != AgentStatusPendingInit
			}
		}
		return false
	}); err != nil {
		return StartSessionResponse{}, fmt.Errorf("session %s readiness wait cancelled: %w", sessionID, err)
	}

	// Spawn goroutine to watch child completion and update status.
	// Belt-and-suspenders: SessionWorkflow also signals on completion,
	// which handles the case where the harness CAN'd (goroutine lost).
	workflow.Go(ctx, func(gctx workflow.Context) {
		var result WorkflowResult
		err := future.Get(gctx, &result)
		if err != nil {
			updateSessionStatusByID(state, sessionID, AgentStatusErrored)
		} else {
			updateSessionStatusByID(state, sessionID, AgentStatusCompleted)
		}
	})

	return StartSessionResponse{
		SessionID:         sessionID,
		SessionWorkflowID: agentWfID,
	}, nil
}

// mergeCLIOverrides overlays non-zero fields from overlay onto base.
func mergeCLIOverrides(base CLIOverrides, overlay *CLIOverrides) CLIOverrides {
	result := base
	if overlay == nil {
		return result
	}
	if overlay.Cwd != "" {
		result.Cwd = overlay.Cwd
	}
	if overlay.CodexHome != "" {
		result.CodexHome = overlay.CodexHome
	}
	if overlay.Model != "" {
		result.Model = overlay.Model
	}
	if overlay.Provider != "" {
		result.Provider = overlay.Provider
	}
	if overlay.Permissions.ApprovalMode != "" {
		result.Permissions.ApprovalMode = overlay.Permissions.ApprovalMode
	}
	if overlay.Permissions.SandboxMode != "" {
		result.Permissions.SandboxMode = overlay.Permissions.SandboxMode
	}
	if len(overlay.Permissions.SandboxWritableRoots) > 0 {
		result.Permissions.SandboxWritableRoots = overlay.Permissions.SandboxWritableRoots
	}
	if overlay.Permissions.SandboxNetworkAccess {
		result.Permissions.SandboxNetworkAccess = overlay.Permissions.SandboxNetworkAccess
	}
	if overlay.SessionTaskQueue != "" {
		result.SessionTaskQueue = overlay.SessionTaskQueue
	}
	if overlay.DisableSuggestions {
		result.DisableSuggestions = overlay.DisableSuggestions
	}
	if overlay.MemoryEnabled {
		result.MemoryEnabled = overlay.MemoryEnabled
	}
	if overlay.MemoryDbPath != "" {
		result.MemoryDbPath = overlay.MemoryDbPath
	}
	return result
}

// updateSessionStatusByID finds the session with the given sessionID and updates its status.
func updateSessionStatusByID(state *HarnessWorkflowState, sessionID string, status AgentStatus) {
	for i := range state.Sessions {
		if state.Sessions[i].SessionID == sessionID {
			state.Sessions[i].Status = status
			return
		}
	}
}

// updateSessionStatusByWorkflowID finds the session with the given SessionWorkflowID
// and applies the update from a signal.
func updateSessionStatusByWorkflowID(state *HarnessWorkflowState, req UpdateSessionStatusRequest) {
	for i := range state.Sessions {
		if state.Sessions[i].SessionWorkflowID == req.SessionWorkflowID {
			if req.Status != "" {
				state.Sessions[i].Status = req.Status
			}
			if req.Name != "" {
				state.Sessions[i].Name = req.Name
			}
			return
		}
	}
}
