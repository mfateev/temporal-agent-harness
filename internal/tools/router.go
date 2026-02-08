package tools

import "context"

// ToolRouter wraps a ToolRegistry with tool specifications for dispatch.
// Rebuilt per-turn with the current tool configuration.
//
// Maps to: codex-rs/core/src/tools/router.rs ToolRouter
type ToolRouter struct {
	registry *ToolRegistry
	specs    []ToolSpec
}

// NewToolRouter creates a new ToolRouter.
func NewToolRouter(registry *ToolRegistry, specs []ToolSpec) *ToolRouter {
	return &ToolRouter{
		registry: registry,
		specs:    specs,
	}
}

// GetToolSpecs returns the tool specifications for LLM prompt construction.
func (r *ToolRouter) GetToolSpecs() []ToolSpec {
	return r.specs
}

// DispatchToolCall dispatches a tool invocation to the appropriate handler.
//
// Maps to: codex-rs/core/src/tools/router.rs ToolRouter::dispatch_tool_call
func (r *ToolRouter) DispatchToolCall(ctx context.Context, invocation *ToolInvocation) (*ToolOutput, error) {
	handler, err := r.registry.GetHandler(invocation.ToolName)
	if err != nil {
		return nil, err
	}
	return handler.Handle(ctx, invocation)
}

// Registry returns the underlying ToolRegistry.
func (r *ToolRouter) Registry() *ToolRegistry {
	return r.registry
}
