package tools

func init() {
	RegisterSpec(SpecEntry{Name: "exec_command", Constructor: NewExecCommandToolSpec})
	RegisterSpec(SpecEntry{Name: "write_stdin", Constructor: NewWriteStdinToolSpec})
}

// Default timeouts for exec tools.
const (
	// DefaultExecCommandTimeoutMs covers max yield (30s) + overhead.
	DefaultExecCommandTimeoutMs = 45_000
	// DefaultWriteStdinTimeoutMs covers max yield (30s) + overhead.
	DefaultWriteStdinTimeoutMs = 45_000
)

// NewExecCommandToolSpec creates the specification for the exec_command tool.
// Runs a command in a PTY or pipes, returning output or a session ID for
// ongoing interaction via write_stdin.
//
// Maps to: codex-rs/core/src/tools/spec.rs create_exec_command_tool
func NewExecCommandToolSpec() ToolSpec {
	params := []ToolParameter{
		{
			Name:        "cmd",
			Type:        "string",
			Description: "Shell command to execute.",
			Required:    true,
		},
		{
			Name:        "workdir",
			Type:        "string",
			Description: "Optional working directory to run the command in; defaults to the turn cwd.",
			Required:    false,
		},
		{
			Name:        "shell",
			Type:        "string",
			Description: "Shell binary to launch. Defaults to the user's default shell.",
			Required:    false,
		},
		{
			Name:        "login",
			Type:        "boolean",
			Description: "Whether to launch the shell as a login shell. Defaults to true.",
			Required:    false,
		},
		{
			Name:        "tty",
			Type:        "boolean",
			Description: "Whether to run in a PTY (interactive) or pipes (non-interactive). Defaults to false.",
			Required:    false,
		},
		{
			Name:        "yield_time_ms",
			Type:        "number",
			Description: "How long to wait (in milliseconds) for output before yielding. Defaults to 10000. Range: 250-30000.",
			Required:    false,
		},
		{
			Name:        "max_output_tokens",
			Type:        "number",
			Description: "Maximum number of tokens to return. Excess output will be truncated.",
			Required:    false,
		},
	}
	params = append(params, approvalParameters(false)...)

	return ToolSpec{
		Name: "exec_command",
		Description: `Runs a command in a PTY, returning output or a session ID for ongoing interaction.
- For short commands, the output and exit code are returned immediately.
- For long-running commands, a session_id is returned. Use write_stdin to send further input and poll for output.
- Set tty=true for interactive commands (REPLs, editors) that need terminal emulation.
- yield_time_ms controls how long to wait for initial output (default 10s, max 30s).`,
		Parameters:       params,
		DefaultTimeoutMs: DefaultExecCommandTimeoutMs,
	}
}

// NewWriteStdinToolSpec creates the specification for the write_stdin tool.
// Writes characters to an existing exec session and returns recent output.
//
// Maps to: codex-rs/core/src/tools/spec.rs create_write_stdin_tool
func NewWriteStdinToolSpec() ToolSpec {
	return ToolSpec{
		Name: "write_stdin",
		Description: `Writes characters to an existing unified exec session and returns recent output.
- Use session_id from a previous exec_command call.
- Send empty chars to poll for new output without sending input.
- yield_time_ms controls how long to wait for output (default 250ms for writes, min 5000ms for empty polls).`,
		Parameters: []ToolParameter{
			{
				Name:        "session_id",
				Type:        "number",
				Description: "Identifier of the running unified exec session.",
				Required:    true,
			},
			{
				Name:        "chars",
				Type:        "string",
				Description: "Bytes to write to stdin (may be empty to poll for output).",
				Required:    false,
			},
			{
				Name:        "yield_time_ms",
				Type:        "number",
				Description: "How long to wait (in milliseconds) for output before yielding. Defaults to 250.",
				Required:    false,
			},
			{
				Name:        "max_output_tokens",
				Type:        "number",
				Description: "Maximum number of tokens to return. Excess output will be truncated.",
				Required:    false,
			},
		},
		DefaultTimeoutMs: DefaultWriteStdinTimeoutMs,
	}
}
