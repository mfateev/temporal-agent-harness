// Package workflow contains Temporal workflow definitions.
//
// harness.go implements HarnessWorkflow — a long-lived orchestrator that
// owns multiple agentic sessions (child AgenticWorkflow runs) on behalf of
// a single user identity.
package workflow

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/mfateev/temporal-agent-harness/internal/activities"
	"github.com/mfateev/temporal-agent-harness/internal/instructions"
	"github.com/mfateev/temporal-agent-harness/internal/models"
)

// Handler name constants for HarnessWorkflow.
const (
	// QueryGetSessions returns the list of active/completed sessions.
	QueryGetSessions = "get_sessions"

	// UpdateStartSession starts a new agentic session as a child workflow.
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

	// ApprovalMode overrides the approval policy.
	ApprovalMode models.ApprovalMode `json:"approval_mode,omitempty"`

	// SessionTaskQueue overrides the task queue for session activities.
	SessionTaskQueue string `json:"session_task_queue,omitempty"`

	// SandboxMode overrides the sandbox mode ("full-access", "read-only", "workspace-write").
	SandboxMode string `json:"sandbox_mode,omitempty"`

	// SandboxWritableRoots overrides the writable roots for workspace-write mode.
	SandboxWritableRoots []string `json:"sandbox_writable_roots,omitempty"`

	// SandboxNetworkAccess overrides whether network is allowed in the sandbox.
	SandboxNetworkAccess bool `json:"sandbox_network_access,omitempty"`

	// DisableSuggestions disables prompt suggestions after turn completion.
	DisableSuggestions bool `json:"disable_suggestions,omitempty"`
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
	// harness-resolved base config. Optional.
	OverrideConfig *CLIOverrides `json:"override_config,omitempty"`
}

// StartSessionResponse is returned by the UpdateStartSession update.
type StartSessionResponse struct {
	// SessionID is a short stable ID for the session (e.g. "sess-00000001").
	SessionID string `json:"session_id"`

	// SessionWorkflowID is the Temporal workflow ID of the child workflow.
	SessionWorkflowID string `json:"session_workflow_id"`
}

