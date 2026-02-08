// Package handlers contains built-in tool handler implementations.
//
// Corresponds to: codex-rs/core/src/tools/handlers/
package handlers

import (
	"bytes"
	"context"
	"os/exec"

	"github.com/mfateev/codex-temporal-go/internal/command_safety"
	execpkg "github.com/mfateev/codex-temporal-go/internal/exec"
	"github.com/mfateev/codex-temporal-go/internal/tools"
)

// ShellTool executes shell commands.
//
// Maps to: codex-rs/core/src/tools/handlers/shell.rs
type ShellTool struct{}

// NewShellTool creates a new shell tool handler.
func NewShellTool() *ShellTool {
	return &ShellTool{}
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

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	if invocation.Cwd != "" {
		cmd.Dir = invocation.Cwd
	}

	// Capture stdout and stderr separately for smart aggregation with output limiting.
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()

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
