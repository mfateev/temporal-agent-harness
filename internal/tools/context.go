// Package tools provides tool registry, routing, and handler specifications.
//
// Corresponds to: codex-rs/core/src/tools/
package tools

// ToolKind classifies the type of tool handler.
//
// Maps to: codex-rs/core/src/tools/registry.rs ToolKind
type ToolKind int

const (
	ToolKindFunction ToolKind = iota // Standard function tool
	ToolKindMcp                      // MCP server tool (future)
)

// ToolOutput represents the result of tool execution.
//
// Maps to: codex-rs/core/src/tools/router.rs ToolOutput::Function
type ToolOutput struct {
	Content string `json:"content"`
	Success *bool  `json:"success,omitempty"`
}

// ToolInvocation provides context for tool execution.
//
// Maps to: codex-rs/core/src/tools/context.rs ToolInvocation
type ToolInvocation struct {
	CallID    string                 `json:"call_id"`
	ToolName  string                 `json:"tool_name"`
	Arguments map[string]interface{} `json:"arguments"`
	// Future: Session context, turn context, diff tracker
}
