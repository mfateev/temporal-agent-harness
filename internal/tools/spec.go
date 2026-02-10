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
	DefaultShellTimeoutMs      = 10_000  // 10s — matches Codex default
	DefaultReadFileTimeoutMs   = 30_000  // 30s
	DefaultApplyPatchTimeoutMs = 30_000  // 30s
	DefaultWriteFileTimeoutMs  = 30_000  // 30s
	DefaultListDirTimeoutMs    = 30_000  // 30s
	DefaultGrepFilesTimeoutMs  = 30_000  // 30s — matches Codex COMMAND_TIMEOUT
	DefaultToolTimeoutMs       = 120_000 // 2min — fallback for tools without a default
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
			{
				Name:        "working_directory",
				Type:        "string",
				Description: "Directory to execute the command in. Defaults to the session working directory.",
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

// NewApplyPatchToolSpec creates the specification for the apply_patch tool.
//
// Maps to: codex-rs/core/src/tools/handlers/apply_patch.rs create_apply_patch_json_tool
func NewApplyPatchToolSpec() ToolSpec {
	return ToolSpec{
		Name: "apply_patch",
		Description: `Use the apply_patch tool to edit files.
Your patch language is a stripped-down, file-oriented diff format designed to be easy to parse and safe to apply. You can think of it as a high-level envelope:

*** Begin Patch
[ one or more file sections ]
*** End Patch

Within that envelope, you get a sequence of file operations.
You MUST include a header to specify the action you are taking.
Each operation starts with one of three headers:

*** Add File: <path> - create a new file. Every following line is a + line (the initial contents).
*** Delete File: <path> - remove an existing file. Nothing follows.
*** Update File: <path> - patch an existing file in place (optionally with a rename).

May be immediately followed by *** Move to: <new path> if you want to rename the file.
Then one or more "hunks", each introduced by @@ (optionally followed by a hunk header).
Within a hunk each line starts with ' ' (context), '-' (removed), or '+' (added).

For context: show 3 lines of code immediately above and 3 lines immediately below each change. If 3 lines of context is insufficient to uniquely identify the snippet of code within the file, use the @@ operator to indicate the class or function to which the snippet belongs.

The full grammar:
Patch := Begin { FileOp } End
Begin := "*** Begin Patch" NEWLINE
End := "*** End Patch" NEWLINE
FileOp := AddFile | DeleteFile | UpdateFile
AddFile := "*** Add File: " path NEWLINE { "+" line NEWLINE }
DeleteFile := "*** Delete File: " path NEWLINE
UpdateFile := "*** Update File: " path NEWLINE [ MoveTo ] { Hunk }
MoveTo := "*** Move to: " newPath NEWLINE
Hunk := "@@" [ header ] NEWLINE { HunkLine } [ "*** End of File" NEWLINE ]
HunkLine := (" " | "-" | "+") text NEWLINE`,
		Parameters: []ToolParameter{
			{
				Name:        "input",
				Type:        "string",
				Description: "The entire contents of the apply_patch command",
				Required:    true,
			},
		},
		DefaultTimeoutMs: DefaultApplyPatchTimeoutMs,
	}
}

// NewWriteFileToolSpec creates the specification for the write_file tool.
//
// This is a new addition (not ported from Codex Rust, which routes all
// file writes through apply_patch).
func NewWriteFileToolSpec() ToolSpec {
	return ToolSpec{
		Name:        "write_file",
		Description: "Create or overwrite a file with the given content. Parent directories are created automatically if they don't exist.",
		Parameters: []ToolParameter{
			{
				Name:        "path",
				Type:        "string",
				Description: "The path to the file to write",
				Required:    true,
			},
			{
				Name:        "content",
				Type:        "string",
				Description: "The content to write to the file",
				Required:    true,
			},
		},
		DefaultTimeoutMs: DefaultWriteFileTimeoutMs,
	}
}

// NewListDirToolSpec creates the specification for the list_dir tool.
//
// Maps to: codex-rs/core/src/tools/spec.rs create_list_dir_tool
func NewListDirToolSpec() ToolSpec {
	return ToolSpec{
		Name:        "list_dir",
		Description: "Lists entries in a local directory with 1-indexed entry numbers and simple type labels.",
		Parameters: []ToolParameter{
			{
				Name:        "dir_path",
				Type:        "string",
				Description: "Absolute path to the directory to list.",
				Required:    true,
			},
			{
				Name:        "offset",
				Type:        "number",
				Description: "The entry number to start listing from. Must be 1 or greater.",
				Required:    false,
			},
			{
				Name:        "limit",
				Type:        "number",
				Description: "The maximum number of entries to return.",
				Required:    false,
			},
			{
				Name:        "depth",
				Type:        "number",
				Description: "The maximum directory depth to traverse. Must be 1 or greater.",
				Required:    false,
			},
		},
		DefaultTimeoutMs: DefaultListDirTimeoutMs,
	}
}

// NewGrepFilesToolSpec creates the specification for the grep_files tool.
//
// Maps to: codex-rs/core/src/tools/spec.rs create_grep_files_tool
func NewGrepFilesToolSpec() ToolSpec {
	return ToolSpec{
		Name:        "grep_files",
		Description: "Finds files whose contents match the pattern and lists them by modification time.",
		Parameters: []ToolParameter{
			{
				Name:        "pattern",
				Type:        "string",
				Description: "Regular expression pattern to search for.",
				Required:    true,
			},
			{
				Name:        "include",
				Type:        "string",
				Description: "Optional glob that limits which files are searched (e.g. \"*.rs\" or \"*.{ts,tsx}\").",
				Required:    false,
			},
			{
				Name:        "path",
				Type:        "string",
				Description: "Directory or file path to search in. Defaults to the current working directory.",
				Required:    false,
			},
			{
				Name:        "limit",
				Type:        "number",
				Description: "Maximum number of file paths to return (defaults to 100).",
				Required:    false,
			},
		},
		DefaultTimeoutMs: DefaultGrepFilesTimeoutMs,
	}
}
