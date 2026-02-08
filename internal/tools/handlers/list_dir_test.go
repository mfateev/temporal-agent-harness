package handlers

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mfateev/codex-temporal-go/internal/tools"
)

// Tests ported from: codex-rs/core/src/tools/handlers/list_dir.rs mod tests
// All 7 Rust unit tests are ported below.

func newListDirInvocation(args map[string]interface{}) *tools.ToolInvocation {
	return &tools.ToolInvocation{
		CallID:    "test-call",
		ToolName:  "list_dir",
		Arguments: args,
	}
}

// Port of: lists_directory_entries
func TestListDir_ListsDirectoryEntries(t *testing.T) {
	dir := t.TempDir()

	subDir := filepath.Join(dir, "nested")
	require.NoError(t, os.Mkdir(subDir, 0o755))

	deeperDir := filepath.Join(subDir, "deeper")
	require.NoError(t, os.Mkdir(deeperDir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "entry.txt"), []byte("content"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "child.txt"), []byte("child"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(deeperDir, "grandchild.txt"), []byte("grandchild"), 0o644))

	// Create symlink (Unix only — skip symlink assertion if it fails).
	hasSymlink := false
	linkPath := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Join(dir, "entry.txt"), linkPath); err == nil {
		hasSymlink = true
	}

	entries, err := listDirSlice(dir, 1, 20, 3)
	require.NoError(t, err)

	if hasSymlink {
		assert.Equal(t, []string{
			"entry.txt",
			"link@",
			"nested/",
			"  child.txt",
			"  deeper/",
			"    grandchild.txt",
		}, entries)
	} else {
		assert.Equal(t, []string{
			"entry.txt",
			"nested/",
			"  child.txt",
			"  deeper/",
			"    grandchild.txt",
		}, entries)
	}
}

// Port of: errors_when_offset_exceeds_entries
func TestListDir_ErrorsWhenOffsetExceedsEntries(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "nested"), 0o755))

	_, err := listDirSlice(dir, 10, 1, 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "offset exceeds directory entry count")
}

// Port of: respects_depth_parameter
func TestListDir_RespectsDepthParameter(t *testing.T) {
	dir := t.TempDir()

	nested := filepath.Join(dir, "nested")
	deeper := filepath.Join(nested, "deeper")
	require.NoError(t, os.Mkdir(nested, 0o755))
	require.NoError(t, os.Mkdir(deeper, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "root.txt"), []byte("root"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "child.txt"), []byte("child"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(deeper, "grandchild.txt"), []byte("deep"), 0o644))

	// depth=1: only top-level entries
	entriesDepth1, err := listDirSlice(dir, 1, 10, 1)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"nested/",
		"root.txt",
	}, entriesDepth1)

	// depth=2: top-level + children of directories
	entriesDepth2, err := listDirSlice(dir, 1, 20, 2)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"nested/",
		"  child.txt",
		"  deeper/",
		"root.txt",
	}, entriesDepth2)

	// depth=3: includes grandchildren
	entriesDepth3, err := listDirSlice(dir, 1, 30, 3)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"nested/",
		"  child.txt",
		"  deeper/",
		"    grandchild.txt",
		"root.txt",
	}, entriesDepth3)
}

// Port of: paginates_in_sorted_order
func TestListDir_PaginatesInSortedOrder(t *testing.T) {
	dir := t.TempDir()

	dirA := filepath.Join(dir, "a")
	dirB := filepath.Join(dir, "b")
	require.NoError(t, os.Mkdir(dirA, 0o755))
	require.NoError(t, os.Mkdir(dirB, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dirA, "a_child.txt"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dirB, "b_child.txt"), []byte("b"), 0o644))

	firstPage, err := listDirSlice(dir, 1, 2, 2)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"a/",
		"  a_child.txt",
		"More than 2 entries found",
	}, firstPage)

	secondPage, err := listDirSlice(dir, 3, 2, 2)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"b/",
		"  b_child.txt",
	}, secondPage)
}

// Port of: handles_large_limit_without_overflow
func TestListDir_HandlesLargeLimitWithoutOverflow(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "alpha.txt"), []byte("alpha"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "beta.txt"), []byte("beta"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gamma.txt"), []byte("gamma"), 0o644))

	entries, err := listDirSlice(dir, 2, math.MaxInt, 1)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"beta.txt",
		"gamma.txt",
	}, entries)
}

