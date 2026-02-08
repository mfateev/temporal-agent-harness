package patch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests ported from: codex-rs/apply-patch/src/lib.rs mod tests
// and codex-rs/core/tests/suite/apply_patch_cli.rs

// Helper to construct a patch with the given body.
func wrapPatchBody(body string) string {
	return "*** Begin Patch\n" + body + "\n*** End Patch"
}

func TestApply_AddFileCreatesFileWithContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "add.txt")

	patch := wrapPatchBody("*** Add File: " + path + "\n+ab\n+cd")

	result, err := Apply(patch, dir)
	require.NoError(t, err)
	assert.Contains(t, result, "Success")
	assert.Contains(t, result, "A "+path)

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "ab\ncd\n", string(contents))
}

func TestApply_DeleteFileRemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "del.txt")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

	patch := wrapPatchBody("*** Delete File: " + path)

	result, err := Apply(patch, dir)
	require.NoError(t, err)
	assert.Contains(t, result, "D "+path)
	assert.NoFileExists(t, path)
}

func TestApply_UpdateFileModifiesContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "update.txt")
	require.NoError(t, os.WriteFile(path, []byte("foo\nbar\n"), 0o644))

	patch := wrapPatchBody("*** Update File: " + path + "\n@@\n foo\n-bar\n+baz")

	result, err := Apply(patch, dir)
	require.NoError(t, err)
	assert.Contains(t, result, "M "+path)

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "foo\nbaz\n", string(contents))
}

func TestApply_UpdateFileCanMoveFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dest := filepath.Join(dir, "dst.txt")
	require.NoError(t, os.WriteFile(src, []byte("line\n"), 0o644))

	patch := wrapPatchBody(
		"*** Update File: " + src + "\n" +
			"*** Move to: " + dest + "\n" +
			"@@\n-line\n+line2")

	result, err := Apply(patch, dir)
	require.NoError(t, err)
	assert.Contains(t, result, "M "+dest)
	assert.NoFileExists(t, src)

	contents, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, "line2\n", string(contents))
}

func TestApply_MultipleUpdateChunks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multi.txt")
	require.NoError(t, os.WriteFile(path, []byte("foo\nbar\nbaz\nqux\n"), 0o644))

	patch := wrapPatchBody(
		"*** Update File: " + path + "\n" +
			"@@\n foo\n-bar\n+BAR\n" +
			"@@\n baz\n-qux\n+QUX")

	_, err := Apply(patch, dir)
	require.NoError(t, err)

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "foo\nBAR\nbaz\nQUX\n", string(contents))
}

func TestApply_InterleavedChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "interleaved.txt")
	require.NoError(t, os.WriteFile(path, []byte("a\nb\nc\nd\ne\nf\n"), 0o644))

	patch := wrapPatchBody(
		"*** Update File: " + path + "\n" +
			"@@\n a\n-b\n+B\n" +
			"@@\n c\n d\n-e\n+E\n" +
			"@@\n f\n+g\n*** End of File")

	_, err := Apply(patch, dir)
	require.NoError(t, err)

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "a\nB\nc\nd\nE\nf\ng\n", string(contents))
}

func TestApply_PureAdditionFollowedByRemoval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "panic.txt")
	require.NoError(t, os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644))

	patch := wrapPatchBody(
		"*** Update File: " + path + "\n" +
			"@@\n+after-context\n+second-line\n" +
			"@@\n line1\n-line2\n-line3\n+line2-replacement")

	_, err := Apply(patch, dir)
	require.NoError(t, err)

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "line1\nline2-replacement\nafter-context\nsecond-line\n", string(contents))
}

