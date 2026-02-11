package handlers

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mfateev/codex-temporal-go/internal/tools"
)

func newReadInvocation(args map[string]interface{}) *tools.ToolInvocation {
	return &tools.ToolInvocation{
		CallID:    "test-call",
		ToolName:  "read_file",
		Arguments: args,
	}
}

func TestReadFile_OutputIncludesFilePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	require.NoError(t, os.WriteFile(path, []byte("line1\nline2\n"), 0644))

	tool := NewReadFileTool()
	out, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"path": path,
	}))
	require.NoError(t, err)
	require.NotNil(t, out.Success)
	assert.True(t, *out.Success)

	// Output must start with "File: <path>\n"
	assert.Contains(t, out.Content, "File: "+path+"\n")
	// The line-numbered content follows the header
	assert.Contains(t, out.Content, "     1\tline1")
	assert.Contains(t, out.Content, "     2\tline2")
}

func TestReadFile_EmptyFileIncludesFilePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	require.NoError(t, os.WriteFile(path, []byte{}, 0644))

	tool := NewReadFileTool()
	out, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"path": path,
	}))
	require.NoError(t, err)
	require.NotNil(t, out.Success)
	assert.True(t, *out.Success)
	assert.Contains(t, out.Content, "File: "+path+"\n")
	assert.Contains(t, out.Content, "(empty file)")
}

func TestReadFile_OffsetBeyondFileIncludesFilePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "short.txt")
	require.NoError(t, os.WriteFile(path, []byte("only one line\n"), 0644))

	tool := NewReadFileTool()
	out, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"path":   path,
		"offset": 100,
	}))
	require.NoError(t, err)
	require.NotNil(t, out.Success)
	assert.True(t, *out.Success)
	assert.Contains(t, out.Content, "File: "+path+"\n")
	assert.Contains(t, out.Content, "(file has fewer than 100 lines)")
}

func TestReadFile_MissingPath(t *testing.T) {
	tool := NewReadFileTool()
	_, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{}))
	require.Error(t, err)
	assert.True(t, tools.IsValidationError(err))
}

func TestReadFile_EmptyPath(t *testing.T) {
	tool := NewReadFileTool()
	_, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"path": "",
	}))
	require.Error(t, err)
	assert.True(t, tools.IsValidationError(err))
}

func TestReadFile_NonexistentFile(t *testing.T) {
	tool := NewReadFileTool()
	out, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"path": "/nonexistent/path/file.txt",
	}))
	require.NoError(t, err) // Returns output, not error
	require.NotNil(t, out.Success)
	assert.False(t, *out.Success)
	assert.Contains(t, out.Content, "Failed to open file")
}

func TestReadFile_WithLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multi.txt")
	require.NoError(t, os.WriteFile(path, []byte("a\nb\nc\nd\ne\n"), 0644))

	tool := NewReadFileTool()
	out, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"path":  path,
		"limit": 2,
	}))
	require.NoError(t, err)
	assert.True(t, *out.Success)
	assert.Contains(t, out.Content, "File: "+path+"\n")
	assert.Contains(t, out.Content, "     1\ta")
	assert.Contains(t, out.Content, "     2\tb")
	assert.NotContains(t, out.Content, "     3\tc")
}
