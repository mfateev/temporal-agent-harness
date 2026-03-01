package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseConfigToml_FullConfig(t *testing.T) {
	input := `
model = "claude-sonnet-4-0"
model_provider = "anthropic"
model_context_window = 200000
model_auto_compact_token_limit = 160000
model_reasoning_effort = "high"
approval_policy = "unless-trusted"
sandbox_mode = "workspace-write"
disable_suggestions = false

[sandbox_workspace_write]
writable_roots = ["/home/dev/projects"]
network_access = true

[memory]
enabled = true
db_path = "/home/dev/.codex/state.sqlite"

[mcp_servers.docs]
command = "docs-server"
args = ["--verbose"]
startup_timeout_sec = 5
enabled_tools = ["search", "read"]

[mcp_servers.docs.env]
DOCS_API_KEY = "secret"
`
	cfg, err := ParseConfigToml([]byte(input))
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "claude-sonnet-4-0", *cfg.Model)
	assert.Equal(t, "anthropic", *cfg.ModelProvider)
	assert.Equal(t, 200000, *cfg.ModelContextWindow)
	assert.Equal(t, 160000, *cfg.ModelAutoCompactTokenLimit)
	assert.Equal(t, "high", *cfg.ModelReasoningEffort)
	assert.Equal(t, "unless-trusted", *cfg.ApprovalPolicy)
	assert.Equal(t, "workspace-write", *cfg.SandboxMode)
	assert.Equal(t, false, *cfg.DisableSuggestions)

	require.NotNil(t, cfg.SandboxWorkspaceWrite)
	assert.Equal(t, []string{"/home/dev/projects"}, cfg.SandboxWorkspaceWrite.WritableRoots)
	assert.Equal(t, true, *cfg.SandboxWorkspaceWrite.NetworkAccess)

	require.NotNil(t, cfg.Memory)
	assert.Equal(t, true, *cfg.Memory.Enabled)
	assert.Equal(t, "/home/dev/.codex/state.sqlite", *cfg.Memory.DbPath)

	require.Contains(t, cfg.McpServers, "docs")
	docs := cfg.McpServers["docs"]
	assert.Equal(t, "docs-server", docs.Command)
	assert.Equal(t, []string{"--verbose"}, docs.Args)
	assert.Equal(t, map[string]string{"DOCS_API_KEY": "secret"}, docs.Env)
	assert.Equal(t, 5, *docs.StartupTimeoutSec)
	assert.Equal(t, []string{"search", "read"}, docs.EnabledTools)
}

func TestParseConfigToml_Empty(t *testing.T) {
	cfg, err := ParseConfigToml([]byte(""))
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Nil(t, cfg.Model)
	assert.Nil(t, cfg.ModelProvider)
	assert.Nil(t, cfg.ApprovalPolicy)
	assert.Nil(t, cfg.Memory)
	assert.Nil(t, cfg.McpServers)
}

func TestParseConfigToml_InvalidTOML(t *testing.T) {
	_, err := ParseConfigToml([]byte("[invalid"))
	assert.Error(t, err)
}

func TestParseConfigToml_PartialConfig(t *testing.T) {
	input := `
model = "gpt-4o"
disable_suggestions = true
`
	cfg, err := ParseConfigToml([]byte(input))
	require.NoError(t, err)

	assert.Equal(t, "gpt-4o", *cfg.Model)
	assert.Equal(t, true, *cfg.DisableSuggestions)
	assert.Nil(t, cfg.ModelProvider)
	assert.Nil(t, cfg.ApprovalPolicy)
	assert.Nil(t, cfg.SandboxMode)
	assert.Nil(t, cfg.Memory)
}

