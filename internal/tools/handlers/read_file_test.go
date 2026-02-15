package handlers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mfateev/temporal-agent-harness/internal/tools"
)

func newReadInvocation(args map[string]interface{}) *tools.ToolInvocation {
	return &tools.ToolInvocation{
		CallID:    "test-call",
		ToolName:  "read_file",
		Arguments: args,
	}
}

// ---------------------------------------------------------------------------
// Existing slice-mode tests (unchanged)
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Helper: write temp file and return its path
// ---------------------------------------------------------------------------

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return path
}

// extractLineNums parses the formatted output (after the "File: ..." header)
// and returns the 1-indexed line numbers that appear.
func extractLineNums(content string) []int {
	var nums []int
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "File: ") {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(line, "%d\t", &n); err == nil {
			nums = append(nums, n)
		}
	}
	return nums
}

// extractLines returns the raw text (after line-number prefix) for each output line.
func extractLines(content string) []string {
	var result []string
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "File: ") {
			continue
		}
		idx := strings.IndexByte(line, '\t')
		if idx >= 0 {
			result = append(result, line[idx+1:])
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Indentation mode tests (ported from codex-rs)
// ---------------------------------------------------------------------------

// TestReadFile_IndentationMode_SingleBlock anchors on inner(); max_levels=1.
// Input:
//
//	fn outer() {
//	    if cond {
//	        inner();
//	    }
//	    tail();
//	}
//
// anchor_line=3, max_levels=1, include_siblings=false
// Expected: lines 2-4 (if cond { ... inner(); ... })
func TestReadFile_IndentationMode_SingleBlock(t *testing.T) {
	content := "fn outer() {\n    if cond {\n        inner();\n    }\n    tail();\n}\n"
	path := writeTempFile(t, content)

	tool := NewReadFileTool()
	out, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"file_path": path,
		"mode":      "indentation",
		"indentation": map[string]interface{}{
			"anchor_line": 3,
			"max_levels":  1,
		},
	}))
	require.NoError(t, err)
	require.NotNil(t, out.Success)
	assert.True(t, *out.Success)

	nums := extractLineNums(out.Content)
	assert.Equal(t, []int{2, 3, 4}, nums, "should include lines 2-4")
}

// TestReadFile_IndentationMode_MultipleLevels uses max_levels=3 to walk all the
// way up to the root level.
// Input (4 levels of nesting):
//
//	mod root {
//	    fn outer() {
//	        if cond {
//	            inner();
//	        }
//	    }
//	}
//
// anchor_line=4, max_levels=3
// Expected: all 7 lines
func TestReadFile_IndentationMode_MultipleLevels(t *testing.T) {
	content := "mod root {\n    fn outer() {\n        if cond {\n            inner();\n        }\n    }\n}\n"
	path := writeTempFile(t, content)

	tool := NewReadFileTool()
	out, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"file_path": path,
		"mode":      "indentation",
		"indentation": map[string]interface{}{
			"anchor_line": 4,
			"max_levels":  3,
		},
	}))
	require.NoError(t, err)
	require.NotNil(t, out.Success)
	assert.True(t, *out.Success)

	nums := extractLineNums(out.Content)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7}, nums, "should include all 7 lines")
}

// TestReadFile_IndentationMode_IncludeSiblings tests include_siblings=true.
// Input:
//
//	fn wrapper() {
//	    if first {
//	        do_first();
//	    }
//	    if second {
//	        do_second();
//	    }
//	}
//
// anchor_line=3, max_levels=1, include_siblings=true
// Expected: lines 2-7 (both if blocks)
func TestReadFile_IndentationMode_IncludeSiblings(t *testing.T) {
	content := "fn wrapper() {\n    if first {\n        do_first();\n    }\n    if second {\n        do_second();\n    }\n}\n"
	path := writeTempFile(t, content)

	tool := NewReadFileTool()

	// With include_siblings=true: should include both if blocks.
	out, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"file_path": path,
		"mode":      "indentation",
		"indentation": map[string]interface{}{
			"anchor_line":      3,
			"max_levels":       1,
			"include_siblings": true,
		},
	}))
	require.NoError(t, err)
	require.NotNil(t, out.Success)
	assert.True(t, *out.Success)

	nums := extractLineNums(out.Content)
	assert.Equal(t, []int{2, 3, 4, 5, 6, 7}, nums, "include_siblings=true should include lines 2-7")

	// With include_siblings=false (default): should include only the first block.
	out2, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"file_path": path,
		"mode":      "indentation",
		"indentation": map[string]interface{}{
			"anchor_line": 3,
			"max_levels":  1,
		},
	}))
	require.NoError(t, err)

	nums2 := extractLineNums(out2.Content)
	assert.Equal(t, []int{2, 3, 4}, nums2, "include_siblings=false should include only lines 2-4")
}

