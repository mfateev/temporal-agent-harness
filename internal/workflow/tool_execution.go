// Package workflow contains Temporal workflow definitions.
//
// tool_execution.go handles parallel tool activity dispatch and error conversion.
//
// Maps to: codex-rs/core/src/tools/parallel.rs drain_in_flight
package workflow

import (
	"encoding/json"
	"errors"
	"time"

	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/mfateev/temporal-agent-harness/internal/activities"
	"github.com/mfateev/temporal-agent-harness/internal/models"
	"github.com/mfateev/temporal-agent-harness/internal/tools"
)

// ToolExecutor handles parallel tool activity dispatch.
type ToolExecutor struct {
	toolSpecs        []tools.ToolSpec
	cwd              string
	sessionTaskQueue string
}

// NewToolExecutor creates a ToolExecutor with the given specs, working directory, and task queue.
func NewToolExecutor(specs []tools.ToolSpec, cwd, taskQueue string) *ToolExecutor {
	return &ToolExecutor{toolSpecs: specs, cwd: cwd, sessionTaskQueue: taskQueue}
}

// ExecuteParallel runs all tool activities in parallel and waits for all.
// Delegates to executeToolsInParallel.
func (e *ToolExecutor) ExecuteParallel(ctx workflow.Context, calls []models.ConversationItem) ([]activities.ToolActivityOutput, error) {
	return executeToolsInParallel(ctx, calls, e.toolSpecs, e.cwd, e.sessionTaskQueue)
}

// executeToolsInParallel runs all tool activities in parallel and waits for all.
//
// Each tool gets a per-activity StartToCloseTimeout derived from:
//  1. timeout_ms argument provided by the LLM (highest priority)
//  2. DefaultTimeoutMs from the tool's ToolSpec
//  3. DefaultToolTimeoutMs constant as a fallback
//
// If sessionTaskQueue is non-empty, tool activities are dispatched to that queue
// (enabling per-session worker routing in multi-host mode).
//
// Maps to: codex-rs/core/src/tools/parallel.rs drain_in_flight
func executeToolsInParallel(ctx workflow.Context, functionCalls []models.ConversationItem, toolSpecs []tools.ToolSpec, cwd, sessionTaskQueue string) ([]activities.ToolActivityOutput, error) {
	logger := workflow.GetLogger(ctx)

	// Build a lookup map from tool name to spec for fast access.
	specByName := make(map[string]tools.ToolSpec, len(toolSpecs))
	for _, spec := range toolSpecs {
		specByName[spec.Name] = spec
	}

	// Start all tool activities in parallel using futures
	futures := make([]workflow.Future, len(functionCalls))
	for i, fc := range functionCalls {
		logger.Info("Starting tool execution", "tool", fc.Name, "call_id", fc.CallID)

		// Parse arguments from raw JSON string
		var args map[string]interface{}
		if fc.Arguments != "" {
			if err := json.Unmarshal([]byte(fc.Arguments), &args); err != nil {
				args = map[string]interface{}{"_raw": fc.Arguments}
			}
		}

		// Resolve per-tool timeout for StartToCloseTimeout.
		timeout := resolveToolTimeout(specByName, fc.Name, args)

		actOpts := workflow.ActivityOptions{
			StartToCloseTimeout: timeout,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval:    time.Second,
				BackoffCoefficient: 2.0,
				MaximumInterval:    time.Minute,
				MaximumAttempts:    5,
			},
		}
		if sessionTaskQueue != "" {
			actOpts.TaskQueue = sessionTaskQueue
		}
		toolCtx := workflow.WithActivityOptions(ctx, actOpts)

		input := activities.ToolActivityInput{
			CallID:    fc.CallID,
			ToolName:  fc.Name,
			Arguments: args,
			Cwd:       cwd,
		}
		futures[i] = workflow.ExecuteActivity(toolCtx, "ExecuteTool", input)
	}

	// Wait for ALL tools to complete.
	// Activity errors (ApplicationError) are converted to failed tool results
	// so the LLM can see what went wrong and decide how to proceed.
	results := make([]activities.ToolActivityOutput, len(functionCalls))
	for i, future := range futures {
		var result activities.ToolActivityOutput
		if err := future.Get(ctx, &result); err != nil {
			results[i] = toolActivityErrorToOutput(logger, functionCalls[i].CallID, functionCalls[i].Name, err)
		} else {
			results[i] = result
			logger.Info("Tool execution completed", "tool", functionCalls[i].Name)
		}
	}

	return results, nil
}

