package mcp

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestTool(server, tool string) ToolInfo {
	return ToolInfo{ServerName: server, ToolName: tool}
}

// TestQualifyTools_ShortNonDuplicatedNames verifies basic tool qualification.
// Port of: codex-rs test_qualify_tools_short_non_duplicated_names
func TestQualifyTools_ShortNonDuplicatedNames(t *testing.T) {
	tools := []ToolInfo{
		createTestTool("server1", "tool1"),
		createTestTool("server1", "tool2"),
	}

	qualified := QualifyTools(tools)

	assert.Len(t, qualified, 2)
	assert.Contains(t, qualified, "mcp__server1__tool1")
	assert.Contains(t, qualified, "mcp__server1__tool2")
}

// TestQualifyTools_DuplicatedNamesSkipped verifies duplicate tool handling.
// Port of: codex-rs test_qualify_tools_duplicated_names_skipped
func TestQualifyTools_DuplicatedNamesSkipped(t *testing.T) {
	tools := []ToolInfo{
		createTestTool("server1", "duplicate_tool"),
		createTestTool("server1", "duplicate_tool"),
	}

	qualified := QualifyTools(tools)

	// Only the first tool should remain, the second is skipped
	assert.Len(t, qualified, 1)
	assert.Contains(t, qualified, "mcp__server1__duplicate_tool")
}

