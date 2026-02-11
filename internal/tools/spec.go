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
	Name        string                 `json:"name"`
	Type        string                 `json:"type"`
	Description string                 `json:"description"`
	Required    bool                   `json:"required"`
	Items       map[string]interface{} `json:"items,omitempty"` // For array types: JSON schema of array items
}

// NewShellToolSpec creates the specification for the shell tool.
//
// Maps to: codex-rs/core/src/tools/spec.rs create_shell_command_tool
func NewShellToolSpec() ToolSpec {
	return ToolSpec{
		Name: "shell",
		Description: `Runs a shell command and returns its output.
- Always set the ` + "`workdir`" + ` param when using the shell function. Do not use ` + "`cd`" + ` unless absolutely necessary.`,
		Parameters: []ToolParameter{
			{
				Name:        "command",
				Type:        "string",
				Description: "The shell script to execute in the user's default shell",
				Required:    true,
			},
			{
				Name:        "workdir",
				Type:        "string",
				Description: "The working directory to execute the command in",
				Required:    false,
			},
			{
				Name:        "timeout_ms",
				Type:        "number",
				Description: "The timeout for the command in milliseconds",
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
// Maps to: codex-rs/core/src/tools/spec.rs create_read_file_tool
func NewReadFileToolSpec() ToolSpec {
	return ToolSpec{
		Name:        "read_file",
		Description: "Reads a local file with 1-indexed line numbers, supporting slice and indentation-aware block modes.",
		Parameters: []ToolParameter{
			{
				Name:        "file_path",
				Type:        "string",
				Description: "Absolute path to the file",
				Required:    true,
			},
			{
				Name:        "offset",
				Type:        "number",
				Description: "The line number to start reading from. Must be 1 or greater.",
				Required:    false,
			},
			{
				Name:        "limit",
				Type:        "number",
				Description: "The maximum number of lines to return.",
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
Within a hunk each line starts with:

For instructions on [context_before] and [context_after]:
- By default, show 3 lines of code immediately above and 3 lines immediately below each change. If a change is within 3 lines of a previous change, do NOT duplicate the first change's [context_after] lines in the second change's [context_before] lines.
- If 3 lines of context is insufficient to uniquely identify the snippet of code within the file, use the @@ operator to indicate the class or function to which the snippet belongs. For instance, we might have:
@@ class BaseClass
[3 lines of pre-context]
- [old_code]
+ [new_code]
[3 lines of post-context]

- If a code block is repeated so many times in a class or function such that even a single @@ statement and 3 lines of context cannot uniquely identify the snippet of code, you can use multiple @@ statements to jump to the right context. For instance:

@@ class BaseClass
@@   def method():
[3 lines of pre-context]
- [old_code]
+ [new_code]
[3 lines of post-context]

The full grammar definition is below:
Patch := Begin { FileOp } End
Begin := "*** Begin Patch" NEWLINE
End := "*** End Patch" NEWLINE
FileOp := AddFile | DeleteFile | UpdateFile
AddFile := "*** Add File: " path NEWLINE { "+" line NEWLINE }
DeleteFile := "*** Delete File: " path NEWLINE
UpdateFile := "*** Update File: " path NEWLINE [ MoveTo ] { Hunk }
MoveTo := "*** Move to: " newPath NEWLINE
Hunk := "@@" [ header ] NEWLINE { HunkLine } [ "*** End of File" NEWLINE ]
HunkLine := (" " | "-" | "+") text NEWLINE

A full patch can combine several operations:

*** Begin Patch
*** Add File: hello.txt
+Hello world
*** Update File: src/app.py
*** Move to: src/main.py
@@ def greet():
-print("Hi")
+print("Hello, world!")
*** Delete File: obsolete.txt
*** End Patch

It is important to remember:

- You must include a header with your intended action (Add/Delete/Update)
- You must prefix new lines with + even when creating a new file
- File references can only be relative, NEVER ABSOLUTE.`,
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

// NewRequestUserInputToolSpec creates the specification for the request_user_input tool.
// This tool is intercepted by the workflow (not dispatched as an activity).
//
// Maps to: codex-rs/protocol/src/request_user_input.rs
func NewRequestUserInputToolSpec() ToolSpec {
	return ToolSpec{
		Name:        "request_user_input",
		Description: "Ask the user one or more multi-choice questions. Each question has a list of options with label and description. Use this when you need clarification or a decision from the user.",
		Parameters: []ToolParameter{
			{
				Name:        "questions",
				Type:        "array",
				Description: "Array of questions. Max 4 questions.",
				Required:    true,
				Items: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id": map[string]interface{}{
							"type":        "string",
							"description": "Unique identifier for this question",
						},
						"question": map[string]interface{}{
							"type":        "string",
							"description": "The question text to display to the user",
						},
						"options": map[string]interface{}{
							"type":        "array",
							"description": "Available choices for this question",
							"items": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"label": map[string]interface{}{
										"type":        "string",
										"description": "Short display text for this option",
									},
									"description": map[string]interface{}{
										"type":        "string",
										"description": "Explanation of what this option means",
									},
								},
								"required": []string{"label"},
							},
						},
					},
					"required": []string{"id", "question", "options"},
				},
			},
		},
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