func TestApply_UpdateLineWithUnicodeDash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unicode.py")

	// Original line contains EN DASH (\u2013) and NON-BREAKING HYPHEN (\u2011).
	original := "import asyncio  # local import \u2013 avoids top\u2011level dep\n"
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))

	// Patch uses plain ASCII dash / hyphen.
	patch := wrapPatchBody(
		"*** Update File: " + path + "\n@@\n" +
			"-import asyncio  # local import - avoids top-level dep\n" +
			"+import asyncio  # HELLO")

	result, err := Apply(patch, dir)
	require.NoError(t, err)
	assert.Contains(t, result, "M "+path)

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "import asyncio  # HELLO\n", string(contents))
}

func TestApply_MultipleOperations(t *testing.T) {
	dir := t.TempDir()

	modifyPath := filepath.Join(dir, "modify.txt")
	deletePath := filepath.Join(dir, "delete.txt")
	require.NoError(t, os.WriteFile(modifyPath, []byte("line1\nline2\n"), 0o644))
	require.NoError(t, os.WriteFile(deletePath, []byte("obsolete\n"), 0o644))

	patch := wrapPatchBody(
		"*** Add File: " + filepath.Join(dir, "nested", "new.txt") + "\n+created\n" +
			"*** Delete File: " + deletePath + "\n" +
			"*** Update File: " + modifyPath + "\n@@\n-line2\n+changed")

	result, err := Apply(patch, dir)
	require.NoError(t, err)
	assert.Contains(t, result, "A "+filepath.Join(dir, "nested", "new.txt"))
	assert.Contains(t, result, "M "+modifyPath)
	assert.Contains(t, result, "D "+deletePath)

	// Verify new file
	newContents, err := os.ReadFile(filepath.Join(dir, "nested", "new.txt"))
	require.NoError(t, err)
	assert.Equal(t, "created\n", string(newContents))

	// Verify modified file
	modContents, err := os.ReadFile(modifyPath)
	require.NoError(t, err)
	assert.Equal(t, "line1\nchanged\n", string(modContents))

	// Verify deleted file
	assert.NoFileExists(t, deletePath)
}

func TestApply_MultipleChunks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "multi.txt")
	require.NoError(t, os.WriteFile(target, []byte("line1\nline2\nline3\nline4\n"), 0o644))

	patch := wrapPatchBody(
		"*** Update File: " + target + "\n@@\n-line2\n+changed2\n@@\n-line4\n+changed4")

	_, err := Apply(patch, dir)
	require.NoError(t, err)

	contents, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "line1\nchanged2\nline3\nchanged4\n", string(contents))
}

func TestApply_MoveFileToNewDirectory(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "old", "name.txt")
	newPath := filepath.Join(dir, "renamed", "dir", "name.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(original), 0o755))
	require.NoError(t, os.WriteFile(original, []byte("old content\n"), 0o644))

	patch := wrapPatchBody(
		"*** Update File: " + original + "\n" +
			"*** Move to: " + newPath + "\n" +
			"@@\n-old content\n+new content")

	_, err := Apply(patch, dir)
	require.NoError(t, err)

	assert.NoFileExists(t, original)
	contents, err := os.ReadFile(newPath)
	require.NoError(t, err)
	assert.Equal(t, "new content\n", string(contents))
}

func TestApply_UpdateAppendsTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "no_newline.txt")
	require.NoError(t, os.WriteFile(target, []byte("no newline at end"), 0o644))

	patch := wrapPatchBody(
		"*** Update File: " + target + "\n@@\n-no newline at end\n+first line\n+second line")

	_, err := Apply(patch, dir)
	require.NoError(t, err)

	contents, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.True(t, contents[len(contents)-1] == '\n')
	assert.Equal(t, "first line\nsecond line\n", string(contents))
}

func TestApply_InsertOnlyHunk(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "insert_only.txt")
	require.NoError(t, os.WriteFile(target, []byte("alpha\nomega\n"), 0o644))

	patch := wrapPatchBody(
		"*** Update File: " + target + "\n@@\n alpha\n+beta\n omega")

	_, err := Apply(patch, dir)
	require.NoError(t, err)

	contents, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "alpha\nbeta\nomega\n", string(contents))
}

