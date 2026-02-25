package cli

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentsMdTemplate(t *testing.T) {
	tmpl := agentsMdTemplate()
	assert.Contains(t, tmpl, "# Project Instructions")
	assert.Contains(t, tmpl, "## Description")
	assert.Contains(t, tmpl, "## Conventions")
	assert.Contains(t, tmpl, "## Tool Preferences")
	assert.Contains(t, tmpl, "## Important Files")
}

func TestRunInitCmd_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	cmd := runInitCmd(dir)
	msg := cmd()

	result, ok := msg.(InitResultMsg)
	require.True(t, ok, "expected InitResultMsg, got %T", msg)
	assert.True(t, result.Created)
	assert.False(t, result.AlreadyExists)
	assert.Equal(t, filepath.Join(dir, "AGENTS.md"), result.Path)

	// Verify file was written
	content, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "# Project Instructions")
}

func TestRunInitCmd_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	// Create the file first
	err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("existing"), 0644)
	require.NoError(t, err)

	cmd := runInitCmd(dir)
	msg := cmd()

	result, ok := msg.(InitResultMsg)
	require.True(t, ok, "expected InitResultMsg, got %T", msg)
	assert.True(t, result.AlreadyExists)
	assert.False(t, result.Created)

	// Verify file was NOT overwritten
	content, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	require.NoError(t, err)
	assert.Equal(t, "existing", string(content))
}

func TestModel_InitCommand(t *testing.T) {
	m := newTestModel()
	m.textarea.SetValue("/init")
	_, cmd := m.handleInputKey(tea.KeyMsg{Type: tea.KeyEnter})
	assert.NotNil(t, cmd, "should return a command")
}
