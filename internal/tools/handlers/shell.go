// Package handlers contains built-in tool handler implementations.
//
// Corresponds to: codex-rs/core/src/tools/handlers/
package handlers

import (
	"bytes"
	"context"
	"os"
	"os/exec"

	"github.com/mfateev/codex-temporal-go/internal/command_safety"
	execpkg "github.com/mfateev/codex-temporal-go/internal/exec"
	"github.com/mfateev/codex-temporal-go/internal/execenv"
	"github.com/mfateev/codex-temporal-go/internal/sandbox"
	"github.com/mfateev/codex-temporal-go/internal/tools"
)

// ShellTool executes shell commands.
//
// Maps to: codex-rs/core/src/tools/handlers/shell.rs
type ShellTool struct {
	sandboxMgr sandbox.SandboxManager
}

// NewShellTool creates a new shell tool handler.
func NewShellTool() *ShellTool {
	return &ShellTool{sandboxMgr: sandbox.NewNoopSandboxManager()}
}

// NewShellToolWithSandbox creates a shell tool handler with a sandbox manager.
func NewShellToolWithSandbox(mgr sandbox.SandboxManager) *ShellTool {
	return &ShellTool{sandboxMgr: mgr}
}

// Name returns the tool's name.
func (t *ShellTool) Name() string {
	return "shell"
}

// Kind returns ToolKindFunction.
func (t *ShellTool) Kind() tools.ToolKind {
	return tools.ToolKindFunction
}

// IsMutating returns true if the command might modify the environment.
// Uses command safety classification to identify read-only commands.
//
// Maps to: codex-rs/core/src/tools/handlers/shell.rs is_mutating
func (t *ShellTool) IsMutating(invocation *tools.ToolInvocation) bool {
	commandArg, ok := invocation.Arguments["command"]
	if !ok {
		return true // Can't determine safety without a command
	}
	command, ok := commandArg.(string)
	if !ok || command == "" {
		return true
	}
	cmdVec := []string{"bash", "-c", command}
	return !command_safety.IsKnownSafeCommand(cmdVec)
}

// Handle executes a shell command. Timeout is managed by Temporal's
// StartToCloseTimeout on the activity options — the context is cancelled
// when the timeout fires, and Temporal retries per the RetryPolicy.
//
// If a SandboxPolicy is set on the invocation, the command is wrapped
// through the SandboxManager before execution.
//
// Maps to: codex-rs/core/src/tools/handlers/shell.rs handle
func (t *ShellTool) Handle(ctx context.Context, invocation *tools.ToolInvocation) (*tools.ToolOutput, error) {
	commandArg, ok := invocation.Arguments["command"]
	if !ok {
		return nil, tools.NewValidationError("missing required argument: command")
	}

	command, ok := commandArg.(string)
	if !ok {
		return nil, tools.NewValidationError("command must be a string")
	}

	if command == "" {
		return nil, tools.NewValidationError("command cannot be empty")
	}

	// Build the command spec and apply sandbox if configured
	spec := sandbox.CommandSpec{
		Program: "bash",
		Args:    []string{"-c", command},
		Cwd:     invocation.Cwd,
	}

	execEnv, err := t.resolveExecEnv(spec, invocation.SandboxPolicy)
	if err != nil {
		return nil, tools.NewValidationError("sandbox setup failed: " + err.Error())
	}

	cmd := exec.CommandContext(ctx, execEnv.Command[0], execEnv.Command[1:]...)
	if execEnv.Cwd != "" {
		cmd.Dir = execEnv.Cwd
	}

	// Apply environment variable filtering if an env policy is set.
	// When a policy is present, we clear the inherited env and use the filtered set.
	if invocation.EnvPolicy != nil {
		filteredEnv := resolveFilteredEnv(invocation.EnvPolicy)
		cmd.Env = execenv.EnvMapToSlice(filteredEnv)
	}

	// Apply sandbox environment variables (merged on top of any filtered env)
	if len(execEnv.Env) > 0 {
		if cmd.Env == nil {
			cmd.Env = os.Environ() // start from current env if not already filtered
		}
		cmd.Env = appendEnvMap(cmd.Env, execEnv.Env)
	}

	// Capture stdout and stderr separately for smart aggregation with output limiting.
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err = cmd.Run()

	// Aggregate and limit output.
	output := execpkg.AggregateOutput(stdoutBuf.Bytes(), stderrBuf.Bytes())

	if err != nil {
		if ctx.Err() != nil {
			// Context cancelled or deadline exceeded — let Temporal handle retry.
			return nil, ctx.Err()
		}
		// Command failed but produced output - return as tool result with Success=false
		success := false
		return &tools.ToolOutput{
			Content: string(output),
			Success: &success,
		}, nil
	}

	success := true
	return &tools.ToolOutput{
		Content: string(output),
		Success: &success,
	}, nil
}

// resolveExecEnv applies sandbox wrapping if a policy is set.
func (t *ShellTool) resolveExecEnv(spec sandbox.CommandSpec, policyRef *tools.SandboxPolicyRef) (*sandbox.ExecEnv, error) {
	if policyRef == nil || t.sandboxMgr == nil {
		return &sandbox.ExecEnv{
			Command: append([]string{spec.Program}, spec.Args...),
			Cwd:     spec.Cwd,
		}, nil
	}

	policy := sandboxPolicyRefToPolicy(policyRef)
	return t.sandboxMgr.Transform(spec, policy)
}

// sandboxPolicyRefToPolicy converts the serializable ref to a sandbox.SandboxPolicy.
func sandboxPolicyRefToPolicy(ref *tools.SandboxPolicyRef) *sandbox.SandboxPolicy {
	if ref == nil {
		return nil
	}
	roots := make([]sandbox.WritableRoot, len(ref.WritableRoots))
	for i, r := range ref.WritableRoots {
		roots[i] = sandbox.WritableRoot(r)
	}
	return &sandbox.SandboxPolicy{
		Mode:          sandbox.SandboxMode(ref.Mode),
		WritableRoots: roots,
		NetworkAccess: ref.NetworkAccess,
	}
}

// resolveFilteredEnv converts an EnvPolicyRef to a filtered environment map.
func resolveFilteredEnv(ref *tools.EnvPolicyRef) map[string]string {
	if ref == nil {
		return nil
	}
	policy := &execenv.ShellEnvironmentPolicy{
		Inherit:               execenv.Inherit(ref.Inherit),
		IgnoreDefaultExcludes: ref.IgnoreDefaultExcludes,
		Exclude:               ref.Exclude,
		Set:                   ref.Set,
		IncludeOnly:           ref.IncludeOnly,
	}
	return execenv.CreateEnv(policy)
}

// appendEnvMap appends key=value pairs from a map to an env slice.
func appendEnvMap(base []string, envMap map[string]string) []string {
	for k, v := range envMap {
		base = append(base, k+"="+v)
	}
	return base
}