// buildToolSpecs builds tool specifications based on configuration and profile.
// After building the base set from ToolsConfig, it filters out any tools
// listed in the profile's ToolOverrides.Disable list.
func buildToolSpecs(config models.ToolsConfig, profile models.ResolvedProfile) []tools.ToolSpec {
	specs := []tools.ToolSpec{}

	switch config.ResolvedShellType() {
	case models.ShellToolDefault:
		specs = append(specs, tools.NewShellToolSpec(false))
	case models.ShellToolShellCommand:
		specs = append(specs, tools.NewShellCommandToolSpec(false))
	case models.ShellToolDisabled:
		// no shell tool
	}

	if config.EnableReadFile {
		specs = append(specs, tools.NewReadFileToolSpec())
	}

	if config.EnableWriteFile {
		specs = append(specs, tools.NewWriteFileToolSpec())
	}

	if config.EnableListDir {
		specs = append(specs, tools.NewListDirToolSpec())
	}

	if config.EnableGrepFiles {
		specs = append(specs, tools.NewGrepFilesToolSpec())
	}

	if config.EnableApplyPatch {
		specs = append(specs, tools.NewApplyPatchToolSpec())
	}

	// request_user_input is always available (intercepted by workflow, not dispatched)
	specs = append(specs, tools.NewRequestUserInputToolSpec())

	// update_plan is intercepted by the workflow (not dispatched as an activity)
	if config.EnableUpdatePlan {
		specs = append(specs, tools.NewUpdatePlanToolSpec())
	}

	// Collaboration tools for subagent orchestration (intercepted by workflow)
	if config.EnableCollab {
		specs = append(specs,
			tools.NewSpawnAgentToolSpec(),
			tools.NewSendInputToolSpec(),
			tools.NewWaitToolSpec(),
			tools.NewCloseAgentToolSpec(),
			tools.NewResumeAgentToolSpec(),
		)
	}

	// Filter out tools disabled by the profile
	if profile.Tools != nil && len(profile.Tools.Disable) > 0 {
		disabled := make(map[string]bool, len(profile.Tools.Disable))
		for _, name := range profile.Tools.Disable {
			disabled[name] = true
		}
		filtered := specs[:0]
		for _, spec := range specs {
			if !disabled[spec.Name] {
				filtered = append(filtered, spec)
			}
		}
		specs = filtered
	}

	return specs
}

// toolActivityErrorToOutput converts a tool activity error into a ToolActivityOutput
// so the LLM can see what went wrong and decide how to proceed.
//
// Uses ApplicationError.Type() for classification and .Details() for structured context.
// Never parses error messages.
func toolActivityErrorToOutput(logger log.Logger, callID, toolName string, err error) activities.ToolActivityOutput {
	success := false
	reason := "unknown error"

	var appErr *temporal.ApplicationError
	var timeoutErr *temporal.TimeoutError
	var canceledErr *temporal.CanceledError

	switch {
	case errors.As(err, &appErr):
		logger.Warn("Tool activity failed",
			"tool", toolName,
			"error_type", appErr.Type(),
			"non_retryable", appErr.NonRetryable())

		// Extract structured context from Details — never parse the message.
		var details models.ToolErrorDetails
		if appErr.HasDetails() {
			_ = appErr.Details(&details)
			reason = details.Reason
		}

	case errors.As(err, &timeoutErr):
		logger.Warn("Tool activity timed out",
			"tool", toolName,
			"timeout_type", timeoutErr.TimeoutType())
		reason = "tool execution timed out"

	case errors.As(err, &canceledErr):
		logger.Warn("Tool activity canceled", "tool", toolName)
		reason = "tool execution was canceled"

	default:
		logger.Error("Tool activity failed with unexpected error",
			"tool", toolName, "error", err)
		reason = "activity execution failed"
	}

	return activities.ToolActivityOutput{
		CallID:  callID,
		Content: reason,
		Success: &success,
	}
}

// resolveToolTimeout determines the StartToCloseTimeout for a tool activity.
//
// Priority:
//  1. timeout_ms argument from LLM (per-invocation override)
//  2. DefaultTimeoutMs from the tool's ToolSpec
//  3. DefaultToolTimeoutMs constant as a global fallback
//
// Maps to: codex-rs/core/src/exec.rs timeout resolution for tool commands
func resolveToolTimeout(specByName map[string]tools.ToolSpec, toolName string, args map[string]interface{}) time.Duration {
	// 1. Check for LLM-provided timeout_ms in arguments.
	if args != nil {
		if v, ok := args["timeout_ms"]; ok {
			if ms, ok := toInt64(v); ok && ms > 0 {
				return time.Duration(ms) * time.Millisecond
			}
		}
	}

	// 2. Use the tool spec's default timeout.
	if spec, ok := specByName[toolName]; ok && spec.DefaultTimeoutMs > 0 {
		return time.Duration(spec.DefaultTimeoutMs) * time.Millisecond
	}

	// 3. Global fallback.
	return time.Duration(tools.DefaultToolTimeoutMs) * time.Millisecond
}
