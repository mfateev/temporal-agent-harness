package tools

import "fmt"

// ToolHandler is the interface for tool implementations.
//
// Maps to: codex-rs/core/src/tools/registry.rs ToolHandler trait
type ToolHandler interface {
	// Name returns the tool's name.
	Name() string

	// Kind returns the tool handler kind (Function, Mcp, etc.).
	// Maps to: codex-rs ToolHandler::kind()
	Kind() ToolKind

	// IsMutating returns whether this invocation may modify the environment.
	// Used by Codex for parallel vs sequential gating (read lock vs write lock).
	// Maps to: codex-rs ToolHandler::is_mutating()
	IsMutating(invocation *ToolInvocation) bool

	// Handle executes the tool with the given invocation context.
	// Maps to: codex-rs ToolHandler::handle()
	Handle(invocation *ToolInvocation) (*ToolOutput, error)
}

// ToolRegistry stores tool handlers by name.
//
// Maps to: codex-rs/core/src/tools/registry.rs ToolRegistry
type ToolRegistry struct {
	handlers map[string]ToolHandler
}

// NewToolRegistry creates a new tool registry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		handlers: make(map[string]ToolHandler),
	}
}

// Register registers a tool handler.
func (r *ToolRegistry) Register(handler ToolHandler) {
	r.handlers[handler.Name()] = handler
}

// GetHandler returns a tool handler by name.
func (r *ToolRegistry) GetHandler(name string) (ToolHandler, error) {
	handler, ok := r.handlers[name]
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	return handler, nil
}

// HasTool checks if a tool is registered.
func (r *ToolRegistry) HasTool(name string) bool {
	_, ok := r.handlers[name]
	return ok
}

// ToolCount returns the number of registered tools.
func (r *ToolRegistry) ToolCount() int {
	return len(r.handlers)
}
