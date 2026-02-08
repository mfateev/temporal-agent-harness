package tools

import (
	"fmt"
)

// ToolHandler is the interface for tool implementations
//
// Maps to: codex-rs/core/src/tools/registry.rs ToolHandler trait
type ToolHandler interface {
	// Name returns the tool's name
	Name() string

	// Execute executes the tool with the given arguments
	Execute(args map[string]interface{}) (string, error)
}

// ToolRegistry manages available tools
//
// Maps to: codex-rs/core/src/tools/registry.rs ToolRegistry
type ToolRegistry struct {
	handlers map[string]ToolHandler
	specs    map[string]ToolSpec
}

// NewToolRegistry creates a new tool registry
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		handlers: make(map[string]ToolHandler),
		specs:    make(map[string]ToolSpec),
	}
}

// Register registers a tool handler and its specification
func (r *ToolRegistry) Register(handler ToolHandler, spec ToolSpec) {
	r.handlers[handler.Name()] = handler
	r.specs[handler.Name()] = spec
}

// GetHandler returns a tool handler by name
func (r *ToolRegistry) GetHandler(name string) (ToolHandler, error) {
	handler, ok := r.handlers[name]
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	return handler, nil
}

// GetToolSpecs returns all tool specifications (for LLM prompt)
func (r *ToolRegistry) GetToolSpecs() []ToolSpec {
	specs := make([]ToolSpec, 0, len(r.specs))
	for _, spec := range r.specs {
		specs = append(specs, spec)
	}
	return specs
}

// HasTool checks if a tool is registered
func (r *ToolRegistry) HasTool(name string) bool {
	_, ok := r.handlers[name]
	return ok
}

// ToolCount returns the number of registered tools
func (r *ToolRegistry) ToolCount() int {
	return len(r.handlers)
}
