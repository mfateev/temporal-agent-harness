package activities

import (
	"context"
	"fmt"

	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/tools"
)

// ToolActivityInput is the input for tool execution
//
// Maps to: codex-rs/core/src/tools/parallel.rs ToolCall
type ToolActivityInput struct {
	ToolCall models.ToolCall `json:"tool_call"`
}

// ToolActivityOutput is the output from tool execution
//
// Maps to: codex-rs/core/src/tools/types.rs ToolResult
type ToolActivityOutput struct {
	ToolCallID string `json:"tool_call_id"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ToolActivities contains tool-related activities
type ToolActivities struct {
	registry *tools.ToolRegistry
}

// NewToolActivities creates a new ToolActivities instance
func NewToolActivities(registry *tools.ToolRegistry) *ToolActivities {
	return &ToolActivities{
		registry: registry,
	}
}

// ExecuteTool executes a single tool call
//
// Maps to: codex-rs/core/src/tools/router.rs ToolRouter.dispatch()
func (a *ToolActivities) ExecuteTool(ctx context.Context, input ToolActivityInput) (ToolActivityOutput, error) {
	toolCall := input.ToolCall

	// Get tool handler
	handler, err := a.registry.GetHandler(toolCall.Name)
	if err != nil {
		// Tool not found - return as tool error (not activity error)
		return ToolActivityOutput{
			ToolCallID: toolCall.ID,
			Error:      fmt.Sprintf("Tool not found: %s", toolCall.Name),
		}, nil
	}

	// Execute tool
	output, err := handler.Execute(toolCall.Arguments)
	if err != nil {
		// Tool execution failed - return as tool error (not activity error)
		return ToolActivityOutput{
			ToolCallID: toolCall.ID,
			Error:      err.Error(),
		}, nil
	}

	// Success
	return ToolActivityOutput{
		ToolCallID: toolCall.ID,
		Output:     output,
	}, nil
}
