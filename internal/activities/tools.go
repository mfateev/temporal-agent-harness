package activities

import (
	"context"
	"errors"

	"github.com/mfateev/codex-temporal-go/internal/models"
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
// Only returned on successful activity completion. Infrastructure errors
// are returned as temporal.ApplicationError (retryable or non-retryable).
//
// Maps to: codex-rs/core/src/tools/router.rs ToolOutput + call_id
type ToolActivityOutput struct {
	CallID  string `json:"call_id"`
	Content string `json:"content,omitempty"`
	Success *bool  `json:"success,omitempty"`
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
// Error handling:
//   - Tool not found → non-retryable ApplicationError (ToolNotFound)
//   - Handler validation error → non-retryable ApplicationError (ToolValidation)
//   - Handler timeout → non-retryable ApplicationError (ToolTimeout)
//   - Tool runs but fails (e.g., command exits non-zero) → successful return with Success=false
//   - Tool runs successfully → successful return with Success=true
//
// Maps to: codex-rs/core/src/tools/router.rs ToolRouter.dispatch()
func (a *ToolActivities) ExecuteTool(ctx context.Context, input ToolActivityInput) (ToolActivityOutput, error) {
	handler, err := a.registry.GetHandler(input.ToolName)
	if err != nil {
		return ToolActivityOutput{}, models.NewToolNotFoundError(input.ToolName)
	}

	invocation := &tools.ToolInvocation{
		CallID:    input.CallID,
		ToolName:  input.ToolName,
		Arguments: input.Arguments,
	}

	output, err := handler.Handle(invocation)
	if err != nil {
		return ToolActivityOutput{}, classifyHandlerError(input.ToolName, err)
	}

	return ToolActivityOutput{
		CallID:  input.CallID,
		Content: output.Content,
		Success: output.Success,
	}, nil
}

// classifyHandlerError converts a handler error into the appropriate
// temporal.ApplicationError based on the error context.
//
// Currently all handler errors are non-retryable because they represent
// validation failures (missing args, bad types) or execution issues
// (timeouts) that won't resolve on retry. If a handler detects a
// transient issue, it should wrap it with tools.ErrTransient so this
// function can classify it as retryable.
func classifyHandlerError(toolName string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return models.NewToolTimeoutError(toolName, err)
	}

	// Default: treat handler errors as validation/execution errors (non-retryable).
	// The same invalid input will produce the same error on retry.
	return models.NewToolValidationError(toolName, err)
}
