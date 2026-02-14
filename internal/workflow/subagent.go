// Subagent orchestration — manages child workflows within a parent workflow.
//
// Maps to:
//   - codex-rs/core/src/agent/control.rs  (AgentControl)
//   - codex-rs/core/src/agent/role.rs     (AgentRole)
//   - codex-rs/core/src/agent/collab.rs   (build_agent_spawn_config, handle_* functions)
//   - codex-rs/protocol/src/protocol.rs   (AgentStatus)
package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/mfateev/temporal-agent-harness/internal/instructions"
	"github.com/mfateev/temporal-agent-harness/internal/models"
)

// ExplorerModel is the cheaper model used for explorer agents on OpenAI.
// Maps to: codex-rs/core/src/agent/role.rs EXPLORER_MODEL
const ExplorerModel = "gpt-5.1-codex-mini"

// ---------------------------------------------------------------------------
// Constants — match Codex Rust guards.rs / collab.rs
// ---------------------------------------------------------------------------

// MaxThreadSpawnDepth is the maximum nesting depth for subagents.
// Parent (depth 0) can spawn children (depth 1). Children cannot spawn grandchildren.
// Maps to: codex-rs/core/src/agent/guards.rs MAX_THREAD_SPAWN_DEPTH
const MaxThreadSpawnDepth = 1

// MinWaitTimeoutMs is the minimum timeout_ms for the wait tool.
const MinWaitTimeoutMs = 10_000

// DefaultWaitTimeoutMs is the default timeout_ms for the wait tool.
const DefaultWaitTimeoutMs = 30_000

// MaxWaitTimeoutMs is the maximum timeout_ms for the wait tool.
const MaxWaitTimeoutMs = 300_000

// closeAgentGracePeriod is how long close_agent waits for the child to finish
// after sending the shutdown signal.
const closeAgentGracePeriod = 5 * time.Second

// ---------------------------------------------------------------------------
// AgentRole — maps to codex-rs/core/src/agent/role.rs AgentRole
// ---------------------------------------------------------------------------

// AgentRole determines the child's configuration overrides.
type AgentRole string

const (
	AgentRoleDefault      AgentRole = "default"
	AgentRoleOrchestrator AgentRole = "orchestrator"
	AgentRoleWorker       AgentRole = "worker"
	AgentRoleExplorer     AgentRole = "explorer"
	AgentRolePlanner      AgentRole = "planner"
)

// parseAgentRole converts a string to AgentRole, defaulting to AgentRoleDefault.
func parseAgentRole(s string) AgentRole {
	switch s {
	case "orchestrator":
		return AgentRoleOrchestrator
	case "worker":
		return AgentRoleWorker
	case "explorer":
		return AgentRoleExplorer
	case "planner":
		return AgentRolePlanner
	default:
		return AgentRoleDefault
	}
}

// ---------------------------------------------------------------------------
// AgentStatus — maps to codex-rs/protocol/src/protocol.rs AgentStatus
// ---------------------------------------------------------------------------

// AgentStatus tracks child workflow lifecycle.
type AgentStatus string

const (
	AgentStatusPendingInit AgentStatus = "pending_init"
	AgentStatusRunning     AgentStatus = "running"
	AgentStatusCompleted   AgentStatus = "completed"
	AgentStatusErrored     AgentStatus = "errored"
	AgentStatusShutdown    AgentStatus = "shutdown"
	AgentStatusNotFound    AgentStatus = "not_found"
)