func TestApply_MoveOverwritesExistingDestination(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "old", "name.txt")
	destination := filepath.Join(dir, "renamed", "dir", "name.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(original), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(destination), 0o755))
	require.NoError(t, os.WriteFile(original, []byte("from\n"), 0o644))
	require.NoError(t, os.WriteFile(destination, []byte("existing\n"), 0o644))

	patch := wrapPatchBody(
		"*** Update File: " + original + "\n" +
			"*** Move to: " + destination + "\n" +
			"@@\n-from\n+new")

	_, err := Apply(patch, dir)
	require.NoError(t, err)

	assert.NoFileExists(t, original)
	contents, err := os.ReadFile(destination)
	require.NoError(t, err)
	assert.Equal(t, "new\n", string(contents))
}

func TestApply_AddOverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "duplicate.txt")
	require.NoError(t, os.WriteFile(path, []byte("old content\n"), 0o644))

	patch := wrapPatchBody("*** Add File: " + path + "\n+new content")

	_, err := Apply(patch, dir)
	require.NoError(t, err)

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new content\n", string(contents))
}

func TestApply_RejectsMissingContext(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "modify.txt")
	require.NoError(t, os.WriteFile(target, []byte("line1\nline2\n"), 0o644))

	patch := wrapPatchBody(
		"*** Update File: " + target + "\n@@\n-missing\n+changed")

	_, err := Apply(patch, dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Failed to find expected lines in")

	// Original file unchanged.
	contents, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "line1\nline2\n", string(contents))
}

func TestApply_ReportsMissingTargetFile(t *testing.T) {
	dir := t.TempDir()

	patch := wrapPatchBody(
		"*** Update File: " + filepath.Join(dir, "missing.txt") + "\n@@\n-nope\n+better")

	_, err := Apply(patch, dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Failed to read file to update")
	assert.Contains(t, err.Error(), "missing.txt")
}

func TestApply_DeleteMissingFileReportsError(t *testing.T) {
	dir := t.TempDir()

	patch := wrapPatchBody("*** Delete File: " + filepath.Join(dir, "missing.txt"))

	_, err := Apply(patch, dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Failed to read file to delete")
	assert.Contains(t, err.Error(), "missing.txt")
}

func TestApply_RejectsEmptyPatch(t *testing.T) {
	dir := t.TempDir()
	_, err := Apply("*** Begin Patch\n*** End Patch", dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty patch")
}

func TestApply_DeleteDirectoryReportsError(t *testing.T) {
	dir := t.TempDir()
	dirPath := filepath.Join(dir, "subdir")
	require.NoError(t, os.Mkdir(dirPath, 0o755))

	patch := wrapPatchBody("*** Delete File: " + dirPath)

	_, err := Apply(patch, dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a directory")
}

func TestApply_EOFAnchor(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tail.txt")
	require.NoError(t, os.WriteFile(target, []byte("alpha\nlast\n"), 0o644))

	patch := wrapPatchBody(
		"*** Update File: " + target + "\n@@\n-last\n+end\n*** End of File")

	_, err := Apply(patch, dir)
	require.NoError(t, err)

	contents, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "alpha\nend\n", string(contents))
}

func TestApply_ChangeContextDisambiguatesTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "multi_ctx.txt")
	require.NoError(t, os.WriteFile(target, []byte("fn a\nx=10\ny=2\nfn b\nx=10\ny=20\n"), 0o644))

	patch := wrapPatchBody(
		"*** Update File: " + target + "\n@@ fn b\n-x=10\n+x=11")

	_, err := Apply(patch, dir)
	require.NoError(t, err)

	contents, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "fn a\nx=10\ny=2\nfn b\nx=11\ny=20\n", string(contents))
}

