// Package workflow contains Temporal workflow definitions.
//
// init.go handles one-time session initialization: resolving the model profile,
// and (when config is not pre-assembled) loading instructions and exec policy
// from the worker filesystem.
package workflow

import (
	"fmt"
	"path/filepath"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/mfateev/temporal-agent-harness/internal/activities"
	"github.com/mfateev/temporal-agent-harness/internal/instructions"
	"github.com/mfateev/temporal-agent-harness/internal/memories"
	"github.com/mfateev/temporal-agent-harness/internal/models"
)

// resolveProfile resolves the model profile from the registry.
// Pure computation — no activity needed. Must be called before
// buildToolSpecs.
func (s *SessionState) resolveProfile() {
	registry := models.NewDefaultRegistry()
	s.ResolvedProfile = registry.Resolve(s.Config.Model.Provider, s.Config.Model.Model)

	// Apply model parameter overrides from the profile
	if s.ResolvedProfile.Temperature != nil {
		s.Config.Model.Temperature = *s.ResolvedProfile.Temperature
	}
	if s.ResolvedProfile.MaxTokens != nil {
		s.Config.Model.MaxTokens = *s.ResolvedProfile.MaxTokens
	}
	if s.ResolvedProfile.ContextWindow != nil {
		s.Config.Model.ContextWindow = *s.ResolvedProfile.ContextWindow
	}
}

// resolveInstructions loads worker-side AGENTS.md files and merges all
// instruction sources into the session configuration. Called when
// BaseInstructions is empty (i.e. AgenticWorkflow was not started via
// HarnessWorkflow). Non-fatal: falls back gracefully on activity failure.
func (s *SessionState) resolveInstructions(ctx workflow.Context) {
	logger := workflow.GetLogger(ctx)

	// Load worker-side project docs via activity (runs on session task queue)
	var workerDocs string
	loadInput := activities.LoadWorkerInstructionsInput{
		Cwd:             s.Config.Cwd,
		AgentsFileNames: s.ResolvedProfile.AgentsFileNames,
	}

	actOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 2,
		},
	}
	if s.Config.SessionTaskQueue != "" {
		actOpts.TaskQueue = s.Config.SessionTaskQueue
	}
	loadCtx := workflow.WithActivityOptions(ctx, actOpts)

	var loadResult activities.LoadWorkerInstructionsOutput
	err := workflow.ExecuteActivity(loadCtx, "LoadWorkerInstructions", loadInput).Get(ctx, &loadResult)
	if err != nil {
		logger.Warn("Failed to load worker instructions, using defaults", "error", err)
	} else {
		workerDocs = loadResult.ProjectDocs
	}

	// Merge all instruction sources, including profile's PromptSuffix
	merged := instructions.MergeInstructions(instructions.MergeInput{
		PromptSuffix:      s.ResolvedProfile.PromptSuffix,
		WorkerProjectDocs: workerDocs,
		ApprovalMode:      string(s.Config.Permissions.ApprovalMode),
		Cwd:               s.Config.Cwd,
	})

	// Store merged results in config (persists through ContinueAsNew)
	s.Config.BaseInstructions = merged.Base
	s.Config.DeveloperInstructions = merged.Developer
	s.Config.UserInstructions = merged.User

	logger.Info("Instructions resolved",
		"base_len", len(merged.Base),
		"developer_len", len(merged.Developer),
		"user_len", len(merged.User))
}

// rebuildInstructions re-merges instructions from existing config values.
// Used when config fields that affect instructions (personality, approval mode)
// change mid-session. Does not reload worker docs — uses cached values.
func (s *SessionState) rebuildInstructions() {
	merged := instructions.MergeInstructions(instructions.MergeInput{
		PromptSuffix:             s.ResolvedProfile.PromptSuffix,
		CLIProjectDocs:           s.Config.CLIProjectDocs,
		UserPersonalInstructions: s.Config.UserPersonalInstructions,
		ApprovalMode:             string(s.Config.Permissions.ApprovalMode),
		Cwd:                      s.Config.Cwd,
		Personality:              s.Config.Personality,
	})
	s.Config.DeveloperInstructions = merged.Developer
	s.Config.UserInstructions = merged.User
}