func TestApplyToConfig_AllFields(t *testing.T) {
	tomlInput := `
model = "claude-sonnet-4-0"
model_provider = "anthropic"
model_context_window = 200000
model_auto_compact_token_limit = 160000
model_reasoning_effort = "high"
approval_policy = "unless-trusted"
sandbox_mode = "workspace-write"
disable_suggestions = true

[sandbox_workspace_write]
writable_roots = ["/home/dev/projects"]
network_access = true

[memory]
enabled = true
db_path = "/tmp/test.sqlite"

[mcp_servers.test]
command = "test-server"
args = ["--flag"]
required = true
`
	parsed, err := ParseConfigToml([]byte(tomlInput))
	require.NoError(t, err)

	cfg := DefaultSessionConfiguration()
	parsed.ApplyToConfig(&cfg)

	assert.Equal(t, "claude-sonnet-4-0", cfg.Model.Model)
	assert.Equal(t, "anthropic", cfg.Model.Provider)
	assert.Equal(t, 200000, cfg.Model.ContextWindow)
	assert.Equal(t, 160000, cfg.AutoCompactTokenLimit)
	assert.Equal(t, ReasoningEffortHigh, cfg.Model.ReasoningEffort)
	assert.Equal(t, ApprovalUnlessTrusted, cfg.Permissions.ApprovalMode)
	assert.Equal(t, "workspace-write", cfg.Permissions.SandboxMode)
	assert.Equal(t, []string{"/home/dev/projects"}, cfg.Permissions.SandboxWritableRoots)
	assert.Equal(t, true, cfg.Permissions.SandboxNetworkAccess)
	assert.Equal(t, true, cfg.DisableSuggestions)
	assert.Equal(t, true, cfg.MemoryEnabled)
	assert.Equal(t, "/tmp/test.sqlite", cfg.MemoryDbPath)

	require.Contains(t, cfg.McpServers, "test")
	assert.Equal(t, "test-server", cfg.McpServers["test"].Transport.Command)
	assert.Equal(t, []string{"--flag"}, cfg.McpServers["test"].Transport.Args)
	assert.Equal(t, true, cfg.McpServers["test"].Required)
}

func TestApplyToConfig_EmptyConfig(t *testing.T) {
	parsed, err := ParseConfigToml([]byte(""))
	require.NoError(t, err)

	cfg := DefaultSessionConfiguration()
	original := cfg
	parsed.ApplyToConfig(&cfg)

	// Nothing should change
	assert.Equal(t, original.Model.Model, cfg.Model.Model)
	assert.Equal(t, original.Model.Provider, cfg.Model.Provider)
	assert.Equal(t, original.Model.ContextWindow, cfg.Model.ContextWindow)
}

func TestApplyToConfig_PartialOverride(t *testing.T) {
	tomlInput := `model = "gpt-4o"`

	parsed, err := ParseConfigToml([]byte(tomlInput))
	require.NoError(t, err)

	cfg := DefaultSessionConfiguration()
	parsed.ApplyToConfig(&cfg)

	// Model should be overridden
	assert.Equal(t, "gpt-4o", cfg.Model.Model)
	// Provider should remain default
	assert.Equal(t, "openai", cfg.Model.Provider)
}

func TestApplyToConfig_McpServerConversion(t *testing.T) {
	tomlInput := `
[mcp_servers.myserver]
command = "my-cmd"
args = ["arg1", "arg2"]
cwd = "/tmp"
url = ""
enabled = false
startup_timeout_sec = 15
tool_timeout_sec = 120
enabled_tools = ["tool1"]
disabled_tools = ["tool2"]

[mcp_servers.myserver.env]
KEY = "val"
`
	parsed, err := ParseConfigToml([]byte(tomlInput))
	require.NoError(t, err)

	cfg := DefaultSessionConfiguration()
	parsed.ApplyToConfig(&cfg)

	require.Contains(t, cfg.McpServers, "myserver")
	srv := cfg.McpServers["myserver"]

	assert.Equal(t, "my-cmd", srv.Transport.Command)
	assert.Equal(t, []string{"arg1", "arg2"}, srv.Transport.Args)
	assert.Equal(t, map[string]string{"KEY": "val"}, srv.Transport.Env)
	assert.Equal(t, "/tmp", srv.Transport.Cwd)
	assert.Equal(t, false, *srv.Enabled)
	assert.Equal(t, 15, *srv.StartupTimeoutSec)
	assert.Equal(t, 120, *srv.ToolTimeoutSec)
	assert.Equal(t, []string{"tool1"}, srv.EnabledTools)
	assert.Equal(t, []string{"tool2"}, srv.DisabledTools)
}