// isTerminal returns true if the status represents a final state.
func (s AgentStatus) isTerminal() bool {
	switch s {
	case AgentStatusCompleted, AgentStatusErrored, AgentStatusShutdown:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// AgentInfo — tracks a single child workflow's state.
// Maps to: codex-rs/core/src/agent/control.rs AgentInfo
// ---------------------------------------------------------------------------

// AgentInfo tracks a single child workflow's state.
type AgentInfo struct {
	AgentID     string      `json:"agent_id"`
	WorkflowID  string      `json:"workflow_id"`
	RunID       string      `json:"run_id"`
	Role        AgentRole   `json:"role"`
	Status      AgentStatus `json:"status"`
	FinalOutput string      `json:"final_output,omitempty"` // Last assistant message from child
	TaskMessage string      `json:"task_message"`           // Original spawn message
}

// ---------------------------------------------------------------------------
// AgentControl — manages child workflow lifecycles within a parent.
// Maps to: codex-rs/core/src/agent/control.rs AgentControl
// ---------------------------------------------------------------------------

// AgentControl manages child workflow lifecycles within a parent workflow.
type AgentControl struct {
	// Agents persists across ContinueAsNew (JSON-serialized).
	Agents      map[string]*AgentInfo `json:"agents"`
	ParentDepth int                   `json:"parent_depth"` // 0 = parent, 1 = child

	// childFutures is transient — lost on ContinueAsNew.
	// Maps agent ID to the child workflow future for awaiting completion.
	childFutures map[string]workflow.ChildWorkflowFuture `json:"-"`
}

// NewAgentControl creates a new AgentControl for the given depth.
func NewAgentControl(depth int) *AgentControl {
	return &AgentControl{
		Agents:       make(map[string]*AgentInfo),
		ParentDepth:  depth,
		childFutures: make(map[string]workflow.ChildWorkflowFuture),
	}
}

// HasActiveChildren returns true if any child is not in a terminal state.
func (ac *AgentControl) HasActiveChildren() bool {
	for _, info := range ac.Agents {
		if !info.Status.isTerminal() {
			return true
		}
	}
	return false
}

// nextAgentID generates a deterministic agent ID using SideEffect.
func nextAgentID(ctx workflow.Context) string {
	var nanos int64
	encoded := workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
		return workflow.Now(ctx).UnixNano()
	})
	_ = encoded.Get(&nanos)
	return fmt.Sprintf("agent-%d", nanos)
}

// ---------------------------------------------------------------------------
// Collab tool names — used for dispatch and approval classification.
// ---------------------------------------------------------------------------

// collabToolNames is the set of all collaboration tool names.
var collabToolNames = map[string]bool{
	"spawn_agent":  true,
	"send_input":   true,
	"wait":         true,
	"close_agent":  true,
	"resume_agent": true,
}

// isCollabToolCall returns true if the tool name is a collaboration tool.
func isCollabToolCall(name string) bool {
	return collabToolNames[name]
}

// ---------------------------------------------------------------------------
// collabInputItem — structured content item for spawn_agent / send_input.
// Maps to: codex-rs/core/src/agent/collab.rs ContentItem
// ---------------------------------------------------------------------------

type collabInputItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Path     string `json:"path,omitempty"`
	Name     string `json:"name,omitempty"`
}

// parseCollabInput validates that exactly one of message or items is provided
// and returns the resolved plain-text message. For items, only text items are
// extracted (images and paths are not yet supported as message content).
func parseCollabInput(message *string, items []collabInputItem) (string, error) {
	hasMessage := message != nil && *message != ""
	hasItems := len(items) > 0

	if hasMessage && hasItems {
		return "", fmt.Errorf("provide either message or items, not both")
	}
	if !hasMessage && !hasItems {
		return "", fmt.Errorf("either message or items is required")
	}

	if hasMessage {
		return *message, nil
	}

	// Extract text from items
	var texts []string
	for _, item := range items {
		if item.Type == "text" && item.Text != "" {
			texts = append(texts, item.Text)
		}
	}
	if len(texts) == 0 {
		return "", fmt.Errorf("items must contain at least one text item")
	}
	return strings.Join(texts, "\n"), nil
}

// ---------------------------------------------------------------------------
// handleCollabToolCall dispatches to the correct collab handler.
// ---------------------------------------------------------------------------

