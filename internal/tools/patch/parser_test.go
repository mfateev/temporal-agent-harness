package patch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests ported from: codex-rs/apply-patch/src/parser.rs tests

func wrapPatch(body string) string {
	return "*** Begin Patch\n" + body + "\n*** End Patch"
}

func TestParse_BadFirstLine(t *testing.T) {
	_, err := Parse("bad")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "The first line of the patch must be '*** Begin Patch'")
}

func TestParse_MissingEndPatch(t *testing.T) {
	_, err := Parse("*** Begin Patch\nbad")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "The last line of the patch must be '*** End Patch'")
}

func TestParse_AddFileWithWhitespaceAroundMarkers(t *testing.T) {
	// The parser should be tolerant of extra whitespace around markers.
	patch := "*** Begin Patch \n*** Add File: foo\n+hi\n *** End Patch"
	p, err := Parse(patch)
	require.NoError(t, err)
	require.Len(t, p.Hunks, 1)
	assert.Equal(t, HunkAdd, p.Hunks[0].Type)
	assert.Equal(t, "foo", p.Hunks[0].Path)
	assert.Equal(t, "hi\n", p.Hunks[0].Contents)
}

func TestParse_EmptyUpdateFileIsError(t *testing.T) {
	_, err := Parse("*** Begin Patch\n*** Update File: test.py\n*** End Patch")
	require.Error(t, err)
	pe, ok := err.(*ParseError)
	require.True(t, ok)
	assert.Contains(t, pe.Message, "Update file hunk for path 'test.py' is empty")
	assert.Equal(t, 2, pe.LineNumber)
}

func TestParse_EmptyPatch(t *testing.T) {
	p, err := Parse("*** Begin Patch\n*** End Patch")
	require.NoError(t, err)
	assert.Empty(t, p.Hunks)
}

func TestParse_FullPatchAllHunkTypes(t *testing.T) {
	input := "*** Begin Patch\n" +
		"*** Add File: path/add.py\n" +
		"+abc\n" +
		"+def\n" +
		"*** Delete File: path/delete.py\n" +
		"*** Update File: path/update.py\n" +
		"*** Move to: path/update2.py\n" +
		"@@ def f():\n" +
		"-    pass\n" +
		"+    return 123\n" +
		"*** End Patch"

	p, err := Parse(input)
	require.NoError(t, err)
	require.Len(t, p.Hunks, 3)

	// Add hunk
	assert.Equal(t, HunkAdd, p.Hunks[0].Type)
	assert.Equal(t, "path/add.py", p.Hunks[0].Path)
	assert.Equal(t, "abc\ndef\n", p.Hunks[0].Contents)

	// Delete hunk
	assert.Equal(t, HunkDelete, p.Hunks[1].Type)
	assert.Equal(t, "path/delete.py", p.Hunks[1].Path)

	// Update hunk
	assert.Equal(t, HunkUpdate, p.Hunks[2].Type)
	assert.Equal(t, "path/update.py", p.Hunks[2].Path)
	assert.Equal(t, "path/update2.py", p.Hunks[2].MovePath)
	require.Len(t, p.Hunks[2].Chunks, 1)
	assert.Equal(t, "def f():", p.Hunks[2].Chunks[0].ChangeContext)
	assert.Equal(t, []string{"    pass"}, p.Hunks[2].Chunks[0].OldLines)
	assert.Equal(t, []string{"    return 123"}, p.Hunks[2].Chunks[0].NewLines)
	assert.False(t, p.Hunks[2].Chunks[0].IsEOF)
}

func TestParse_UpdateHunkFollowedByAddFile(t *testing.T) {
	input := "*** Begin Patch\n" +
		"*** Update File: file.py\n" +
		"@@\n" +
		"+line\n" +
		"*** Add File: other.py\n" +
		"+content\n" +
		"*** End Patch"

	p, err := Parse(input)
	require.NoError(t, err)
	require.Len(t, p.Hunks, 2)

	// Update hunk
	assert.Equal(t, HunkUpdate, p.Hunks[0].Type)
	assert.Equal(t, "file.py", p.Hunks[0].Path)
	require.Len(t, p.Hunks[0].Chunks, 1)
	assert.Empty(t, p.Hunks[0].Chunks[0].OldLines)
	assert.Equal(t, []string{"line"}, p.Hunks[0].Chunks[0].NewLines)

	// Add hunk
	assert.Equal(t, HunkAdd, p.Hunks[1].Type)
	assert.Equal(t, "other.py", p.Hunks[1].Path)
	assert.Equal(t, "content\n", p.Hunks[1].Contents)
}