// TestQualifyTools_LongNamesSameServer verifies name length enforcement with SHA1 hash suffix.
// Port of: codex-rs test_qualify_tools_long_names_same_server
func TestQualifyTools_LongNamesSameServer(t *testing.T) {
	serverName := "my_server"

	tools := []ToolInfo{
		createTestTool(serverName, "extremely_lengthy_function_name_that_absolutely_surpasses_all_reasonable_limits"),
		createTestTool(serverName, "yet_another_extremely_lengthy_function_name_that_absolutely_surpasses_all_reasonable_limits"),
	}

	qualified := QualifyTools(tools)

	require.Len(t, qualified, 2)

	keys := make([]string, 0, len(qualified))
	for k := range qualified {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	assert.Len(t, keys[0], 64)
	assert.Equal(t, "mcp__my_server__extremel119a2b97664e41363932dc84de21e2ff1b93b3e9", keys[0])

	assert.Len(t, keys[1], 64)
	assert.Equal(t, "mcp__my_server__yet_anot419a82a89325c1b477274a41f8c65ea5f3a7f341", keys[1])
}

// TestQualifyTools_SanitizesInvalidCharacters verifies character sanitization while preserving originals.
// Port of: codex-rs test_qualify_tools_sanitizes_invalid_characters
func TestQualifyTools_SanitizesInvalidCharacters(t *testing.T) {
	tools := []ToolInfo{createTestTool("server.one", "tool.two")}

	qualified := QualifyTools(tools)

	require.Len(t, qualified, 1)

	var qualifiedName string
	var tool ToolInfo
	for k, v := range qualified {
		qualifiedName = k
		tool = v
	}

	assert.Equal(t, "mcp__server_one__tool_two", qualifiedName)

	// The key is sanitized for OpenAI, but we keep original parts for the actual MCP call.
	assert.Equal(t, "server.one", tool.ServerName)
	assert.Equal(t, "tool.two", tool.ToolName)

	// Verify all characters are API-compatible
	for _, c := range qualifiedName {
		assert.True(t,
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-',
			"qualified name must be Responses API compatible: %q", qualifiedName)
	}
}

// TestToolFilter_AllowsByDefault verifies default allow behavior.
// Port of: codex-rs tool_filter_allows_by_default
func TestToolFilter_AllowsByDefault(t *testing.T) {
	filter := ToolFilter{}
	assert.True(t, filter.Allows("any"))
}

// TestToolFilter_AppliesEnabledList verifies allow-list filtering.
// Port of: codex-rs tool_filter_applies_enabled_list
func TestToolFilter_AppliesEnabledList(t *testing.T) {
	filter := ToolFilter{
		Enabled:  map[string]bool{"allowed": true},
		Disabled: map[string]bool{},
	}

	assert.True(t, filter.Allows("allowed"))
	assert.False(t, filter.Allows("denied"))
}

// TestToolFilter_AppliesDisabledList verifies deny-list filtering.
// Port of: codex-rs tool_filter_applies_disabled_list
func TestToolFilter_AppliesDisabledList(t *testing.T) {
	filter := ToolFilter{
		Enabled:  nil,
		Disabled: map[string]bool{"blocked": true},
	}

	assert.False(t, filter.Allows("blocked"))
	assert.True(t, filter.Allows("open"))
}

// TestToolFilter_AppliesEnabledThenDisabled verifies combined filtering.
// Port of: codex-rs tool_filter_applies_enabled_then_disabled
func TestToolFilter_AppliesEnabledThenDisabled(t *testing.T) {
	filter := ToolFilter{
		Enabled:  map[string]bool{"keep": true, "remove": true},
		Disabled: map[string]bool{"remove": true},
	}

	assert.True(t, filter.Allows("keep"))
	assert.False(t, filter.Allows("remove"))
	assert.False(t, filter.Allows("unknown"))
}

// TestFilterTools_AppliesPerServerFilters verifies per-server tool filtering.
// Port of: codex-rs filter_tools_applies_per_server_filters
func TestFilterTools_AppliesPerServerFilters(t *testing.T) {
	server1Tools := []ToolInfo{
		createTestTool("server1", "tool_a"),
		createTestTool("server1", "tool_b"),
	}
	server2Tools := []ToolInfo{
		createTestTool("server2", "tool_a"),
	}

	server1Filter := ToolFilter{
		Enabled:  map[string]bool{"tool_a": true, "tool_b": true},
		Disabled: map[string]bool{"tool_b": true},
	}
	server2Filter := ToolFilter{
		Enabled:  nil,
		Disabled: map[string]bool{"tool_a": true},
	}

	filtered1 := FilterTools(server1Tools, server1Filter)
	filtered2 := FilterTools(server2Tools, server2Filter)
	filtered := append(filtered1, filtered2...)

	require.Len(t, filtered, 1)
	assert.Equal(t, "server1", filtered[0].ServerName)
	assert.Equal(t, "tool_a", filtered[0].ToolName)
}

// TestNewToolFilter_FromConfig verifies ToolFilter creation from config lists.
func TestNewToolFilter_FromConfig(t *testing.T) {
	filter := NewToolFilter([]string{"tool_a", "tool_b"}, []string{"tool_b"})
	assert.True(t, filter.Allows("tool_a"))
	assert.False(t, filter.Allows("tool_b"))
	assert.False(t, filter.Allows("tool_c"))
}

// TestNewToolFilter_EmptyConfig verifies default behavior with empty lists.
func TestNewToolFilter_EmptyConfig(t *testing.T) {
	filter := NewToolFilter(nil, nil)
	assert.True(t, filter.Allows("anything"))
}

// TestSanitizeName verifies individual name sanitization.
func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"hello.world", "hello_world"},
		{"a-b_c", "a-b_c"},
		{"foo bar", "foo_bar"},
		{"MixedCase123", "MixedCase123"},
		{"", "_"},
		{"...", "___"},
		{"@#$%", "____"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, SanitizeName(tt.input))
		})
	}
}

// TestQualifyToolName verifies individual tool name qualification.
func TestQualifyToolName(t *testing.T) {
	name := QualifyToolName("github", "create_issue")
	assert.Equal(t, "mcp__github__create_issue", name)
}

// TestQualifyToolName_LongName verifies truncation with SHA1.
func TestQualifyToolName_LongName(t *testing.T) {
	name := QualifyToolName("my_server", "extremely_lengthy_function_name_that_absolutely_surpasses_all_reasonable_limits")
	assert.Len(t, name, 64)
}