func TestApply_VerificationFailureHasNoSideEffects(t *testing.T) {
	dir := t.TempDir()

	// Patch tries to create a file, then update a missing file.
	// The whole patch should fail and the created file should not exist.
	patch := wrapPatchBody(
		"*** Add File: " + filepath.Join(dir, "created.txt") + "\n+hello\n" +
			"*** Update File: " + filepath.Join(dir, "missing.txt") + "\n@@\n-old\n+new")

	_, err := Apply(patch, dir)
	require.Error(t, err)

	// The Add hunk should NOT have been applied because verification
	// discovered the missing update target up front.
	assert.NoFileExists(t, filepath.Join(dir, "created.txt"))
}

func TestApply_RelativePaths(t *testing.T) {
	dir := t.TempDir()

	// Use relative paths — the cwd should be used to resolve them.
	patch := wrapPatchBody("*** Add File: hello.txt\n+world")

	result, err := Apply(patch, dir)
	require.NoError(t, err)
	assert.Contains(t, result, "A hello.txt")

	contents, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "world\n", string(contents))
}

func TestApply_ReadonlyFileError(t *testing.T) {
	// Verify that the OS actually enforces readonly permissions, as some
	// environments (e.g., containers with CAP_DAC_OVERRIDE) allow writes
	// to readonly files even for non-root users.
	probe := filepath.Join(t.TempDir(), "probe")
	os.WriteFile(probe, []byte("x"), 0o444)
	if err := os.WriteFile(probe, []byte("y"), 0o444); err == nil {
		t.Skip("Skipping readonly test: environment does not enforce file permissions")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "readonly.txt")
	require.NoError(t, os.WriteFile(path, []byte("before\n"), 0o644))
	require.NoError(t, os.Chmod(path, 0o444))
	t.Cleanup(func() { os.Chmod(path, 0o644) })

	patch := wrapPatchBody(
		"*** Update File: " + path + "\n@@\n-before\n+after")

	_, err := Apply(patch, dir)
	require.Error(t, err)
}

func TestApply_MoveWithContextOnly(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "old", "name.txt")
	destination := filepath.Join(dir, "renamed", "name.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(original), 0o755))
	require.NoError(t, os.WriteFile(original, []byte("same\n"), 0o644))

	// Move with context line only (no actual content change).
	patch := wrapPatchBody(
		"*** Update File: " + original + "\n" +
			"*** Move to: " + destination + "\n" +
			"@@\n same")

	_, err := Apply(patch, dir)
	require.NoError(t, err)

	assert.NoFileExists(t, original)
	contents, err := os.ReadFile(destination)
	require.NoError(t, err)
	assert.Equal(t, "same\n", string(contents))
}

func TestApply_NestedDirectoryCreation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "deep.txt")

	patch := wrapPatchBody("*** Add File: " + path + "\n+deep content")

	_, err := Apply(patch, dir)
	require.NoError(t, err)

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "deep content\n", string(contents))
}

func TestApply_FirstLineReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "first.txt")
	require.NoError(t, os.WriteFile(path, []byte("foo\nbar\nbaz\n"), 0o644))

	patch := wrapPatchBody(
		"*** Update File: " + path + "\n@@\n-foo\n+FOO\n bar")

	_, err := Apply(patch, dir)
	require.NoError(t, err)

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "FOO\nbar\nbaz\n", string(contents))
}

func TestApply_LastLineReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "last.txt")
	require.NoError(t, os.WriteFile(path, []byte("foo\nbar\nbaz\n"), 0o644))

	patch := wrapPatchBody(
		"*** Update File: " + path + "\n@@\n foo\n bar\n-baz\n+BAZ")

	_, err := Apply(patch, dir)
	require.NoError(t, err)

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "foo\nbar\nBAZ\n", string(contents))
}

func TestApply_InsertAtEOF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "insert.txt")
	require.NoError(t, os.WriteFile(path, []byte("foo\nbar\nbaz\n"), 0o644))

	patch := wrapPatchBody(
		"*** Update File: " + path + "\n@@\n+quux\n*** End of File")

	_, err := Apply(patch, dir)
	require.NoError(t, err)

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "foo\nbar\nbaz\nquux\n", string(contents))
}