// TestReadFile_IndentationMode_IncludeHeader tests that comment headers above
// the block are included when include_header=true.
// Input:
//
//	// This is the outer function
//	fn outer() {
//	    inner();
//	}
//
// anchor_line=3, max_levels=0 (unlimited), include_header=true
// Expected: all 4 lines (comment included)
func TestReadFile_IndentationMode_IncludeHeader(t *testing.T) {
	content := "// This is the outer function\nfn outer() {\n    inner();\n}\n"
	path := writeTempFile(t, content)

	tool := NewReadFileTool()
	out, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"file_path": path,
		"mode":      "indentation",
		"indentation": map[string]interface{}{
			"anchor_line":    3,
			"max_levels":     0,
			"include_header": true,
		},
	}))
	require.NoError(t, err)
	require.NotNil(t, out.Success)
	assert.True(t, *out.Success)

	nums := extractLineNums(out.Content)
	assert.Equal(t, []int{1, 2, 3, 4}, nums, "include_header should include the comment line")
}

// TestReadFile_IndentationMode_MaxLines tests the max_lines cap.
// With deep nesting, max_lines=3 should only return 3 lines centered on anchor.
func TestReadFile_IndentationMode_MaxLines(t *testing.T) {
	content := "mod root {\n    fn outer() {\n        if cond {\n            inner();\n        }\n    }\n}\n"
	path := writeTempFile(t, content)

	tool := NewReadFileTool()
	out, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"file_path": path,
		"mode":      "indentation",
		"indentation": map[string]interface{}{
			"anchor_line": 4,
			"max_levels":  0,
			"max_lines":   3,
		},
	}))
	require.NoError(t, err)
	require.NotNil(t, out.Success)
	assert.True(t, *out.Success)

	nums := extractLineNums(out.Content)
	assert.Len(t, nums, 3, "max_lines=3 should return exactly 3 lines")
	// The anchor line (4) should be included.
	assert.Contains(t, nums, 4, "anchor line must be in the result")
}

// TestReadFile_IndentationMode_BlankLinesInheritIndent verifies that blank
// lines between statements inherit the indent of the previous non-blank line.
// Input:
//
//	fn foo() {
//	    a();
//
//	    b();
//	}
//
// anchor_line=2, max_levels=1
// Expected: all 5 lines (blank line inherits indent of a())
func TestReadFile_IndentationMode_BlankLinesInheritIndent(t *testing.T) {
	content := "fn foo() {\n    a();\n\n    b();\n}\n"
	path := writeTempFile(t, content)

	tool := NewReadFileTool()
	out, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"file_path": path,
		"mode":      "indentation",
		"indentation": map[string]interface{}{
			"anchor_line":      2,
			"max_levels":       1,
			"include_siblings": true,
		},
	}))
	require.NoError(t, err)
	require.NotNil(t, out.Success)
	assert.True(t, *out.Success)

	nums := extractLineNums(out.Content)
	assert.Equal(t, []int{1, 2, 3, 4, 5}, nums, "blank line should be included via inherited indent")
}

// TestReadFile_IndentationMode_DefaultsToSlice verifies that no mode param
// or mode="slice" produces the same result as the original slice behavior.
func TestReadFile_IndentationMode_DefaultsToSlice(t *testing.T) {
	content := "line1\nline2\nline3\n"
	path := writeTempFile(t, content)

	tool := NewReadFileTool()

	// No mode argument at all.
	out1, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"file_path": path,
	}))
	require.NoError(t, err)
	require.True(t, *out1.Success)

	// Explicit mode="slice".
	out2, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"file_path": path,
		"mode":      "slice",
	}))
	require.NoError(t, err)
	require.True(t, *out2.Success)

	assert.Equal(t, out1.Content, out2.Content, "no mode and mode=slice should produce identical output")
	assert.Contains(t, out1.Content, "     1\tline1")
	assert.Contains(t, out1.Content, "     2\tline2")
	assert.Contains(t, out1.Content, "     3\tline3")
}