// SessionEntry tracks a single child session spawned by HarnessWorkflow.
type SessionEntry struct {
	// SessionID is the harness-assigned short identifier.
	SessionID string `json:"session_id"`

	// WorkflowID is the Temporal workflow ID of the child AgenticWorkflow.
	WorkflowID string `json:"workflow_id"`

	// UserMessage is the initial message that started the session.
	UserMessage string `json:"user_message"`

	// Status is the current lifecycle status of the child workflow.
	Status AgentStatus `json:"status"`

	// StartedAt is the time the session was started (workflow time).
	StartedAt time.Time `json:"started_at"`
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
// It resolves config, registers handlers, and loops until idle timeout triggers
// ContinueAsNew.
func runHarnessLoop(ctx workflow.Context, state *HarnessWorkflowState) error {
	logger := workflow.GetLogger(ctx)

	// Resolve file-based config via activities (once per workflow run).
	cfg, err := resolveHarnessConfig(ctx, state.Overrides)
	if err != nil {
		logger.Warn("Failed to resolve harness config, using defaults", "error", err)
		cfg = models.DefaultSessionConfiguration()
	}

	// Register query handler for session list.
	if err := workflow.SetQueryHandler(ctx, QueryGetSessions, func() ([]SessionEntry, error) {
		if state.Sessions == nil {
			return []SessionEntry{}, nil
		}
		return state.Sessions, nil
	}); err != nil {
		return fmt.Errorf("failed to register %s query: %w", QueryGetSessions, err)
	}

	// Register update handler for starting new sessions.
	if err := workflow.SetUpdateHandlerWithOptions(
		ctx,
		UpdateStartSession,
		func(ctx workflow.Context, req StartSessionRequest) (StartSessionResponse, error) {
			return handleStartSession(ctx, state, cfg, req)
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

// resolveHarnessConfig loads all file-based configuration via activities and
// assembles a SessionConfiguration to use as the base for new sessions.
func resolveHarnessConfig(ctx workflow.Context, overrides CLIOverrides) (models.SessionConfiguration, error) {
	logger := workflow.GetLogger(ctx)

	actOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 2,
		},
	}
	if overrides.SessionTaskQueue != "" {
		actOpts.TaskQueue = overrides.SessionTaskQueue
	}
	actCtx := workflow.WithActivityOptions(ctx, actOpts)

	// Load worker-side project docs (AGENTS.md).
	var workerDocs string
	var loadWorkerResult activities.LoadWorkerInstructionsOutput
	loadWorkerInput := activities.LoadWorkerInstructionsInput{
		Cwd:             overrides.Cwd,
		AgentsFileNames: nil, // use defaults
	}
	if err := workflow.ExecuteActivity(actCtx, "LoadWorkerInstructions", loadWorkerInput).Get(ctx, &loadWorkerResult); err != nil {
		logger.Warn("Failed to load worker instructions", "error", err)
	} else {
		workerDocs = loadWorkerResult.ProjectDocs
	}

	// Load exec policy rules.
	var execPolicyRules string
	if overrides.CodexHome != "" {
		var loadExecResult activities.LoadExecPolicyOutput
		loadExecInput := activities.LoadExecPolicyInput{
			CodexHome: overrides.CodexHome,
		}
		if err := workflow.ExecuteActivity(actCtx, "LoadExecPolicy", loadExecInput).Get(ctx, &loadExecResult); err != nil {
			logger.Warn("Failed to load exec policy", "error", err)
		} else {
			execPolicyRules = loadExecResult.RulesSource
		}
	}

	// Load personal instructions.
	var personalInstructions string
	var loadPersonalResult activities.LoadPersonalInstructionsOutput
	loadPersonalInput := activities.LoadPersonalInstructionsInput{
		CodexHome: overrides.CodexHome,
	}
	if err := workflow.ExecuteActivity(actCtx, "LoadPersonalInstructions", loadPersonalInput).Get(ctx, &loadPersonalResult); err != nil {
		logger.Warn("Failed to load personal instructions", "error", err)
	} else {
		personalInstructions = loadPersonalResult.Instructions
	}

	// Merge all instruction sources.
	merged := instructions.MergeInstructions(instructions.MergeInput{
		WorkerProjectDocs:        workerDocs,
		UserPersonalInstructions: personalInstructions,
		ApprovalMode:             string(overrides.ApprovalMode),
		Cwd:                      overrides.Cwd,
	})

	// Assemble SessionConfiguration from defaults + overrides + resolved data.
	cfg := models.DefaultSessionConfiguration()

	cfg.BaseInstructions = merged.Base
	cfg.DeveloperInstructions = merged.Developer
	cfg.UserInstructions = merged.User
	cfg.ExecPolicyRules = execPolicyRules
	cfg.Cwd = overrides.Cwd
	cfg.CodexHome = overrides.CodexHome
	cfg.SessionTaskQueue = overrides.SessionTaskQueue

	if overrides.ApprovalMode != "" {
		cfg.ApprovalMode = overrides.ApprovalMode
	}
	if overrides.Provider != "" {
		cfg.Model.Provider = overrides.Provider
	}
	if overrides.Model != "" {
		cfg.Model.Model = overrides.Model
	}

	return cfg, nil
}

// handleStartSession starts a new AgenticWorkflow child and records the session.
func handleStartSession(
	ctx workflow.Context,
	state *HarnessWorkflowState,
	cfg models.SessionConfiguration,
	req StartSessionRequest,
) (StartSessionResponse, error) {
	// Generate a time+counter composite session ID so the ID is meaningful
	// in the session picker list.
	t := workflow.Now(ctx)
	state.SessionCounter++
	sessionID := fmt.Sprintf("sess-%s-%d", t.UTC().Format("20060102-150405"), state.SessionCounter)
	childWfID := state.HarnessID + "/" + sessionID

	// Apply per-request overrides if provided.
	sessionCfg := cfg
	if req.OverrideConfig != nil {
		applyOverrides(&sessionCfg, req.OverrideConfig)
	}

	// Build child workflow input.
	childInput := WorkflowInput{
		ConversationID: childWfID,
		UserMessage:    req.UserMessage,
		Config:         sessionCfg,
	}

	// Start child workflow.
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID: childWfID,
	})
	future := workflow.ExecuteChildWorkflow(childCtx, AgenticWorkflow, childInput)

	// Wait for child workflow execution details (workflow ID, run ID).
	var exec workflow.Execution
	if err := future.GetChildWorkflowExecution().Get(ctx, &exec); err != nil {
		return StartSessionResponse{}, fmt.Errorf("failed to start child workflow %s: %w", childWfID, err)
	}

	// Record the session entry.
	entry := SessionEntry{
		SessionID:   sessionID,
		WorkflowID:  exec.ID,
		UserMessage: req.UserMessage,
		Status:      AgentStatusRunning,
		StartedAt:   workflow.Now(ctx),
	}
	state.Sessions = append(state.Sessions, entry)

	// Spawn goroutine to watch child completion and update status.
	workflow.Go(ctx, func(gctx workflow.Context) {
		var result WorkflowResult
		err := future.Get(gctx, &result)
		if err != nil {
			updateSessionStatus(state, sessionID, AgentStatusErrored)
		} else {
			updateSessionStatus(state, sessionID, AgentStatusCompleted)
		}
	})

	return StartSessionResponse{
		SessionID:         sessionID,
		SessionWorkflowID: exec.ID,
	}, nil
}

// applyOverrides copies non-zero fields from o into cfg.
func applyOverrides(cfg *models.SessionConfiguration, o *CLIOverrides) {
	if o == nil {
		return
	}
	if o.Cwd != "" {
		cfg.Cwd = o.Cwd
	}
	if o.CodexHome != "" {
		cfg.CodexHome = o.CodexHome
	}
	if o.Model != "" {
		cfg.Model.Model = o.Model
	}
	if o.Provider != "" {
		cfg.Model.Provider = o.Provider
	}
	if o.ApprovalMode != "" {
		cfg.ApprovalMode = o.ApprovalMode
	}
	if o.SessionTaskQueue != "" {
		cfg.SessionTaskQueue = o.SessionTaskQueue
	}
	if o.SandboxMode != "" {
		cfg.SandboxMode = o.SandboxMode
	}
	if len(o.SandboxWritableRoots) > 0 {
		cfg.SandboxWritableRoots = o.SandboxWritableRoots
	}
	if o.SandboxNetworkAccess {
		cfg.SandboxNetworkAccess = o.SandboxNetworkAccess
	}
	if o.DisableSuggestions {
		cfg.DisableSuggestions = o.DisableSuggestions
	}
}

// updateSessionStatus finds the session with the given sessionID and updates its status.
func updateSessionStatus(state *HarnessWorkflowState, sessionID string, status AgentStatus) {
	for i := range state.Sessions {
		if state.Sessions[i].SessionID == sessionID {
			state.Sessions[i].Status = status
			return
		}
	}
}
