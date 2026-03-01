package models

import (
	"github.com/BurntSushi/toml"
	"github.com/mfateev/temporal-agent-harness/internal/mcp"
)

// ConfigToml is a TOML-deserializable struct mirroring Codex's config.toml.
// Pointer fields distinguish "not set" from zero values so that only
// explicitly configured values override defaults.
type ConfigToml struct {
	Model                      *string                        `toml:"model"`
	ModelProvider              *string                        `toml:"model_provider"`
	ModelContextWindow         *int                           `toml:"model_context_window"`
	ModelAutoCompactTokenLimit *int                           `toml:"model_auto_compact_token_limit"`
	ModelReasoningEffort       *string                        `toml:"model_reasoning_effort"`
	ModelReasoningSummary      *string                        `toml:"model_reasoning_summary"`
	ApprovalPolicy             *string                        `toml:"approval_policy"`
	SandboxMode                *string                        `toml:"sandbox_mode"`
	SandboxWorkspaceWrite      *SandboxWorkspaceWriteToml     `toml:"sandbox_workspace_write"`
	DisableSuggestions         *bool                          `toml:"disable_suggestions"`
	McpServers                 map[string]McpServerConfigToml `toml:"mcp_servers"`
	Memory                     *MemoryToml                    `toml:"memory"`
	DisabledSkills             []string                       `toml:"disabled_skills"`
}

// SandboxWorkspaceWriteToml configures workspace-write sandbox settings.
type SandboxWorkspaceWriteToml struct {
	WritableRoots []string `toml:"writable_roots"`
	NetworkAccess *bool    `toml:"network_access"`
}

// MemoryToml configures the cross-session memory subsystem.
type MemoryToml struct {
	Enabled *bool   `toml:"enabled"`
	DbPath  *string `toml:"db_path"`
}

// McpServerConfigToml is the TOML representation of an MCP server config.
type McpServerConfigToml struct {
	Command           string            `toml:"command"`
	Args              []string          `toml:"args"`
	Env               map[string]string `toml:"env"`
	Cwd               string            `toml:"cwd"`
	URL               string            `toml:"url"`
	Enabled           *bool             `toml:"enabled"`
	Required          *bool             `toml:"required"`
	StartupTimeoutSec *int              `toml:"startup_timeout_sec"`
	ToolTimeoutSec    *int              `toml:"tool_timeout_sec"`
	EnabledTools      []string          `toml:"enabled_tools"`
	DisabledTools     []string          `toml:"disabled_tools"`
}

// ParseConfigToml parses raw TOML bytes into a ConfigToml.
func ParseConfigToml(data []byte) (*ConfigToml, error) {
	var cfg ConfigToml
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ApplyToConfig merges non-nil fields from the TOML config into the given
// SessionConfiguration. Only fields explicitly set in the TOML file are applied.
func (c *ConfigToml) ApplyToConfig(cfg *SessionConfiguration) {
	if c.Model != nil {
		cfg.Model.Model = *c.Model
	}
	if c.ModelProvider != nil {
		cfg.Model.Provider = *c.ModelProvider
	}
	if c.ModelContextWindow != nil {
		cfg.Model.ContextWindow = *c.ModelContextWindow
	}
	if c.ModelAutoCompactTokenLimit != nil {
		cfg.AutoCompactTokenLimit = *c.ModelAutoCompactTokenLimit
	}
	if c.ModelReasoningEffort != nil {
		if effort, ok := ParseReasoningEffort(*c.ModelReasoningEffort); ok {
			cfg.Model.ReasoningEffort = effort
		}
	}
	if c.ModelReasoningSummary != nil {
		if summary, ok := ParseReasoningSummary(*c.ModelReasoningSummary); ok {
			cfg.Model.ReasoningSummary = summary
		}
	}
	if c.ApprovalPolicy != nil {
		cfg.Permissions.ApprovalMode = ApprovalMode(*c.ApprovalPolicy)
	}
	if c.SandboxMode != nil {
		cfg.Permissions.SandboxMode = *c.SandboxMode
	}
	if c.SandboxWorkspaceWrite != nil {
		if len(c.SandboxWorkspaceWrite.WritableRoots) > 0 {
			cfg.Permissions.SandboxWritableRoots = c.SandboxWorkspaceWrite.WritableRoots
		}
		if c.SandboxWorkspaceWrite.NetworkAccess != nil {
			cfg.Permissions.SandboxNetworkAccess = *c.SandboxWorkspaceWrite.NetworkAccess
		}
	}
	if c.DisableSuggestions != nil {
		cfg.DisableSuggestions = *c.DisableSuggestions
	}
	if len(c.McpServers) > 0 {
		if cfg.McpServers == nil {
			cfg.McpServers = make(map[string]mcp.McpServerConfig, len(c.McpServers))
		}
		for name, srv := range c.McpServers {
			cfg.McpServers[name] = srv.toMcpServerConfig()
		}
	}
	if len(c.DisabledSkills) > 0 {
		cfg.DisabledSkills = c.DisabledSkills
	}
	if c.Memory != nil {
		if c.Memory.Enabled != nil {
			cfg.MemoryEnabled = *c.Memory.Enabled
		}
		if c.Memory.DbPath != nil {
			cfg.MemoryDbPath = *c.Memory.DbPath
		}
	}
}

// toMcpServerConfig converts a TOML MCP server config to the runtime type.
func (m *McpServerConfigToml) toMcpServerConfig() mcp.McpServerConfig {
	sc := mcp.McpServerConfig{
		Transport: mcp.McpServerTransportConfig{
			Command: m.Command,
			Args:    m.Args,
			Env:     m.Env,
			Cwd:     m.Cwd,
			URL:     m.URL,
		},
		Enabled:           m.Enabled,
		StartupTimeoutSec: m.StartupTimeoutSec,
		ToolTimeoutSec:    m.ToolTimeoutSec,
		EnabledTools:      m.EnabledTools,
		DisabledTools:     m.DisabledTools,
	}
	if m.Required != nil {
		sc.Required = *m.Required
	}
	return sc
}