func TestParse_UpdateWithoutExplicitContextMarker(t *testing.T) {
	// First chunk can omit @@ and start directly with diff lines.
	input := "*** Begin Patch\n*** Update File: file2.py\n import foo\n+bar\n*** End Patch"

	p, err := Parse(input)
	require.NoError(t, err)
	require.Len(t, p.Hunks, 1)
	assert.Equal(t, HunkUpdate, p.Hunks[0].Type)
	require.Len(t, p.Hunks[0].Chunks, 1)
	assert.Equal(t, "", p.Hunks[0].Chunks[0].ChangeContext)
	assert.Equal(t, []string{"import foo"}, p.Hunks[0].Chunks[0].OldLines)
	assert.Equal(t, []string{"import foo", "bar"}, p.Hunks[0].Chunks[0].NewLines)
}

func TestParse_InvalidHunkHeader(t *testing.T) {
	_, err := Parse("*** Begin Patch\n*** Frobnicate File: foo\n*** End Patch")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a valid hunk header")
}

func TestParseUpdateChunk_BadLine(t *testing.T) {
	_, _, err := parseUpdateChunk([]string{"bad"}, 123, false)
	require.Error(t, err)
	pe, ok := err.(*ParseError)
	require.True(t, ok)
	assert.Contains(t, pe.Message, "Expected update hunk to start with a @@ context marker")
	assert.Equal(t, 123, pe.LineNumber)
}

func TestParseUpdateChunk_EmptyAfterMarker(t *testing.T) {
	_, _, err := parseUpdateChunk([]string{"@@"}, 123, false)
	require.Error(t, err)
	pe := err.(*ParseError)
	assert.Contains(t, pe.Message, "Update hunk does not contain any lines")
	assert.Equal(t, 124, pe.LineNumber)
}

func TestParseUpdateChunk_BadFirstDiffLine(t *testing.T) {
	_, _, err := parseUpdateChunk([]string{"@@", "bad"}, 123, false)
	require.Error(t, err)
	pe := err.(*ParseError)
	assert.Contains(t, pe.Message, "Unexpected line found in update hunk")
	assert.Equal(t, 124, pe.LineNumber)
}

func TestParseUpdateChunk_EOFWithoutLines(t *testing.T) {
	_, _, err := parseUpdateChunk([]string{"@@", "*** End of File"}, 123, false)
	require.Error(t, err)
	pe := err.(*ParseError)
	assert.Contains(t, pe.Message, "Update hunk does not contain any lines")
}

func TestParseUpdateChunk_ContextAndDiffLines(t *testing.T) {
	lines := []string{
		"@@ change_context",
		"",
		" context",
		"-remove",
		"+add",
		" context2",
		"*** End Patch",
	}
	chunk, consumed, err := parseUpdateChunk(lines, 123, false)
	require.NoError(t, err)
	assert.Equal(t, 6, consumed)
	assert.Equal(t, "change_context", chunk.ChangeContext)
	assert.Equal(t, []string{"", "context", "remove", "context2"}, chunk.OldLines)
	assert.Equal(t, []string{"", "context", "add", "context2"}, chunk.NewLines)
	assert.False(t, chunk.IsEOF)
}

func TestParseUpdateChunk_EOFMarker(t *testing.T) {
	lines := []string{"@@", "+line", "*** End of File"}
	chunk, consumed, err := parseUpdateChunk(lines, 123, false)
	require.NoError(t, err)
	assert.Equal(t, 3, consumed)
	assert.Empty(t, chunk.OldLines)
	assert.Equal(t, []string{"line"}, chunk.NewLines)
	assert.True(t, chunk.IsEOF)
}