func (s *SessionState) handleCollabToolCall(ctx workflow.Context, fc models.ConversationItem) (models.ConversationItem, error) {
	switch fc.Name {
	case "spawn_agent":
		return s.handleSpawnAgent(ctx, fc)
	case "send_input":
		return s.handleSendInput(ctx, fc)
	case "wait":
		return s.handleWait(ctx, fc)
	case "close_agent":
		return s.handleCloseAgent(ctx, fc)
	case "resume_agent":
		return s.handleResumeAgent(ctx, fc)
	default:
		return collabErrorOutput(fc.CallID, fmt.Sprintf("unknown collab tool: %s", fc.Name)), nil
	}
}

// ---------------------------------------------------------------------------
// handleSpawnAgent — spawn a child workflow.
// Maps to: codex-rs/core/src/agent/collab.rs handle_spawn_agent
// ---------------------------------------------------------------------------

func (s *SessionState) handleSpawnAgent(ctx workflow.Context, fc models.ConversationItem) (models.ConversationItem, error) {
	logger := workflow.GetLogger(ctx)

	// Parse arguments
	var args struct {
		Message   *string          `json:"message"`
		Items     []collabInputItem `json:"items"`
		AgentType string           `json:"agent_type"`
	}
	if err := json.Unmarshal([]byte(fc.Arguments), &args); err != nil {
		return collabErrorOutput(fc.CallID, fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	msg, err := parseCollabInput(args.Message, args.Items)
	if err != nil {
		return collabErrorOutput(fc.CallID, err.Error()), nil
	}

	// Check depth limit
	childDepth := s.AgentCtl.ParentDepth + 1
	if childDepth > MaxThreadSpawnDepth {
		return collabErrorOutput(fc.CallID, fmt.Sprintf(
			"cannot spawn agent: maximum nesting depth (%d) exceeded", MaxThreadSpawnDepth)), nil
	}

	role := parseAgentRole(args.AgentType)
	agentID := nextAgentID(ctx)

	// Build child workflow input
	childInput := buildAgentSpawnConfig(s.Config, role, msg, childDepth)

	// Register agent info before starting the child
	info := &AgentInfo{
		AgentID:     agentID,
		Role:        role,
		Status:      AgentStatusPendingInit,
		TaskMessage: msg,
	}
	s.AgentCtl.Agents[agentID] = info

	// Start child workflow
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID: s.ConversationID + "/" + agentID,
	})

	future := workflow.ExecuteChildWorkflow(childCtx, "AgenticWorkflow", childInput)

	// Get the child workflow execution info (workflow ID, run ID)
	var childExec workflow.Execution
	if err := future.GetChildWorkflowExecution().Get(ctx, &childExec); err != nil {
		info.Status = AgentStatusErrored
		return collabErrorOutput(fc.CallID, fmt.Sprintf("failed to start child workflow: %v", err)), nil
	}

	info.WorkflowID = childExec.ID
	info.RunID = childExec.RunID
	info.Status = AgentStatusRunning

	// Store the future for later awaiting
	s.AgentCtl.childFutures[agentID] = future

	// Start a goroutine to watch for child completion
	s.startChildCompletionWatcher(ctx, agentID, future)

	logger.Info("Spawned child agent",
		"agent_id", agentID,
		"role", role,
		"child_depth", childDepth,
		"child_workflow_id", childExec.ID)

	// Return success with agent ID
	return collabSuccessOutput(fc.CallID, map[string]interface{}{
		"agent_id": agentID,
	}), nil
}

// ---------------------------------------------------------------------------
// handleSendInput — send a message to a running child.
// Maps to: codex-rs/core/src/agent/collab.rs handle_send_input
// ---------------------------------------------------------------------------

