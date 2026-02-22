// Package mcp provides MCP (Model Context Protocol) client support.
//
// Corresponds to: codex-rs/core/src/mcp_connection_manager.rs (config types)
//                 codex-rs/core/src/config/types.rs (McpServerConfig)
package mcp

import "time"

// Default timeout for initializing MCP server & initially listing tools.
// Maps to: codex-rs DEFAULT_STARTUP_TIMEOUT (10 seconds)
const DefaultStartupTimeout = 10 * time.Second

// Default timeout for individual tool calls.
// Maps to: codex-rs DEFAULT_TOOL_TIMEOUT (60 seconds)
const DefaultToolTimeout = 60 * time.Second

// McpServerConfig configures an MCP server connection.
//
// Maps to: codex-rs/core/src/config/types.rs McpServerConfig
type McpServerConfig struct {
	// Transport configuration (stdio or streamable HTTP).
	Transport McpServerTransportConfig `json:"transport"`

	// Whether this server is enabled. Default: true.
	Enabled *bool `json:"enabled,omitempty"`

	// Whether this server is required. If true, initialization failure is fatal.
	// Default: false.
	Required bool `json:"required,omitempty"`

	// Timeout for server startup and initial tool listing.
	// Default: DefaultStartupTimeout (10s).
	StartupTimeoutSec *int `json:"startup_timeout_sec,omitempty"`

	// Timeout for individual tool calls.
	// Default: DefaultToolTimeout (60s).
	ToolTimeoutSec *int `json:"tool_timeout_sec,omitempty"`

	// Explicit allow-list of tool names. If set, only these tools are exposed.
	EnabledTools []string `json:"enabled_tools,omitempty"`

	// Explicit deny-list of tool names. These tools are never exposed.
	DisabledTools []string `json:"disabled_tools,omitempty"`
}

// IsEnabled returns whether this server config is enabled (default: true).
func (c *McpServerConfig) IsEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// GetStartupTimeout returns the startup timeout, using DefaultStartupTimeout if not set.
func (c *McpServerConfig) GetStartupTimeout() time.Duration {
	if c.StartupTimeoutSec != nil {
		return time.Duration(*c.StartupTimeoutSec) * time.Second
	}
	return DefaultStartupTimeout
}

// GetToolTimeout returns the tool call timeout, using DefaultToolTimeout if not set.
func (c *McpServerConfig) GetToolTimeout() time.Duration {
	if c.ToolTimeoutSec != nil {
		return time.Duration(*c.ToolTimeoutSec) * time.Second
	}
	return DefaultToolTimeout
}

// McpToolSpec is a simplified tool specification extracted from MCP Tool definitions.
// Used to pass tool metadata from the mcp package to the workflow/activity layer
// without requiring a dependency on the MCP SDK.
type McpToolSpec struct {
	QualifiedName string                 `json:"qualified_name"` // mcp__server__tool
	ServerName    string                 `json:"server_name"`
	ToolName      string                 `json:"tool_name"`
	Description   string                 `json:"description"`
	InputSchema   map[string]interface{} `json:"input_schema,omitempty"` // Raw JSON Schema
	ReadOnly      bool                   `json:"read_only,omitempty"`
}

// McpServerTransportConfig specifies how to connect to the MCP server.
//
// Maps to: codex-rs/core/src/config/types.rs McpServerTransportConfig
type McpServerTransportConfig struct {
	// Stdio transport: spawn a subprocess.
	// Mutually exclusive with URL.
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`

	// Streamable HTTP transport: connect to a URL.
	// Mutually exclusive with Command.
	URL string `json:"url,omitempty"`
}

// IsStdio returns true if this config uses stdio transport.
func (t *McpServerTransportConfig) IsStdio() bool {
	return t.Command != ""
}

// IsHTTP returns true if this config uses streamable HTTP transport.
func (t *McpServerTransportConfig) IsHTTP() bool {
	return t.URL != ""
}

// ToolFilter controls which MCP tools are exposed from a server.
// A tool is allowed if: (1) enabled is nil (no allowlist) OR the tool is in enabled,
// AND (2) the tool is not in disabled.
//
// Maps to: codex-rs/core/src/mcp_connection_manager.rs ToolFilter
type ToolFilter struct {
	Enabled  map[string]bool // Allow-list (nil = allow all)
	Disabled map[string]bool // Deny-list
}

// NewToolFilter creates a ToolFilter from the config's enabled/disabled tool lists.
func NewToolFilter(enabledTools, disabledTools []string) ToolFilter {
	var enabled map[string]bool
	if len(enabledTools) > 0 {
		enabled = make(map[string]bool, len(enabledTools))
		for _, t := range enabledTools {
			enabled[t] = true
		}
	}

	disabled := make(map[string]bool, len(disabledTools))
	for _, t := range disabledTools {
		disabled[t] = true
	}

	return ToolFilter{Enabled: enabled, Disabled: disabled}
}

// Allows returns whether the given tool name passes the filter.
func (f *ToolFilter) Allows(toolName string) bool {
	if f.Enabled != nil && !f.Enabled[toolName] {
		return false
	}
	return !f.Disabled[toolName]
}

