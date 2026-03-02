// Package workflow contains Temporal workflow definitions.
//
// session.go implements SessionWorkflow — a per-session orchestrator that
// sits between HarnessWorkflow and AgenticWorkflow. It handles one-time
// initialization (config resolution, profile, MCP, memory, skills) and
// starts AgenticWorkflow with pre-resolved configuration.
//
// Maps to: codex-temporal SessionWorkflow
package workflow

import (
	"fmt"

	"go.temporal.io/sdk/workflow"

	"github.com/mfateev/temporal-agent-harness/internal/models"
	"github.com/mfateev/temporal-agent-harness/internal/tools"
)

// SessionWorkflow is the per-session orchestrator.
// Started as a child of HarnessWorkflow (with ABANDON close policy).
// Handles init, starts AgenticWorkflow, and signals harness on completion.
func SessionWorkflow(ctx workflow.Context, input SessionWorkflowInput) error {
	logger := workflow.GetLogger(ctx)
	wfID := workflow.GetInfo(ctx).WorkflowExecution.ID

	// agentWorkflowID uses a convention: sessionWorkflowID + "/main"
	agentWorkflowID := wfID + "/main"

	// Register the readiness query immediately so the WaitForSessionReady
	// activity can poll us. Returns "" until the agent is started.
	var readyAgentID string
	if err := workflow.SetQueryHandler(ctx, QueryGetAgentWorkflowID, func() (string, error) {
		return readyAgentID, nil
	}); err != nil {
		return fmt.Errorf("failed to register %s query: %w", QueryGetAgentWorkflowID, err)
	}

	// --- One-time init (moved from AgenticWorkflow + HarnessWorkflow) ---

	// 1. Resolve file-based config via activities.
	cfg, err := resolveHarnessConfig(ctx, input.Overrides)
	if err != nil {
		logger.Warn("Failed to resolve config, using defaults", "error", err)
		cfg = models.DefaultSessionConfiguration()
	}

	// 2. Resolve model profile (pure computation).
	registry := models.NewDefaultRegistry()
	resolvedProfile := registry.Resolve(cfg.Model.Provider, cfg.Model.Model)
	if resolvedProfile.Temperature != nil {
		cfg.Model.Temperature = *resolvedProfile.Temperature
	}
	if resolvedProfile.MaxTokens != nil {
		cfg.Model.MaxTokens = *resolvedProfile.MaxTokens
	}
	if resolvedProfile.ContextWindow != nil {
		cfg.Model.ContextWindow = *resolvedProfile.ContextWindow
	}

	// 3. Build tool specs and init MCP.
	toolSpecs := buildToolSpecs(cfg.Tools, resolvedProfile)

	var mcpToolSpecs []tools.ToolSpec
	var mcpToolLookup map[string]tools.McpToolRef
	if len(cfg.McpServers) > 0 {
		// Use a temporary SessionState to run initMcpServers (it's a method).
		tempState := &SessionState{
			ConversationID: wfID,
			Config:         cfg,
			ToolSpecs:      toolSpecs,
		}
		if err := tempState.initMcpServers(ctx); err != nil {
			return fmt.Errorf("MCP initialization failed: %w", err)
		}
		// The MCP specs were appended after the built-in specs.
		if len(tempState.ToolSpecs) > len(toolSpecs) {
			mcpToolSpecs = tempState.ToolSpecs[len(toolSpecs):]
		}
		mcpToolLookup = tempState.McpToolLookup
	}

	// 4. Load exec policy (if not already in config).
	if cfg.ExecPolicyRules == "" && cfg.CodexHome != "" {
		tempState := &SessionState{Config: cfg}
		tempState.loadExecPolicy(ctx)
		cfg.ExecPolicyRules = tempState.ExecPolicyRules
	}

	// 5. Load memory summary (root workflows only).
	if cfg.MemoryEnabled {
		tempState := &SessionState{Config: cfg}
		tempState.loadMemorySummary(ctx)
		cfg.DeveloperInstructions = tempState.Config.DeveloperInstructions
	}

	// 6. Load skills.
	tempState := &SessionState{Config: cfg}
	tempState.loadSkills(ctx)
	loadedSkills := tempState.LoadedSkills

	// --- Start AgenticWorkflow as child ---

	childInput := WorkflowInput{
		ConversationID:  agentWorkflowID,
		UserMessage:     input.UserMessage,
		Config:          cfg,
		ResolvedProfile: &resolvedProfile,
		McpToolLookup:   mcpToolLookup,
		McpToolSpecs:    mcpToolSpecs,
		LoadedSkills:    loadedSkills,
		CrewAgents:      input.CrewAgents,
		CrewMainAgent:   input.CrewMainAgent,
	}

	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID: agentWorkflowID,
	})
	future := workflow.ExecuteChildWorkflow(childCtx, AgenticWorkflow, childInput)

	// Wait for child workflow to actually start.
	var exec workflow.Execution
	if err := future.GetChildWorkflowExecution().Get(ctx, &exec); err != nil {
		return fmt.Errorf("failed to start AgenticWorkflow %s: %w", agentWorkflowID, err)
	}

	// Mark as ready — unblocks the WaitForSessionReady activity's query poll.
	readyAgentID = exec.ID
	logger.Info("SessionWorkflow ready",
		"session_id", input.SessionID,
		"agent_workflow_id", readyAgentID)

	// Signal harness that the session is now running (best-effort).
	_ = workflow.SignalExternalWorkflow(ctx, input.HarnessID, "", SignalUpdateSessionStatus, UpdateSessionStatusRequest{
		SessionWorkflowID: wfID,
		Status:            AgentStatusRunning,
	}).Get(ctx, nil)

	// Wait for AgenticWorkflow completion.
	var result WorkflowResult
	childErr := future.Get(ctx, &result)

	// Signal harness with final status (best-effort — harness may have CAN'd).
	finalStatus := AgentStatusCompleted
	if childErr != nil {
		finalStatus = AgentStatusErrored
	}
	_ = workflow.SignalExternalWorkflow(ctx, input.HarnessID, "", SignalUpdateSessionStatus, UpdateSessionStatusRequest{
		SessionWorkflowID: wfID,
		Status:            finalStatus,
	}).Get(ctx, nil)

	return childErr
}

// SessionWorkflowContinued is the ContinueAsNew re-entry point for SessionWorkflow.
// Currently SessionWorkflow does not ContinueAsNew, but this is registered
// for forward compatibility.
func SessionWorkflowContinued(ctx workflow.Context, input SessionWorkflowInput) error {
	return SessionWorkflow(ctx, input)
}