func TestParse_MultipleChunksInUpdateFile(t *testing.T) {
	input := "*** Begin Patch\n" +
		"*** Update File: multi.txt\n" +
		"@@\n" +
		" foo\n" +
		"-bar\n" +
		"+BAR\n" +
		"@@\n" +
		" baz\n" +
		"-qux\n" +
		"+QUX\n" +
		"*** End Patch"

	p, err := Parse(input)
	require.NoError(t, err)
	require.Len(t, p.Hunks, 1)
	h := p.Hunks[0]
	assert.Equal(t, HunkUpdate, h.Type)
	require.Len(t, h.Chunks, 2)

	// First chunk
	assert.Equal(t, []string{"foo", "bar"}, h.Chunks[0].OldLines)
	assert.Equal(t, []string{"foo", "BAR"}, h.Chunks[0].NewLines)

	// Second chunk
	assert.Equal(t, []string{"baz", "qux"}, h.Chunks[1].OldLines)
	assert.Equal(t, []string{"baz", "QUX"}, h.Chunks[1].NewLines)
}

func TestParse_InterleavedChangesWithEOF(t *testing.T) {
	input := "*** Begin Patch\n" +
		"*** Update File: interleaved.txt\n" +
		"@@\n" +
		" a\n" +
		"-b\n" +
		"+B\n" +
		"@@\n" +
		" c\n" +
		" d\n" +
		"-e\n" +
		"+E\n" +
		"@@\n" +
		" f\n" +
		"+g\n" +
		"*** End of File\n" +
		"*** End Patch"

	p, err := Parse(input)
	require.NoError(t, err)
	require.Len(t, p.Hunks, 1)
	h := p.Hunks[0]
	require.Len(t, h.Chunks, 3)

	assert.Equal(t, []string{"a", "b"}, h.Chunks[0].OldLines)
	assert.Equal(t, []string{"a", "B"}, h.Chunks[0].NewLines)
	assert.False(t, h.Chunks[0].IsEOF)

	assert.Equal(t, []string{"c", "d", "e"}, h.Chunks[1].OldLines)
	assert.Equal(t, []string{"c", "d", "E"}, h.Chunks[1].NewLines)
	assert.False(t, h.Chunks[1].IsEOF)

	assert.Equal(t, []string{"f"}, h.Chunks[2].OldLines)
	assert.Equal(t, []string{"f", "g"}, h.Chunks[2].NewLines)
	assert.True(t, h.Chunks[2].IsEOF)
}

func TestParse_PureAddition(t *testing.T) {
	input := "*** Begin Patch\n" +
		"*** Update File: file.py\n" +
		"@@\n" +
		"+new line\n" +
		"*** End Patch"

	p, err := Parse(input)
	require.NoError(t, err)
	require.Len(t, p.Hunks, 1)
	require.Len(t, p.Hunks[0].Chunks, 1)
	assert.Empty(t, p.Hunks[0].Chunks[0].OldLines)
	assert.Equal(t, []string{"new line"}, p.Hunks[0].Chunks[0].NewLines)
}

func TestParse_MoveToWithoutChunks(t *testing.T) {
	// Move without content changes - still needs at least one chunk
	_, err := Parse("*** Begin Patch\n*** Update File: old.txt\n*** Move to: new.txt\n*** End Patch")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestParse_MultipleAddFiles(t *testing.T) {
	input := "*** Begin Patch\n" +
		"*** Add File: a.txt\n" +
		"+hello\n" +
		"*** Add File: b.txt\n" +
		"+world\n" +
		"*** End Patch"

	p, err := Parse(input)
	require.NoError(t, err)
	require.Len(t, p.Hunks, 2)
	assert.Equal(t, "a.txt", p.Hunks[0].Path)
	assert.Equal(t, "hello\n", p.Hunks[0].Contents)
	assert.Equal(t, "b.txt", p.Hunks[1].Path)
	assert.Equal(t, "world\n", p.Hunks[1].Contents)
}

func TestParse_ChangeContextInChunk(t *testing.T) {
	input := "*** Begin Patch\n" +
		"*** Update File: app.py\n" +
		"@@ def method():\n" +
		"-    return False\n" +
		"+    return True\n" +
		"*** End Patch"

	p, err := Parse(input)
	require.NoError(t, err)
	require.Len(t, p.Hunks, 1)
	require.Len(t, p.Hunks[0].Chunks, 1)
	assert.Equal(t, "def method():", p.Hunks[0].Chunks[0].ChangeContext)
	assert.Equal(t, []string{"    return False"}, p.Hunks[0].Chunks[0].OldLines)
	assert.Equal(t, []string{"    return True"}, p.Hunks[0].Chunks[0].NewLines)
}
