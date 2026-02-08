package handlers

import (
	"context"
	"os"

	"github.com/mfateev/codex-temporal-go/internal/tools"
	"github.com/mfateev/codex-temporal-go/internal/tools/patch"
)

// ApplyPatchTool applies structured file patches.
//
// Maps to: codex-rs/core/src/tools/handlers/apply_patch.rs ApplyPatchHandler
type ApplyPatchTool struct{}

// NewApplyPatchTool creates a new apply_patch tool handler.
func NewApplyPatchTool() *ApplyPatchTool {
	return &ApplyPatchTool{}
}

// Name returns the tool's name.
func (t *ApplyPatchTool) Name() string {
	return "apply_patch"
}

// Kind returns ToolKindFunction.
func (t *ApplyPatchTool) Kind() tools.ToolKind {
	return tools.ToolKindFunction
}

// IsMutating returns true - apply_patch always modifies the environment.
//
// Maps to: codex-rs/core/src/tools/handlers/apply_patch.rs is_mutating
func (t *ApplyPatchTool) IsMutating(invocation *tools.ToolInvocation) bool {
	return true
}

// Handle parses the patch from the "input" argument and applies it to the filesystem.
//
// Maps to: codex-rs/core/src/tools/handlers/apply_patch.rs handle
func (t *ApplyPatchTool) Handle(_ context.Context, invocation *tools.ToolInvocation) (*tools.ToolOutput, error) {
	inputArg, ok := invocation.Arguments["input"]
	if !ok {
		return nil, tools.NewValidationError("missing required argument: input")
	}

	input, ok := inputArg.(string)
	if !ok {
		return nil, tools.NewValidationError("input must be a string")
	}

	if input == "" {
		return nil, tools.NewValidationError("input cannot be empty")
	}

	// Use the current working directory as the base for resolving relative paths.
	cwd, err := os.Getwd()
	if err != nil {
		success := false
		return &tools.ToolOutput{
			Content: "Failed to determine working directory: " + err.Error(),
			Success: &success,
		}, nil
	}

	result, err := patch.Apply(input, cwd)
	if err != nil {
		success := false
		return &tools.ToolOutput{
			Content: err.Error(),
			Success: &success,
		}, nil
	}

	success := true
	return &tools.ToolOutput{
		Content: result,
		Success: &success,
	}, nil
}