// loadExecPolicy loads exec policy rules from the worker filesystem.
// Called when ExecPolicyRules is empty (i.e. not pre-loaded by HarnessWorkflow).
// Non-fatal: falls back to empty policy on failure.
func (s *SessionState) loadExecPolicy(ctx workflow.Context) {
	logger := workflow.GetLogger(ctx)

	if s.Config.CodexHome == "" {
		return
	}

	loadInput := activities.LoadExecPolicyInput{
		CodexHome: s.Config.CodexHome,
	}

	actOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 2,
		},
	}
	if s.Config.SessionTaskQueue != "" {
		actOpts.TaskQueue = s.Config.SessionTaskQueue
	}
	loadCtx := workflow.WithActivityOptions(ctx, actOpts)

	var loadResult activities.LoadExecPolicyOutput
	err := workflow.ExecuteActivity(loadCtx, "LoadExecPolicy", loadInput).Get(ctx, &loadResult)
	if err != nil {
		logger.Warn("Failed to load exec policy, using defaults", "error", err)
		return
	}

	s.ExecPolicyRules = loadResult.RulesSource
	logger.Info("Exec policy loaded", "rules_len", len(loadResult.RulesSource))
}

// initMcpServers initializes MCP server connections and discovers their tools.
// Called once before the first turn when McpServers is configured.
// Non-fatal for optional servers; required servers cause workflow error.
//
// Maps to: codex-rs Session initialization of MCP connections
func (s *SessionState) initMcpServers(ctx workflow.Context) error {
	if len(s.Config.McpServers) == 0 {
		return nil
	}

	logger := workflow.GetLogger(ctx)
	logger.Info("Initializing MCP servers", "count", len(s.Config.McpServers))

	initInput := activities.InitializeMcpServersInput{
		SessionID:  s.ConversationID,
		McpServers: s.Config.McpServers,
	}

	actOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second, // MCP servers may take time to start
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 2,
		},
	}
	if s.Config.SessionTaskQueue != "" {
		actOpts.TaskQueue = s.Config.SessionTaskQueue
	}
	initCtx := workflow.WithActivityOptions(ctx, actOpts)

	var initResult activities.InitializeMcpServersOutput
	err := workflow.ExecuteActivity(initCtx, "InitializeMcpServers", initInput).Get(ctx, &initResult)
	if err != nil {
		return fmt.Errorf("MCP initialization activity failed: %w", err)
	}

	// Log failures
	for name, errMsg := range initResult.Failures {
		logger.Warn("MCP server failed to initialize", "server", name, "error", errMsg)
	}

	// Append MCP tool specs to session tool specs
	s.ToolSpecs = append(s.ToolSpecs, initResult.ToolSpecs...)

	// Store MCP tool lookup map for dispatch routing
	s.McpToolLookup = initResult.McpToolLookup

	logger.Info("MCP servers initialized",
		"tools_discovered", len(initResult.ToolSpecs),
		"failures", len(initResult.Failures))

	return nil
}

// memoryRoot returns the resolved memory folder root path.
func (s *SessionState) memoryRoot() string {
	if s.Config.MemoryRoot != "" {
		return s.Config.MemoryRoot
	}
	codexHome := s.Config.CodexHome
	if codexHome == "" {
		codexHome = "~/.codex"
	}
	return filepath.Join(codexHome, "memories")
}

// memoryDbPath returns the resolved memory SQLite database path.
func (s *SessionState) memoryDbPath() string {
	if s.Config.MemoryDbPath != "" {
		return s.Config.MemoryDbPath
	}
	codexHome := s.Config.CodexHome
	if codexHome == "" {
		codexHome = "~/.codex"
	}
	return filepath.Join(codexHome, "state.sqlite")
}

// loadMemorySummary reads the memory summary and injects it into the
// developer instructions. Called at session start for root workflows.
func (s *SessionState) loadMemorySummary(ctx workflow.Context) {
	logger := workflow.GetLogger(ctx)

	actOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 15 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 2,
		},
	}
	if s.Config.SessionTaskQueue != "" {
		actOpts.TaskQueue = s.Config.SessionTaskQueue
	}
	actCtx := workflow.WithActivityOptions(ctx, actOpts)

	var result activities.ReadMemorySummaryOutput
	err := workflow.ExecuteActivity(actCtx, "ReadMemorySummary",
		activities.ReadMemorySummaryInput{
			MemoryRoot: s.memoryRoot(),
			MaxTokens:  memories.MemorySummaryMaxTokens,
		},
	).Get(ctx, &result)
	if err != nil {
		logger.Warn("Failed to read memory summary", "error", err)
		return
	}

	if result.Summary == "" {
		logger.Info("No memory summary found, skipping memory injection")
		return
	}

	// Format and inject the memory section into developer instructions
	memorySection := memories.ReadPathTemplate(s.memoryRoot(), result.Summary)
	if s.Config.DeveloperInstructions != "" {
		s.Config.DeveloperInstructions += "\n\n" + memorySection
	} else {
		s.Config.DeveloperInstructions = memorySection
	}

	logger.Info("Memory summary injected into developer instructions",
		"summary_len", len(result.Summary))
}

