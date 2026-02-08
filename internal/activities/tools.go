package activities

import (
	"context"

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
//   - Handler context cancelled/timeout → returned as-is; Temporal retries per RetryPolicy
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

	// Pass the activity context to the handler. Temporal manages timeouts
	// via StartToCloseTimeout — when it fires, ctx is cancelled, the handler
	// returns ctx.Err(), and Temporal retries per the RetryPolicy.
	output, err := handler.Handle(ctx, invocation)
	if err != nil {
		// Context errors (deadline/cancellation) are returned as-is so
		// Temporal recognizes them and applies the retry policy.
		if ctx.Err() != nil {
			return ToolActivityOutput{}, ctx.Err()
		}
		// All other handler errors are validation failures (non-retryable).
		return ToolActivityOutput{}, models.NewToolValidationError(input.ToolName, err)
	}

	return ToolActivityOutput{
		CallID:  input.CallID,
		Content: output.Content,
		Success: output.Success,
	}, nil
}