func (s *SessionState) handleSendInput(ctx workflow.Context, fc models.ConversationItem) (models.ConversationItem, error) {
	logger := workflow.GetLogger(ctx)

	var args struct {
		ID        string           `json:"id"`
		Message   *string          `json:"message"`
		Items     []collabInputItem `json:"items"`
		Interrupt bool             `json:"interrupt"`
	}
	if err := json.Unmarshal([]byte(fc.Arguments), &args); err != nil {
		return collabErrorOutput(fc.CallID, fmt.Sprintf("invalid arguments: %v", err)), nil
	}
	if args.ID == "" {
		return collabErrorOutput(fc.CallID, "id is required"), nil
	}

	msg, err := parseCollabInput(args.Message, args.Items)
	if err != nil {
		return collabErrorOutput(fc.CallID, err.Error()), nil
	}

	info, ok := s.AgentCtl.Agents[args.ID]
	if !ok {
		return collabErrorOutput(fc.CallID, fmt.Sprintf("agent %q not found", args.ID)), nil
	}
	if info.Status.isTerminal() {
		return collabErrorOutput(fc.CallID, fmt.Sprintf("agent %q is %s, cannot send input", args.ID, info.Status)), nil
	}

	signal := AgentInputSignal{
		Content:   msg,
		Interrupt: args.Interrupt,
	}

	signalErr := workflow.SignalExternalWorkflow(ctx, info.WorkflowID, info.RunID, SignalAgentInput, signal).Get(ctx, nil)
	if signalErr != nil {
		logger.Warn("Failed to signal child agent", "agent_id", args.ID, "error", signalErr)
		return collabErrorOutput(fc.CallID, fmt.Sprintf("failed to send input to agent %q: %v", args.ID, signalErr)), nil
	}

	logger.Info("Sent input to child agent", "agent_id", args.ID, "interrupt", args.Interrupt)

	return collabSuccessOutput(fc.CallID, map[string]interface{}{
		"submission_id": fmt.Sprintf("input-%s-%d", args.ID, workflow.Now(ctx).UnixNano()),
	}), nil
}

// ---------------------------------------------------------------------------
// handleWait — wait for agents to reach terminal state.
// Maps to: codex-rs/core/src/agent/collab.rs handle_wait
// ---------------------------------------------------------------------------

