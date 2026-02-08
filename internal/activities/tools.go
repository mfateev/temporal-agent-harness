package activities

import (
	"context"
	"fmt"

	"github.com/mfateev/codex-temporal-go/internal/tools"
)

// ToolActivityInput is the input for tool execution.
//
// Maps to: codex-rs/core/src/tools/context.rs ToolInvocation fields
type ToolActivityInput struct {
	CallID    string                 `json:"call_id"`
	ToolName  string                 `json:"tool_name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ToolActivityOutput is the output from tool execution.
//
// Maps to: codex-rs/core/src/tools/router.rs ToolOutput + call_id
type ToolActivityOutput struct {
	CallID  string `json:"call_id"`
	Content string `json:"content,omitempty"`
	Success *bool  `json:"success,omitempty"`
	Error   string `json:"error,omitempty"` // Infrastructure error only
}

// ToolActivities contains tool-related activities.
type ToolActivities struct {
	registry *tools.ToolRegistry
}

// NewToolActivities creates a new ToolActivities instance.
func NewToolActivities(registry *tools.ToolRegistry) *ToolActivities {
	return &ToolActivities{registry: registry}
}

// ExecuteTool executes a single tool call.
//
// Maps to: codex-rs/core/src/tools/router.rs ToolRouter.dispatch()
func (a *ToolActivities) ExecuteTool(ctx context.Context, input ToolActivityInput) (ToolActivityOutput, error) {
	handler, err := a.registry.GetHandler(input.ToolName)
	if err != nil {
		return ToolActivityOutput{
			CallID: input.CallID,
			Error:  fmt.Sprintf("Tool not found: %s", input.ToolName),
		}, nil
	}

	invocation := &tools.ToolInvocation{
		CallID:    input.CallID,
		ToolName:  input.ToolName,
		Arguments: input.Arguments,
	}

	output, err := handler.Handle(invocation)
	if err != nil {
		return ToolActivityOutput{
			CallID: input.CallID,
			Error:  err.Error(),
		}, nil
	}

	return ToolActivityOutput{
		CallID:  input.CallID,
		Content: output.Content,
		Success: output.Success,
	}, nil
}
