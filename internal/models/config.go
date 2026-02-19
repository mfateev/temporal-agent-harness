package models

import "github.com/mfateev/temporal-agent-harness/internal/tools"

// ModelConfig configures the LLM model parameters
//
// Maps to: codex-rs/core/src/codex.rs SessionConfiguration (model config part)
type ModelConfig struct {
	Provider        string  `json:"provider"`                  // "openai" or "anthropic"
	Model           string  `json:"model"`                     // e.g., "gpt-4o", "claude-sonnet-4.5-20250929"
	Temperature     float64 `json:"temperature"`               // 0.0 to 2.0
	MaxTokens       int     `json:"max_tokens"`                // Max tokens to generate
	ContextWindow   int     `json:"context_window"`            // Max context window size
	ReasoningEffort string  `json:"reasoning_effort,omitempty"` // "low", "medium", "high" — for model reasoning control
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

// ToolsConfig configures which tools are available in a session.
// EnabledTools lists internal tool names. Group names (e.g. "collab")
// are expanded automatically by the registry.
//
// Maps to: codex-rs/core/src/codex.rs SessionConfiguration (tools config part)
type ToolsConfig struct {
	EnabledTools []string `json:"enabled_tools"`
}

// HasTool returns true if the named tool (or any member of a group with that
// name) is present in EnabledTools.
func (c ToolsConfig) HasTool(name string) bool {
	expanded := tools.ExpandGroups(c.EnabledTools)
	for _, t := range expanded {
		if t == name {
			return true
		}
	}
	return false
}

// RemoveTools removes tools by internal name from EnabledTools.
// Group names are expanded before removal.
func (c *ToolsConfig) RemoveTools(names ...string) {
	toRemove := make(map[string]bool, len(names))
	for _, n := range tools.ExpandGroups(names) {
		toRemove[n] = true
	}
	// Also remove the group name itself so HasTool("collab") returns false
	for _, n := range names {
		toRemove[n] = true
	}
	filtered := c.EnabledTools[:0]
	for _, t := range c.EnabledTools {
		if !toRemove[t] {
			filtered = append(filtered, t)
		}
	}
	c.EnabledTools = filtered
}

// AddTools appends tools to EnabledTools (no dedup).
func (c *ToolsConfig) AddTools(names ...string) {
	c.EnabledTools = append(c.EnabledTools, names...)
}

// DefaultToolsConfig returns default tools configuration.
func DefaultToolsConfig() ToolsConfig {
	return ToolsConfig{
		EnabledTools: tools.DefaultEnabledTools(),
	}
}

// WebSearchMode controls whether web search is enabled and its freshness.
//
// Maps to: codex-rs/protocol/src/config_types.rs WebSearchMode
type WebSearchMode string

const (
	WebSearchDisabled WebSearchMode = "disabled"
	WebSearchCached   WebSearchMode = "cached"
	WebSearchLive     WebSearchMode = "live"
)

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

	// ExecPolicyRules contains the pre-loaded exec policy rules source
	// (from ~/.codex/rules/*.rules). Set by HarnessWorkflow so that
	// AgenticWorkflow can apply exec policy without re-running the
	// LoadExecPolicy activity.
	// Empty string means no rules loaded.
	ExecPolicyRules string `json:"exec_policy_rules,omitempty"`

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

	// Context compaction threshold (in estimated tokens). When the conversation
	// history exceeds this limit, proactive compaction is triggered. 0 = disabled.
	// Maps to: codex-rs auto_compact_token_limit
	AutoCompactTokenLimit int `json:"auto_compact_token_limit,omitempty"`

	// Web search configuration
	// Maps to: codex-rs web_search_mode
	WebSearchMode WebSearchMode `json:"web_search_mode,omitempty"`

	// Disable post-turn prompt suggestions
	DisableSuggestions bool `json:"disable_suggestions,omitempty"`

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