// extractMemoryOnShutdown runs phase-1 memory extraction and signals the
// consolidation workflow. Best-effort: errors are logged but don't fail
// the shutdown.
func (s *SessionState) extractMemoryOnShutdown(ctx workflow.Context) {
	logger := workflow.GetLogger(ctx)

	// Skip if already extracted (e.g. idle timeout followed by ContinueAsNew)
	if s.MemoryExtractedAt > 0 {
		return
	}

	items, _ := s.History.GetRawItems()
	// Require at least 4 items for meaningful extraction
	// (turn_started + env_context + user_message + assistant_message)
	if len(items) < 4 {
		logger.Info("Skipping memory extraction: insufficient history", "items", len(items))
		return
	}

	actOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 90 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 2,
		},
	}
	if s.Config.SessionTaskQueue != "" {
		actOpts.TaskQueue = s.Config.SessionTaskQueue
	}
	actCtx := workflow.WithActivityOptions(ctx, actOpts)

	// Determine the phase-1 model
	modelConfig := s.Config.Model
	if s.Config.MemoryConfig.Phase1Model != "" {
		modelConfig.Model = s.Config.MemoryConfig.Phase1Model
	}

	// 1. Extract phase-1
	var phase1Result activities.Phase1Output
	err := workflow.ExecuteActivity(actCtx, "ExtractPhase1",
		activities.Phase1Input{
			History:     items,
			Cwd:         s.Config.Cwd,
			WorkflowID:  s.ConversationID,
			ModelConfig: modelConfig,
		},
	).Get(ctx, &phase1Result)
	if err != nil {
		logger.Warn("Phase-1 memory extraction failed", "error", err)
		return
	}

	if phase1Result.IsNoOp {
		logger.Info("Phase-1 extraction returned no-op, skipping persist")
		s.MemoryExtractedAt = workflow.Now(ctx).Unix()
		return
	}

	// 2. Persist to SQLite
	err = workflow.ExecuteActivity(actCtx, "UpsertStage1Output",
		activities.UpsertStage1Input{
			WorkflowID:      s.ConversationID,
			RawMemory:       phase1Result.RawMemory,
			RolloutSummary:  phase1Result.RolloutSummary,
			RolloutSlug:     phase1Result.RolloutSlug,
			Cwd:             s.Config.Cwd,
			SourceUpdatedAt: workflow.Now(ctx).Unix(),
		},
	).Get(ctx, nil)
	if err != nil {
		logger.Warn("Failed to persist stage1 output", "error", err)
		return
	}

	// 3. Signal consolidation workflow (best-effort)
	consolidationModelConfig := s.Config.Model
	if s.Config.MemoryConfig.Phase2Model != "" {
		consolidationModelConfig.Model = s.Config.MemoryConfig.Phase2Model
	}

	maxRaw := s.Config.MemoryConfig.MaxRawMemoriesForGlobal
	if maxRaw <= 0 {
		maxRaw = 1024
	}

	err = workflow.ExecuteActivity(actCtx, "SignalConsolidation",
		activities.SignalConsolidationInput{
			SessionWorkflowID: s.ConversationID,
			MemoryRoot:        s.memoryRoot(),
			MemoryDbPath:      s.memoryDbPath(),
			ModelConfig:       consolidationModelConfig,
			MaxRawMemories:    maxRaw,
		},
	).Get(ctx, nil)
	if err != nil {
		logger.Warn("Failed to signal consolidation workflow", "error", err)
		// Not fatal — extraction was still persisted
	}

	s.MemoryExtractedAt = workflow.Now(ctx).Unix()
	logger.Info("Memory extraction completed and consolidation signaled",
		"workflow_id", s.ConversationID)
}
