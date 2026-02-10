package models

// ModelConfig configures the LLM model parameters
//
// Maps to: codex-rs/core/src/codex.rs SessionConfiguration (model config part)
type ModelConfig struct {
	Provider      string  `json:"provider"`       // "openai" or "anthropic"
	Model         string  `json:"model"`          // e.g., "gpt-4o", "claude-sonnet-4.5-20250929"
	Temperature   float64 `json:"temperature"`    // 0.0 to 2.0
	MaxTokens     int     `json:"max_tokens"`     // Max tokens to generate
	ContextWindow int     `json:"context_window"` // Max context window size
}

// DefaultModelConfig returns a sensible default configuration
func DefaultModelConfig() ModelConfig {
	return ModelConfig{
		Provider:      "openai", // Default to OpenAI for backward compatibility
		Model:         "gpt-4o-mini",
		Temperature:   0.7,
		MaxTokens:     4096,
		ContextWindow: 128000,
	}
}

// ToolsConfig configures which tools are enabled
//
// Maps to: codex-rs/core/src/codex.rs SessionConfiguration (tools config part)
type ToolsConfig struct {
	EnableShell    bool `json:"enable_shell"`
	EnableReadFile bool `json:"enable_read_file"`
	EnableWriteFile  bool `json:"enable_write_file,omitempty"`  // Built-in write_file tool
	EnableListDir    bool `json:"enable_list_dir,omitempty"`    // Built-in list_dir tool
	EnableGrepFiles  bool `json:"enable_grep_files,omitempty"`  // Built-in grep_files tool
	EnableApplyPatch bool `json:"enable_apply_patch,omitempty"` // Built-in apply_patch tool
}

// DefaultToolsConfig returns default tools configuration
func DefaultToolsConfig() ToolsConfig {
	return ToolsConfig{
		EnableShell:      true,
		EnableReadFile:   true,
		EnableWriteFile:  true,
		EnableListDir:    true,
		EnableGrepFiles:  true,
		EnableApplyPatch: true,
	}
}

// ApprovalMode controls when the user is prompted before tool execution.
//
// Maps to: codex-rs/protocol/src/protocol.rs AskForApproval
type ApprovalMode string

const (
	// ApprovalUnlessTrusted prompts for all mutating tools. Safe commands auto-approved.
	ApprovalUnlessTrusted ApprovalMode = "unless-trusted"
	// ApprovalNever auto-approves everything, never prompts.
	ApprovalNever ApprovalMode = "never"
	// ApprovalOnFailure auto-approves in sandbox, escalates on failure.
	// Maps to: codex-rs on-failure approval mode
	ApprovalOnFailure ApprovalMode = "on-failure"
)

// SessionConfiguration configures a complete agentic session.
//
// Maps to: codex-rs/core/src/codex.rs SessionConfiguration
type SessionConfiguration struct {
	// Instructions hierarchy (maps to Codex 3-tier system)
	BaseInstructions      string `json:"base_instructions,omitempty"`      // Core system prompt for the model
	DeveloperInstructions string `json:"developer_instructions,omitempty"` // Developer overrides (sent as developer message)
	UserInstructions      string `json:"user_instructions,omitempty"`      // Project docs (AGENTS.md content)

	// Model configuration
	Model ModelConfig `json:"model"`

	// Tool configuration
	Tools ToolsConfig `json:"tools"`

	// Approval mode (empty/unset treated as "never" for backward compat)
	ApprovalMode ApprovalMode `json:"approval_mode,omitempty"`

	// Execution context
	Cwd string `json:"cwd,omitempty"` // Working directory for tool execution

	// Codex home directory for loading exec policy rules.
	// Default: ~/.codex
	CodexHome string `json:"codex_home,omitempty"`

	// Sandbox configuration
	SandboxMode          string   `json:"sandbox_mode,omitempty"`           // "full-access", "read-only", "workspace-write"
	SandboxWritableRoots []string `json:"sandbox_writable_roots,omitempty"` // Directories writable in workspace-write mode
	SandboxNetworkAccess bool     `json:"sandbox_network_access,omitempty"` // Whether network is allowed in sandbox

	// Environment variable filtering for shell commands.
	// Maps to: codex-rs ShellEnvironmentPolicy
	EnvInherit               string            `json:"env_inherit,omitempty"`                 // "all" (default), "none", "core"
	EnvIgnoreDefaultExcludes *bool             `json:"env_ignore_default_excludes,omitempty"` // nil = true (default: keep sensitive vars)
	EnvExclude               []string          `json:"env_exclude,omitempty"`                 // Wildcard patterns to exclude
	EnvSet                   map[string]string `json:"env_set,omitempty"`                     // Explicit overrides
	EnvIncludeOnly           []string          `json:"env_include_only,omitempty"`             // Whitelist (if non-empty)

	// Session metadata
	SessionSource string `json:"session_source,omitempty"` // "cli", "api", "exec" — for logging/tracking

	// CLI-side project docs (AGENTS.md from CLI's local project).
	// Worker-side discovery may replace these.
	CLIProjectDocs string `json:"cli_project_docs,omitempty"`

	// User personal instructions (from ~/.codex/instructions.md).
	// Always included in final instructions.
	UserPersonalInstructions string `json:"user_personal_instructions,omitempty"`

	// Task queue for session-specific activities (tools, instruction loading).
	// If empty, uses the workflow's default queue (backward compat).
	SessionTaskQueue string `json:"session_task_queue,omitempty"`
}

// DefaultSessionConfiguration returns sensible defaults.
func DefaultSessionConfiguration() SessionConfiguration {
	return SessionConfiguration{
		Model: DefaultModelConfig(),
		Tools: DefaultToolsConfig(),
	}
}
