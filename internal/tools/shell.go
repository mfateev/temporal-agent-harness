package tools

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// ShellTool executes shell commands
//
// Maps to: codex-rs/core/src/tools/handlers/shell.rs
type ShellTool struct{}

// NewShellTool creates a new shell tool
func NewShellTool() *ShellTool {
	return &ShellTool{}
}

// Name returns the tool's name
func (t *ShellTool) Name() string {
	return "shell"
}

// Execute executes a shell command with a 5-minute timeout
func (t *ShellTool) Execute(args map[string]interface{}) (string, error) {
	// Extract command argument
	commandArg, ok := args["command"]
	if !ok {
		return "", fmt.Errorf("missing required argument: command")
	}

	command, ok := commandArg.(string)
	if !ok {
		return "", fmt.Errorf("command must be a string")
	}

	if command == "" {
		return "", fmt.Errorf("command cannot be empty")
	}

	// Create context with 5-minute timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Execute command with bash -c
	cmd := exec.CommandContext(ctx, "bash", "-c", command)

	// Capture combined output (stdout + stderr)
	output, err := cmd.CombinedOutput()

	if err != nil {
		// Check if timeout
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("command timed out after 5 minutes")
		}

		// Include error with output
		return string(output), fmt.Errorf("command failed: %w", err)
	}

	return string(output), nil
}