// Port of: indicates_truncated_results
func TestListDir_IndicatesTruncatedResults(t *testing.T) {
	dir := t.TempDir()

	for i := 0; i < 40; i++ {
		name := fmt.Sprintf("file_%02d.txt", i)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("content"), 0o644))
	}

	entries, err := listDirSlice(dir, 1, 25, 1)
	require.NoError(t, err)
	assert.Len(t, entries, 26) // 25 entries + "More than..." message
	assert.Equal(t, "More than 25 entries found", entries[len(entries)-1])
}

// Port of: truncation_respects_sorted_order
func TestListDir_TruncationRespectsSortedOrder(t *testing.T) {
	dir := t.TempDir()

	nested := filepath.Join(dir, "nested")
	deeper := filepath.Join(nested, "deeper")
	require.NoError(t, os.Mkdir(nested, 0o755))
	require.NoError(t, os.Mkdir(deeper, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "root.txt"), []byte("root"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "child.txt"), []byte("child"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(deeper, "grandchild.txt"), []byte("deep"), 0o644))

	entries, err := listDirSlice(dir, 1, 3, 3)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"nested/",
		"  child.txt",
		"  deeper/",
		"More than 3 entries found",
	}, entries)
}

// Additional validation tests for the Handle method.

func TestListDir_MissingDirPath(t *testing.T) {
	tool := NewListDirTool()
	inv := newListDirInvocation(map[string]interface{}{})

	_, err := tool.Handle(context.Background(), inv)
	require.Error(t, err)
	assert.True(t, tools.IsValidationError(err))
	assert.Contains(t, err.Error(), "missing required argument: dir_path")
}

func TestListDir_DirPathWrongType(t *testing.T) {
	tool := NewListDirTool()
	inv := newListDirInvocation(map[string]interface{}{
		"dir_path": 123,
	})

	_, err := tool.Handle(context.Background(), inv)
	require.Error(t, err)
	assert.True(t, tools.IsValidationError(err))
	assert.Contains(t, err.Error(), "dir_path must be a string")
}

func TestListDir_EmptyDirPath(t *testing.T) {
	tool := NewListDirTool()
	inv := newListDirInvocation(map[string]interface{}{
		"dir_path": "",
	})

	_, err := tool.Handle(context.Background(), inv)
	require.Error(t, err)
	assert.True(t, tools.IsValidationError(err))
	assert.Contains(t, err.Error(), "dir_path cannot be empty")
}

func TestListDir_RelativePathRejected(t *testing.T) {
	tool := NewListDirTool()
	inv := newListDirInvocation(map[string]interface{}{
		"dir_path": "relative/path",
	})

	_, err := tool.Handle(context.Background(), inv)
	require.Error(t, err)
	assert.True(t, tools.IsValidationError(err))
	assert.Contains(t, err.Error(), "dir_path must be an absolute path")
}

func TestListDir_HandleReturnsAbsolutePathHeader(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.txt"), []byte("x"), 0o644))

	tool := NewListDirTool()
	inv := newListDirInvocation(map[string]interface{}{
		"dir_path": dir,
	})

	output, err := tool.Handle(context.Background(), inv)
	require.NoError(t, err)
	require.NotNil(t, output.Success)
	assert.True(t, *output.Success)
	assert.Contains(t, output.Content, "Absolute path: "+dir)
	assert.Contains(t, output.Content, "test.txt")
}

func TestListDir_NonexistentDirectory(t *testing.T) {
	tool := NewListDirTool()
	inv := newListDirInvocation(map[string]interface{}{
		"dir_path": "/tmp/nonexistent-dir-" + t.Name(),
	})

	output, err := tool.Handle(context.Background(), inv)
	require.NoError(t, err) // filesystem errors are tool output, not Go errors
	require.NotNil(t, output.Success)
	assert.False(t, *output.Success)
	assert.Contains(t, output.Content, "failed to read directory")
}

func TestListDir_ToolMetadata(t *testing.T) {
	tool := NewListDirTool()
	assert.Equal(t, "list_dir", tool.Name())
	assert.Equal(t, tools.ToolKindFunction, tool.Kind())
	assert.False(t, tool.IsMutating(nil))
}