func (s *SessionState) handleWait(ctx workflow.Context, fc models.ConversationItem) (models.ConversationItem, error) {
	logger := workflow.GetLogger(ctx)

	var args struct {
		IDs       []string `json:"ids"`
		TimeoutMs *float64 `json:"timeout_ms"`
	}
	if err := json.Unmarshal([]byte(fc.Arguments), &args); err != nil {
		return collabErrorOutput(fc.CallID, fmt.Sprintf("invalid arguments: %v", err)), nil
	}
	if len(args.IDs) == 0 {
		return collabErrorOutput(fc.CallID, "ids is required and must be non-empty"), nil
	}

	// Resolve timeout
	timeoutMs := int64(DefaultWaitTimeoutMs)
	if args.TimeoutMs != nil {
		timeoutMs = int64(*args.TimeoutMs)
		if timeoutMs < MinWaitTimeoutMs {
			timeoutMs = MinWaitTimeoutMs
		}
		if timeoutMs > MaxWaitTimeoutMs {
			timeoutMs = MaxWaitTimeoutMs
		}
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond

	s.Phase = PhaseWaitingForAgents

	// Check if any requested agent has already reached terminal state
	anyTerminal := func() bool {
		for _, id := range args.IDs {
			if info, ok := s.AgentCtl.Agents[id]; ok && info.Status.isTerminal() {
				return true
			}
		}
		return false
	}

	timedOut := false
	if !anyTerminal() {
		ok, err := workflow.AwaitWithTimeout(ctx, timeout, func() bool {
			return anyTerminal() || s.Interrupted || s.ShutdownRequested
		})
		if err != nil {
			return models.ConversationItem{}, fmt.Errorf("wait await failed: %w", err)
		}
		timedOut = !ok
	}

	logger.Info("Wait completed", "ids", args.IDs, "timed_out", timedOut)

	// Build status map
	statusMap := make(map[string]interface{}, len(args.IDs))
	for _, id := range args.IDs {
		info, ok := s.AgentCtl.Agents[id]
		if !ok {
			statusMap[id] = map[string]interface{}{
				"status": string(AgentStatusNotFound),
			}
			continue
		}
		entry := map[string]interface{}{
			"status": string(info.Status),
		}
		if info.FinalOutput != "" {
			entry["final_output"] = info.FinalOutput
		}
		statusMap[id] = entry
	}

	return collabSuccessOutput(fc.CallID, map[string]interface{}{
		"status":    statusMap,
		"timed_out": timedOut,
	}), nil
}

// ---------------------------------------------------------------------------
// handleCloseAgent — shut down a child workflow.
// Maps to: codex-rs/core/src/agent/collab.rs handle_close_agent
// ---------------------------------------------------------------------------

func (s *SessionState) handleCloseAgent(ctx workflow.Context, fc models.ConversationItem) (models.ConversationItem, error) {
	logger := workflow.GetLogger(ctx)

	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(fc.Arguments), &args); err != nil {
		return collabErrorOutput(fc.CallID, fmt.Sprintf("invalid arguments: %v", err)), nil
	}
	if args.ID == "" {
		return collabErrorOutput(fc.CallID, "id is required"), nil
	}

	info, ok := s.AgentCtl.Agents[args.ID]
	if !ok {
		return collabErrorOutput(fc.CallID, fmt.Sprintf("agent %q not found", args.ID)), nil
	}

	if info.Status.isTerminal() {
		// Already done — just return current status
		return collabSuccessOutput(fc.CallID, map[string]interface{}{
			"agent_id": args.ID,
			"status":   string(info.Status),
		}), nil
	}

	// Signal shutdown
	err := workflow.SignalExternalWorkflow(ctx, info.WorkflowID, info.RunID, SignalAgentShutdown, nil).Get(ctx, nil)
	if err != nil {
		logger.Warn("Failed to signal shutdown to child agent", "agent_id", args.ID, "error", err)
	}

	// Wait briefly for the child to finish
	_, _ = workflow.AwaitWithTimeout(ctx, closeAgentGracePeriod, func() bool {
		return info.Status.isTerminal()
	})

	if !info.Status.isTerminal() {
		info.Status = AgentStatusShutdown
	}

	logger.Info("Closed child agent", "agent_id", args.ID, "status", info.Status)

	result := map[string]interface{}{
		"agent_id": args.ID,
		"status":   string(info.Status),
	}
	if info.FinalOutput != "" {
		result["final_output"] = info.FinalOutput
	}
	return collabSuccessOutput(fc.CallID, result), nil
}

// ---------------------------------------------------------------------------
// handleResumeAgent — not yet implemented.
// Maps to: codex-rs/core/src/agent/collab.rs handle_resume_agent
// ---------------------------------------------------------------------------

func (s *SessionState) handleResumeAgent(_ workflow.Context, fc models.ConversationItem) (models.ConversationItem, error) {
	return collabErrorOutput(fc.CallID, "resume_agent is not yet implemented"), nil
}

// ---------------------------------------------------------------------------
// startChildCompletionWatcher — goroutine that watches for child completion.
// ---------------------------------------------------------------------------

func (s *SessionState) startChildCompletionWatcher(ctx workflow.Context, agentID string, future workflow.ChildWorkflowFuture) {
	workflow.Go(ctx, func(gCtx workflow.Context) {
		var result WorkflowResult
		err := future.Get(gCtx, &result)

		info, ok := s.AgentCtl.Agents[agentID]
		if !ok {
			return
		}

		if err != nil {
			info.Status = AgentStatusErrored
			info.FinalOutput = fmt.Sprintf("child workflow error: %v", err)
		} else {
			info.Status = AgentStatusCompleted
			info.FinalOutput = result.FinalMessage
		}
	})
}

// ---------------------------------------------------------------------------
// buildAgentSpawnConfig — build WorkflowInput for a child workflow.
// Maps to: codex-rs/core/src/agent/collab.rs build_agent_spawn_config
// ---------------------------------------------------------------------------

