// Package tools provides tool registry, routing, and handler specifications.
//
// Corresponds to: codex-rs/core/src/tools/
// - registry.rs (tool handler registry)
// - router.rs (tool dispatch and routing)
// - spec.rs (tool specifications)
// - context.rs (tool invocation context)
package tools

// ToolSpec defines the specification for a tool (sent to LLM in prompt).
//
// Maps to: codex-rs/core/src/tools/spec.rs ToolSpec::Function
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  []ToolParameter `json:"parameters"`
}

// ToolParameter defines a parameter for a tool.
type ToolParameter struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// NewShellToolSpec creates the specification for the shell tool.
//
// Maps to: codex-rs/core/src/tools/handlers/shell.rs tool spec
func NewShellToolSpec() ToolSpec {
	return ToolSpec{
		Name:        "shell",
		Description: "Execute a shell command and return the output. Use this to run bash commands, list files, read command output, etc.",
		Parameters: []ToolParameter{
			{
				Name:        "command",
				Type:        "string",
				Description: "The shell command to execute (will be run with bash -c)",
				Required:    true,
			},
		},
	}
}

// NewReadFileToolSpec creates the specification for the read_file tool.
//
// Maps to: codex-rs/core/src/tools/handlers/read_file.rs tool spec
func NewReadFileToolSpec() ToolSpec {
	return ToolSpec{
		Name:        "read_file",
		Description: "Read the contents of a file. Returns the file content with line numbers.",
		Parameters: []ToolParameter{
			{
				Name:        "path",
				Type:        "string",
				Description: "The path to the file to read",
				Required:    true,
			},
			{
				Name:        "offset",
				Type:        "integer",
				Description: "Starting line number (0-indexed, optional)",
				Required:    false,
			},
			{
				Name:        "limit",
				Type:        "integer",
				Description: "Maximum number of lines to read (optional)",
				Required:    false,
			},
		},
	}
}