// TestReadFile_IndentationMode_UnlimitedLevels verifies that max_levels=0
// goes all the way to the root (indent 0).
func TestReadFile_IndentationMode_UnlimitedLevels(t *testing.T) {
	content := "mod root {\n    fn outer() {\n        if cond {\n            inner();\n        }\n    }\n}\n"
	path := writeTempFile(t, content)

	tool := NewReadFileTool()
	out, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"file_path": path,
		"mode":      "indentation",
		"indentation": map[string]interface{}{
			"anchor_line": 4,
			"max_levels":  0,
		},
	}))
	require.NoError(t, err)
	require.NotNil(t, out.Success)
	assert.True(t, *out.Success)

	nums := extractLineNums(out.Content)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7}, nums, "max_levels=0 should include all lines to root")
}

// TestReadFile_IndentationMode_AnchorDefaultsToOffset verifies that when
// anchor_line is not provided, the offset parameter is used as anchor.
func TestReadFile_IndentationMode_AnchorDefaultsToOffset(t *testing.T) {
	content := "fn outer() {\n    if cond {\n        inner();\n    }\n    tail();\n}\n"
	path := writeTempFile(t, content)

	tool := NewReadFileTool()

	// Using anchor_line explicitly.
	out1, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"file_path": path,
		"mode":      "indentation",
		"indentation": map[string]interface{}{
			"anchor_line": 3,
			"max_levels":  1,
		},
	}))
	require.NoError(t, err)

	// Using offset as fallback for anchor.
	out2, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"file_path": path,
		"offset":    3,
		"mode":      "indentation",
		"indentation": map[string]interface{}{
			"max_levels": 1,
		},
	}))
	require.NoError(t, err)

	assert.Equal(t, out1.Content, out2.Content, "offset should be used when anchor_line is not set")
}

// TestReadFile_IndentationMode_EmptyFile verifies indentation mode handles
// empty files gracefully.
func TestReadFile_IndentationMode_EmptyFile(t *testing.T) {
	path := writeTempFile(t, "")

	tool := NewReadFileTool()
	out, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"file_path": path,
		"mode":      "indentation",
		"indentation": map[string]interface{}{
			"anchor_line": 1,
			"max_levels":  0,
		},
	}))
	require.NoError(t, err)
	require.NotNil(t, out.Success)
	assert.True(t, *out.Success)
	assert.Contains(t, out.Content, "(empty file)")
}

// ---------------------------------------------------------------------------
// Unit tests for helper functions
// ---------------------------------------------------------------------------

func TestMeasureIndent(t *testing.T) {
	tests := []struct {
		line     string
		expected int
	}{
		{"no indent", 0},
		{"    four spaces", 4},
		{"\tone tab", 4},
		{"\t\ttwo tabs", 8},
		{"  \t mixed", 7}, // 2 spaces + 1 tab + 1 space = 2 + 4 + 1 = 7
		{"", 0},
		{"   ", 3},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.line), func(t *testing.T) {
			assert.Equal(t, tt.expected, measureIndent(tt.line))
		})
	}
}

func TestIsComment(t *testing.T) {
	tests := []struct {
		line     string
		expected bool
	}{
		{"// C-style comment", true},
		{"# Python comment", true},
		{"-- SQL comment", true},
		{"    // indented C comment", true},
		{"\t# indented Python comment", true},
		{"not a comment", false},
		{"fn foo() {", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.line), func(t *testing.T) {
			assert.Equal(t, tt.expected, isComment(tt.line))
		})
	}
}

func TestComputeEffectiveIndents(t *testing.T) {
	records := []lineRecord{
		{raw: "fn foo() {", indent: 0, lineNum: 1},
		{raw: "    a();", indent: 4, lineNum: 2},
		{raw: "", indent: 0, lineNum: 3},           // blank
		{raw: "    b();", indent: 4, lineNum: 4},
		{raw: "}", indent: 0, lineNum: 5},
	}
	eff := computeEffectiveIndents(records)
	assert.Equal(t, []int{0, 4, 4, 4, 0}, eff, "blank line should inherit indent of previous non-blank")
}

func TestTrimBlankLines(t *testing.T) {
	records := []lineRecord{
		{raw: "", lineNum: 1},
		{raw: "  ", lineNum: 2},
		{raw: "hello", lineNum: 3},
		{raw: "world", lineNum: 4},
		{raw: "", lineNum: 5},
	}
	trimmed := trimBlankLines(records)
	assert.Len(t, trimmed, 2)
	assert.Equal(t, "hello", trimmed[0].raw)
	assert.Equal(t, "world", trimmed[1].raw)
}

