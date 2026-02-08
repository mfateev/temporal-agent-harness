// Package tools provides tool registry, routing, and handler specifications.
//
// Corresponds to: codex-rs/core/src/tools/
// - registry.rs (tool handler registry)
// - router.rs (tool dispatch and routing)
// - spec.rs (tool specifications)
// - context.rs (tool invocation context)
package tools

// Default timeouts in milliseconds.
// Maps to: codex-rs/core/src/exec.rs DEFAULT_EXEC_COMMAND_TIMEOUT_MS
const (
	DefaultShellTimeoutMs    = 10_000  // 10s — matches Codex default
	DefaultReadFileTimeoutMs = 30_000  // 30s
	DefaultToolTimeoutMs     = 120_000 // 2min — fallback for tools without a default
)

// ToolSpec defines the specification for a tool (sent to LLM in prompt).
//
// Maps to: codex-rs/core/src/tools/spec.rs ToolSpec::Function
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  []ToolParameter `json:"parameters"`

	// DefaultTimeoutMs is the default StartToCloseTimeout for this tool's
	// activity when the LLM does not provide a timeout_ms argument.
	// Tools that expose timeout_ms as a parameter let the LLM override this.
	DefaultTimeoutMs int64 `json:"-"` // not sent to LLM
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
// Maps to: codex-rs/core/src/tools/spec.rs shell tool spec
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
			{
				Name:        "timeout_ms",
				Type:        "number",
				Description: "The timeout for the command in milliseconds. Defaults to 10000 (10s). Use longer timeouts for builds, installs, or test suites.",
				Required:    false,
			},
		},
		DefaultTimeoutMs: DefaultShellTimeoutMs,
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
		DefaultTimeoutMs: DefaultReadFileTimeoutMs,
	}
}