func buildAgentSpawnConfig(parentConfig models.SessionConfiguration, role AgentRole, message string, depth int) WorkflowInput {
	childConfig := buildAgentSharedConfig(parentConfig, depth)
	applyRoleOverrides(&childConfig, role)

	return WorkflowInput{
		ConversationID: "", // Will be set by parent (workflow ID includes agent ID)
		UserMessage:    message,
		Config:         childConfig,
		Depth:          depth,
	}
}

// buildAgentSharedConfig clones parent config and applies shared child settings.
// Maps to: codex-rs/core/src/agent/collab.rs build_agent_shared_config
func buildAgentSharedConfig(parentConfig models.SessionConfiguration, depth int) models.SessionConfiguration {
	// Start with a copy of the parent config.
	// Copy the EnabledTools slice so mutations don't affect the parent.
	cfg := parentConfig
	cfg.Tools.EnabledTools = append([]string(nil), parentConfig.Tools.EnabledTools...)

	// Children at max depth cannot spawn further children
	if depth >= MaxThreadSpawnDepth {
		cfg.Tools.RemoveTools("collab")
	}

	// Inherit approval mode from parent
	// Inherit cwd, sandbox, env settings from parent

	return cfg
}

// applyRoleOverrides modifies the config based on the agent role.
// Maps to: codex-rs/core/src/agent/role.rs AgentRole::apply_to_config
func applyRoleOverrides(cfg *models.SessionConfiguration, role AgentRole) {
	switch role {
	case AgentRoleExplorer:
		// Explorer: cheaper model, medium reasoning, read-only tools, one-shot.
		cfg.Model.ReasoningEffort = "medium"
		cfg.Tools.RemoveTools("write_file", "apply_patch", "request_user_input")
		// Override to cheaper model for OpenAI providers
		if cfg.Model.Provider == "openai" {
			cfg.Model.Model = ExplorerModel
		}
		// Keep read tools: shell (for read commands), read_file, list_dir, grep_files
	case AgentRolePlanner:
		// Planner: read-only tools, no collab, keeps user interaction.
		// The planner explores the codebase and produces a plan without modifications.
		// Keeps request_user_input — planners may ask clarifying questions.
		cfg.Tools.RemoveTools("write_file", "apply_patch", "collab")
		// Replace base instructions with planner-specific prompt
		cfg.BaseInstructions = instructions.PlannerBaseInstructions
	case AgentRoleOrchestrator:
		// Orchestrator: coordination focus, no write tools, one-shot.
		cfg.Tools.RemoveTools("write_file", "apply_patch", "request_user_input")
		cfg.BaseInstructions = instructions.OrchestratorBaseInstructions
	case AgentRoleWorker:
		// Worker: full tool access, one-shot (no user interaction).
		cfg.Tools.RemoveTools("request_user_input")
	case AgentRoleDefault:
		// Default: one-shot (no user interaction).
		cfg.Tools.RemoveTools("request_user_input")
	}
}

// ---------------------------------------------------------------------------
// extractFinalMessage scans history for the last assistant message.
// Used to populate WorkflowResult.FinalMessage for child workflows.
// ---------------------------------------------------------------------------

func extractFinalMessage(items []models.ConversationItem) string {
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Type == models.ItemTypeAssistantMessage && items[i].Content != "" {
			return items[i].Content
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Helper: build FunctionCallOutput items for collab tool responses.
// ---------------------------------------------------------------------------

func collabSuccessOutput(callID string, data map[string]interface{}) models.ConversationItem {
	content, _ := json.Marshal(data)
	trueVal := true
	return models.ConversationItem{
		Type:   models.ItemTypeFunctionCallOutput,
		CallID: callID,
		Output: &models.FunctionCallOutputPayload{
			Content: string(content),
			Success: &trueVal,
		},
	}
}

func collabErrorOutput(callID string, message string) models.ConversationItem {
	falseVal := false
	return models.ConversationItem{
		Type:   models.ItemTypeFunctionCallOutput,
		CallID: callID,
		Output: &models.FunctionCallOutputPayload{
			Content: message,
			Success: &falseVal,
		},
	}
}