// TestReadFile_IndentationMode_TabIndent verifies that tab-indented files work.
func TestReadFile_IndentationMode_TabIndent(t *testing.T) {
	content := "fn outer() {\n\tif cond {\n\t\tinner();\n\t}\n\ttail();\n}\n"
	path := writeTempFile(t, content)

	tool := NewReadFileTool()
	out, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"file_path": path,
		"mode":      "indentation",
		"indentation": map[string]interface{}{
			"anchor_line": 3,
			"max_levels":  1,
		},
	}))
	require.NoError(t, err)
	require.NotNil(t, out.Success)
	assert.True(t, *out.Success)

	nums := extractLineNums(out.Content)
	assert.Equal(t, []int{2, 3, 4}, nums, "tab-indented file should work like space-indented")
}

// TestReadFile_IndentationMode_FilePathHeader checks that indentation mode
// output includes the "File: <path>" header.
func TestReadFile_IndentationMode_FilePathHeader(t *testing.T) {
	content := "fn foo() {\n    bar();\n}\n"
	path := writeTempFile(t, content)

	tool := NewReadFileTool()
	out, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"file_path": path,
		"mode":      "indentation",
		"indentation": map[string]interface{}{
			"anchor_line": 2,
			"max_levels":  0,
		},
	}))
	require.NoError(t, err)
	assert.Contains(t, out.Content, "File: "+path+"\n")
}

// TestReadFile_IndentationMode_AnchorClampedToFileEnd verifies that an anchor
// line beyond the file length is clamped to the last line.
func TestReadFile_IndentationMode_AnchorClampedToFileEnd(t *testing.T) {
	content := "fn foo() {\n    bar();\n}\n"
	path := writeTempFile(t, content)

	tool := NewReadFileTool()
	out, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"file_path": path,
		"mode":      "indentation",
		"indentation": map[string]interface{}{
			"anchor_line": 100,
			"max_levels":  0,
		},
	}))
	require.NoError(t, err)
	require.NotNil(t, out.Success)
	assert.True(t, *out.Success)
	// Should still produce valid output (anchored at last line).
	nums := extractLineNums(out.Content)
	assert.NotEmpty(t, nums)
}

// TestReadFile_IndentationMode_IncludeHeaderWithComment tests include_header
// with multiple comment prefixes.
func TestReadFile_IndentationMode_IncludeHeaderWithComment(t *testing.T) {
	// Two comment lines above a function, with another block above the comments.
	content := "fn other() {\n}\n// doc line 1\n// doc line 2\nfn target() {\n    body();\n}\n"
	path := writeTempFile(t, content)

	tool := NewReadFileTool()
	out, err := tool.Handle(context.Background(), newReadInvocation(map[string]interface{}{
		"file_path": path,
		"mode":      "indentation",
		"indentation": map[string]interface{}{
			"anchor_line":    6,
			"max_levels":     0,
			"include_header": true,
		},
	}))
	require.NoError(t, err)
	require.NotNil(t, out.Success)
	assert.True(t, *out.Success)

	nums := extractLineNums(out.Content)
	// Anchor is line 6 (indent=4), max_levels=0 so min_indent=0,
	// include_siblings=false (default).
	// Expanding up: line 5 "fn target() {" (indent 0 == minIndent) is the first
	// sibling at min. Line 4 "// doc line 2" (indent 0 == minIndent) would be the
	// second sibling, but include_header=true triggers comment scanning above the
	// stop point, so comment lines 3-4 are included. Lines 1-2 are not comments,
	// so scanning stops.
	// Expanding down: line 7 "}" (indent 0 == minIndent) is the first sibling.
	assert.Equal(t, []int{3, 4, 5, 6, 7}, nums)
}

// TestParseIndentationOptions verifies parsing of the indentation argument map.
func TestParseIndentationOptions(t *testing.T) {
	m := map[string]interface{}{
		"anchor_line":      float64(5),
		"max_levels":       float64(2),
		"include_siblings": true,
		"include_header":   true,
		"max_lines":        float64(10),
	}
	opts := parseIndentationOptions(m)
	assert.Equal(t, 5, opts.anchorLine)
	assert.Equal(t, 2, opts.maxLevels)
	assert.True(t, opts.includeSiblings)
	assert.True(t, opts.includeHeader)
	assert.Equal(t, 10, opts.maxLines)
}

// TestParseIndentationOptions_Defaults verifies zero values for missing keys.
func TestParseIndentationOptions_Defaults(t *testing.T) {
	opts := parseIndentationOptions(map[string]interface{}{})
	assert.Equal(t, 0, opts.anchorLine)
	assert.Equal(t, 0, opts.maxLevels)
	assert.False(t, opts.includeSiblings)
	assert.False(t, opts.includeHeader)
	assert.Equal(t, 0, opts.maxLines)
}
