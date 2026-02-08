// Package handlers contains built-in tool handler implementations.
//
// Corresponds to: codex-rs/core/src/tools/handlers/
package handlers

import (
	"context"
	"fmt"
	"os/exec"
	"time"

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

// IsMutating returns true - shell commands can modify the environment.
//
// Maps to: codex-rs/core/src/tools/handlers/shell.rs is_mutating
func (t *ShellTool) IsMutating(invocation *tools.ToolInvocation) bool {
	return true
}

// Handle executes a shell command with a 5-minute timeout.
//
// Maps to: codex-rs/core/src/tools/handlers/shell.rs handle
func (t *ShellTool) Handle(invocation *tools.ToolInvocation) (*tools.ToolOutput, error) {
	commandArg, ok := invocation.Arguments["command"]
	if !ok {
		return nil, fmt.Errorf("missing required argument: command")
	}

	command, ok := commandArg.(string)
	if !ok {
		return nil, fmt.Errorf("command must be a string")
	}

	if command == "" {
		return nil, fmt.Errorf("command cannot be empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	output, err := cmd.CombinedOutput()

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("command timed out after 5 minutes")
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
