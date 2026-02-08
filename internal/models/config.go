package models

// ModelConfig configures the LLM model parameters
//
// Maps to: codex-rs/core/src/codex.rs SessionConfiguration (model config part)
type ModelConfig struct {
	Model         string  `json:"model"`          // e.g., "gpt-3.5-turbo", "gpt-4"
	Temperature   float64 `json:"temperature"`    // 0.0 to 2.0
	MaxTokens     int     `json:"max_tokens"`     // Max tokens to generate
	ContextWindow int     `json:"context_window"` // Max context window size
}

// DefaultModelConfig returns a sensible default configuration
func DefaultModelConfig() ModelConfig {
	return ModelConfig{
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

	// Execution context
	Cwd string `json:"cwd,omitempty"` // Working directory for tool execution

	// Session metadata
	SessionSource string `json:"session_source,omitempty"` // "cli", "api", "exec" — for logging/tracking
}

// DefaultSessionConfiguration returns sensible defaults.
func DefaultSessionConfiguration() SessionConfiguration {
	return SessionConfiguration{
		Model: DefaultModelConfig(),
		Tools: DefaultToolsConfig(),
	}
}
