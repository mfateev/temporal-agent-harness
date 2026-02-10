package execpolicy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendAllowPrefixRule_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	rulesFile := filepath.Join(dir, "rules", "default.rules")

	err := AppendAllowPrefixRule(rulesFile, []string{"echo"})
	require.NoError(t, err)

	content, err := os.ReadFile(rulesFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), `prefix_rule(pattern=["echo"], decision="allow")`)
}

func TestAppendAllowPrefixRule_AppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	rulesFile := filepath.Join(dir, "default.rules")

	// Create initial file
	err := os.WriteFile(rulesFile, []byte(`prefix_rule(pattern=["ls"], decision="allow")`+"\n"), 0o644)
	require.NoError(t, err)

	// Append new rule
	err = AppendAllowPrefixRule(rulesFile, []string{"echo"})
	require.NoError(t, err)

	content, err := os.ReadFile(rulesFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), `prefix_rule(pattern=["ls"], decision="allow")`)
	assert.Contains(t, string(content), `prefix_rule(pattern=["echo"], decision="allow")`)
}

func TestAppendAllowPrefixRule_DeduplicatesExisting(t *testing.T) {
	dir := t.TempDir()
	rulesFile := filepath.Join(dir, "default.rules")

	err := AppendAllowPrefixRule(rulesFile, []string{"echo"})
	require.NoError(t, err)

	// Append same rule again
	err = AppendAllowPrefixRule(rulesFile, []string{"echo"})
	require.NoError(t, err)

	content, err := os.ReadFile(rulesFile)
	require.NoError(t, err)

	// Should only appear once
	line := `prefix_rule(pattern=["echo"], decision="allow")`
	count := 0
	for i := 0; i+len(line) <= len(content); i++ {
		if string(content[i:i+len(line)]) == line {
			count++
		}
	}
	assert.Equal(t, 1, count, "rule should appear exactly once")
}

func TestAppendAllowPrefixRule_MultiTokenPrefix(t *testing.T) {
	dir := t.TempDir()
	rulesFile := filepath.Join(dir, "default.rules")

	err := AppendAllowPrefixRule(rulesFile, []string{"git", "status"})
	require.NoError(t, err)

	content, err := os.ReadFile(rulesFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), `prefix_rule(pattern=["git", "status"], decision="allow")`)
}

func TestAppendAllowPrefixRule_EmptyPrefix(t *testing.T) {
	dir := t.TempDir()
	rulesFile := filepath.Join(dir, "default.rules")

	err := AppendAllowPrefixRule(rulesFile, []string{})
	require.Error(t, err)
}

func TestBuildPrefixRuleLine(t *testing.T) {
	line := buildPrefixRuleLine([]string{"git", "push"})
	assert.Equal(t, `prefix_rule(pattern=["git", "push"], decision="allow")`, line)

	line = buildPrefixRuleLine([]string{"echo"})
	assert.Equal(t, `prefix_rule(pattern=["echo"], decision="allow")`, line)
}
