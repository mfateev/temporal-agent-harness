package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterAndGet(t *testing.T) {
	// Built-in tools are registered via init(). Verify a few known entries.
	entry, ok := GetEntry("shell_command")
	require.True(t, ok, "shell_command should be registered")
	assert.Equal(t, "shell_command", entry.Name)
	assert.NotNil(t, entry.Constructor)

	entry, ok = GetEntry("read_file")
	require.True(t, ok)
	assert.Equal(t, "read_file", entry.Name)

	_, ok = GetEntry("nonexistent_tool")
	assert.False(t, ok, "unknown tool should not be found")
}

func TestBuildSpecs(t *testing.T) {
	specs := BuildSpecs([]string{"shell_command", "read_file"})
	require.Len(t, specs, 2)
	assert.Equal(t, "shell_command", specs[0].Name)
	assert.Equal(t, "read_file", specs[1].Name)
}

func TestBuildSpecs_WithGroup(t *testing.T) {
	specs := BuildSpecs([]string{"collab"})
	// "collab" expands to 5 tools
	require.Len(t, specs, 5)
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.Name
	}
	assert.Contains(t, names, "spawn_agent")
	assert.Contains(t, names, "send_input")
	assert.Contains(t, names, "wait")
	assert.Contains(t, names, "close_agent")
	assert.Contains(t, names, "resume_agent")
}

func TestExpandGroups(t *testing.T) {
	expanded := ExpandGroups([]string{"shell_command", "collab", "read_file"})
	// "collab" should be replaced with its members
	assert.NotContains(t, expanded, "collab")
	assert.Contains(t, expanded, "spawn_agent")
	assert.Contains(t, expanded, "send_input")
	assert.Contains(t, expanded, "shell_command")
	assert.Contains(t, expanded, "read_file")
}

func TestExpandGroups_NoGroups(t *testing.T) {
	expanded := ExpandGroups([]string{"shell_command", "read_file"})
	assert.Equal(t, []string{"shell_command", "read_file"}, expanded)
}

func TestDefaultEnabledTools(t *testing.T) {
	defaults := DefaultEnabledTools()
	assert.Contains(t, defaults, "shell_command")
	assert.Contains(t, defaults, "read_file")
	assert.Contains(t, defaults, "write_file")
	assert.Contains(t, defaults, "apply_patch")
	assert.Contains(t, defaults, "request_user_input")
	assert.Contains(t, defaults, "update_plan")

	// Every default should produce a valid spec
	specs := BuildSpecs(defaults)
	assert.Len(t, specs, len(defaults), "all defaults should resolve to specs")
}

func TestUnknownTool(t *testing.T) {
	// Unknown names should be silently skipped
	specs := BuildSpecs([]string{"shell_command", "does_not_exist", "read_file"})
	require.Len(t, specs, 2, "unknown tool should be skipped")
	assert.Equal(t, "shell_command", specs[0].Name)
	assert.Equal(t, "read_file", specs[1].Name)
}

func TestSpecEntry_ResolvedLLMName(t *testing.T) {
	t.Run("defaults to Name", func(t *testing.T) {
		e := SpecEntry{Name: "shell_command"}
		assert.Equal(t, "shell_command", e.resolvedLLMName())
	})

	t.Run("uses LLMName if set", func(t *testing.T) {
		e := SpecEntry{Name: "patch_gpt", LLMName: "apply_patch"}
		assert.Equal(t, "apply_patch", e.resolvedLLMName())
	})
}

func TestBuiltInToolsRegistered(t *testing.T) {
	// Verify all expected tools are registered after init()
	expected := []string{
		"shell", "shell_command",
		"read_file", "write_file", "list_dir", "grep_files",
		"apply_patch", "request_user_input", "update_plan",
		"spawn_agent", "send_input", "wait", "close_agent", "resume_agent",
	}
	for _, name := range expected {
		_, ok := GetEntry(name)
		assert.True(t, ok, "%s should be registered", name)
	}
}

func TestCollabGroupRegistered(t *testing.T) {
	expanded := ExpandGroups([]string{"collab"})
	assert.Len(t, expanded, 5)
	assert.Contains(t, expanded, "spawn_agent")
	assert.Contains(t, expanded, "send_input")
	assert.Contains(t, expanded, "wait")
	assert.Contains(t, expanded, "close_agent")
	assert.Contains(t, expanded, "resume_agent")
}
