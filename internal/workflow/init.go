// Package workflow contains Temporal workflow definitions.
//
// init.go handles one-time session initialization: resolving the model profile,
// and (when config is not pre-assembled) loading instructions and exec policy
// from the worker filesystem.
package workflow

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/mfateev/temporal-agent-harness/internal/activities"
	"github.com/mfateev/temporal-agent-harness/internal/instructions"
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
		ApprovalMode:      string(s.Config.ApprovalMode),
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
