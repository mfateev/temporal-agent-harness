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
	EnableWriteFile  bool `json:"enable_write_file,omitempty"`  // Future
	EnableApplyPatch bool `json:"enable_apply_patch,omitempty"` // Built-in apply_patch tool
}

// DefaultToolsConfig returns default tools configuration
func DefaultToolsConfig() ToolsConfig {
	return ToolsConfig{
		EnableShell:      true,
		EnableReadFile:   true,
		EnableApplyPatch: true,
	}
}

// SessionConfig combines model and tools configuration
//
// Maps to: codex-rs/core/src/codex.rs SessionConfiguration
type SessionConfig struct {
	Model ModelConfig `json:"model"`
	Tools ToolsConfig `json:"tools"`
}

// DefaultSessionConfig returns default session configuration
func DefaultSessionConfig() SessionConfig {
	return SessionConfig{
		Model: DefaultModelConfig(),
		Tools: DefaultToolsConfig(),
	}
}
