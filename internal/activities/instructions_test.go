package activities

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadWorkerInstructions_WithAGENTSmd(t *testing.T) {
	// Create a temp dir with .git and AGENTS.md
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("project instructions"), 0o644))

	a := NewInstructionActivities()
	result, err := a.LoadWorkerInstructions(context.Background(), LoadWorkerInstructionsInput{
		Cwd: dir,
	})
	require.NoError(t, err)
	assert.Contains(t, result.ProjectDocs, "project instructions")
	assert.Equal(t, dir, result.GitRoot)
}

func TestLoadWorkerInstructions_EmptyCwd(t *testing.T) {
	a := NewInstructionActivities()
	result, err := a.LoadWorkerInstructions(context.Background(), LoadWorkerInstructionsInput{
		Cwd: "",
	})
	require.NoError(t, err)
	assert.Empty(t, result.ProjectDocs)
	assert.Empty(t, result.GitRoot)
}

func TestLoadWorkerInstructions_NonGitDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("ignored"), 0o644))

	a := NewInstructionActivities()
	result, err := a.LoadWorkerInstructions(context.Background(), LoadWorkerInstructionsInput{
		Cwd: dir,
	})
	require.NoError(t, err)
	assert.Empty(t, result.ProjectDocs)
	assert.Empty(t, result.GitRoot)
}

func TestLoadWorkerInstructions_Subdirectory(t *testing.T) {
	// .git at root, AGENTS.md at root, cwd is a subdirectory
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("root docs"), 0o644))

	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte("sub docs"), 0o644))

	a := NewInstructionActivities()
	result, err := a.LoadWorkerInstructions(context.Background(), LoadWorkerInstructionsInput{
		Cwd: sub,
	})
	require.NoError(t, err)
	assert.Contains(t, result.ProjectDocs, "root docs")
	assert.Contains(t, result.ProjectDocs, "sub docs")
	assert.Equal(t, dir, result.GitRoot)
}
