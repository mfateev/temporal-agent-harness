package handlers

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mfateev/temporal-agent-harness/internal/tools"
)

// tabWidth is the number of spaces that a tab character counts as for
// indentation measurement.
//
// Maps to: codex-rs/core/src/tools/handlers/read_file.rs TAB_WIDTH
const tabWidth = 4

// commentPrefixes are the line-comment leaders recognised by the indentation
// mode header scanner.
var commentPrefixes = []string{"#", "//", "--"}

// lineRecord holds a single source line together with its 1-indexed line
// number and measured indentation (in spaces, tabs expanded).
type lineRecord struct {
	raw     string
	indent  int
	lineNum int // 1-indexed
}

// indentationOptions holds the parsed "indentation" object argument.
type indentationOptions struct {
	anchorLine      int  // 1-indexed; 0 means "use offset"
	maxLevels       int  // 0 = unlimited
	includeSiblings bool
	includeHeader   bool
	maxLines        int  // 0 = no cap (fall back to limit)
}

// ReadFileTool reads file contents with optional offset/limit.
//
// Maps to: codex-rs/core/src/tools/handlers/read_file.rs
type ReadFileTool struct{}

// NewReadFileTool creates a new read file tool handler.
func NewReadFileTool() *ReadFileTool {
	return &ReadFileTool{}
}

// Name returns the tool's name.
func (t *ReadFileTool) Name() string {
	return "read_file"
}

// Kind returns ToolKindFunction.
func (t *ReadFileTool) Kind() tools.ToolKind {
	return tools.ToolKindFunction
}

// IsMutating returns false - reading files doesn't modify the environment.
func (t *ReadFileTool) IsMutating(invocation *tools.ToolInvocation) bool {
	return false
}

// Handle reads a file and returns its contents with line numbers.
//
// Maps to: codex-rs/core/src/tools/handlers/read_file.rs handle
func (t *ReadFileTool) Handle(_ context.Context, invocation *tools.ToolInvocation) (*tools.ToolOutput, error) {
	// Accept both "file_path" (upstream name) and "path" (legacy).
	pathArg, ok := invocation.Arguments["file_path"]
	if !ok {
		pathArg, ok = invocation.Arguments["path"]
	}
	if !ok {
		return nil, tools.NewValidationError("missing required argument: file_path")
	}

	path, ok := pathArg.(string)
	if !ok {
		return nil, tools.NewValidationError("path must be a string")
	}

	if path == "" {
		return nil, tools.NewValidationError("path cannot be empty")
	}

	// Offset is 1-indexed (upstream convention). offset=1 means start from
	// the first line. We convert to 0-indexed internally for line skipping.
	offset := 0 // 0 means "not set" — read from beginning
	if offsetArg, ok := invocation.Arguments["offset"]; ok {
		switch v := offsetArg.(type) {
		case int:
			offset = v
		case float64:
			offset = int(v)
		default:
			return nil, tools.NewValidationError("offset must be an integer")
		}
	}

	limit := -1
	if limitArg, ok := invocation.Arguments["limit"]; ok {
		switch v := limitArg.(type) {
		case int:
			limit = v
		case float64:
			limit = int(v)
		default:
			return nil, tools.NewValidationError("limit must be an integer")
		}
	}

	// Parse mode argument (default: "slice").
	mode := "slice"
	if modeArg, ok := invocation.Arguments["mode"]; ok {
		if s, ok := modeArg.(string); ok {
			mode = s
		}
	}

	// Parse indentation options when mode is "indentation".
	var indentOpts indentationOptions
	if mode == "indentation" {
		if indentArg, ok := invocation.Arguments["indentation"]; ok {
			if m, ok := indentArg.(map[string]interface{}); ok {
				indentOpts = parseIndentationOptions(m)
			}
		}
	}

	file, err := os.Open(path)
	if err != nil {
		success := false
		return &tools.ToolOutput{
			Content: fmt.Sprintf("Failed to open file: %v", err),
			Success: &success,
		}, nil
	}
	defer file.Close()

	// Dispatch to the appropriate mode handler.
	if mode == "indentation" {
		return readFileIndentation(file, path, offset, limit, indentOpts)
	}

	return readFileSlice(file, path, offset, limit)
}

// readFileSlice implements the original slice-mode read (offset + limit).
func readFileSlice(file *os.File, path string, offset, limit int) (*tools.ToolOutput, error) {
	scanner := bufio.NewScanner(file)
	var result strings.Builder
	lineNum := 0
	linesRead := 0

	// Convert 1-indexed offset to number of lines to skip.
	// offset <= 0 or unset: start from beginning (skip 0).
	// offset >= 1: skip (offset-1) lines.
	skipLines := 0
	if offset > 1 {
		skipLines = offset - 1
	}

	for lineNum < skipLines && scanner.Scan() {
		lineNum++
	}

	for scanner.Scan() {
		if limit > 0 && linesRead >= limit {
			break
		}

		line := scanner.Text()
		if len(line) > 2000 {
			line = line[:2000] + "... (truncated)"
		}

		result.WriteString(fmt.Sprintf("%6d\t%s\n", lineNum+1, line))
		lineNum++
		linesRead++
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	content := result.String()
	if content == "" {
		if offset > 0 {
			content = fmt.Sprintf("(file has fewer than %d lines)", offset)
		} else {
			content = "(empty file)"
		}
	}

	// Add file path header so the LLM knows which file this content belongs to.
	content = fmt.Sprintf("File: %s\n%s", path, content)

	success := true
	return &tools.ToolOutput{
		Content: content,
		Success: &success,
	}, nil
}

// readFileIndentation implements the indentation-aware block mode.
//
// Algorithm (ported from codex-rs/core/src/tools/handlers/read_file.rs):
//  1. Read all lines into lineRecord structs with raw text, indent, lineNum
//  2. Compute effective indents (blank lines inherit previous non-blank indent)
//  3. Determine anchor line (from indentation.anchor_line or offset, default 1)
//  4. Calculate min_indent = anchor_indent - (max_levels * tabWidth); if max_levels=0, min_indent=0
//  5. Bidirectional expansion from anchor
//  6. Trim leading/trailing blank lines
//  7. Cap to max_lines (or limit)
//  8. Format with line numbers
func readFileIndentation(file *os.File, path string, offset, limit int, opts indentationOptions) (*tools.ToolOutput, error) {
	// Step 1: Read all lines.
	records, err := readAllLines(file)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	if len(records) == 0 {
		content := fmt.Sprintf("File: %s\n(empty file)", path)
		success := true
		return &tools.ToolOutput{Content: content, Success: &success}, nil
	}

	// Step 2: Compute effective indents.
	effectiveIndents := computeEffectiveIndents(records)

	// Step 3: Determine anchor line (1-indexed).
	anchorLine := opts.anchorLine
	if anchorLine <= 0 {
		anchorLine = offset
	}
	if anchorLine <= 0 {
		anchorLine = 1
	}
	// Clamp to file bounds.
	if anchorLine > len(records) {
		anchorLine = len(records)
	}
	anchorIdx := anchorLine - 1 // 0-indexed

	// Step 4: Calculate minimum indent threshold.
	anchorIndent := effectiveIndents[anchorIdx]
	minIndent := 0
	if opts.maxLevels > 0 {
		minIndent = anchorIndent - (opts.maxLevels * tabWidth)
		if minIndent < 0 {
			minIndent = 0
		}
	}

	// Step 5: Bidirectional expansion from anchor.
	included := make([]bool, len(records))
	included[anchorIdx] = true

	// Expand upward.
	expandUp(records, effectiveIndents, included, anchorIdx, minIndent, opts.includeSiblings, opts.includeHeader)

	// Expand downward.
	expandDown(records, effectiveIndents, included, anchorIdx, minIndent, opts.includeSiblings)

	// Step 6: Collect included lines and trim leading/trailing blank lines.
	var selected []lineRecord
	for i, rec := range records {
		if included[i] {
			selected = append(selected, rec)
		}
	}
	selected = trimBlankLines(selected)

	// Step 7: Cap to max_lines.
	cap := opts.maxLines
	if cap <= 0 {
		cap = limit
	}
	if cap > 0 && len(selected) > cap {
		// Center the cap around the anchor line.
		anchorPos := -1
		for i, rec := range selected {
			if rec.lineNum == anchorLine {
				anchorPos = i
				break
			}
		}
		if anchorPos < 0 {
			anchorPos = len(selected) / 2
		}
		half := cap / 2
		start := anchorPos - half
		if start < 0 {
			start = 0
		}
		end := start + cap
		if end > len(selected) {
			end = len(selected)
			start = end - cap
			if start < 0 {
				start = 0
			}
		}
		selected = selected[start:end]
	}

	// Step 8: Format output.
	var result strings.Builder
	for _, rec := range selected {
		line := rec.raw
		if len(line) > 2000 {
			line = line[:2000] + "... (truncated)"
		}
		result.WriteString(fmt.Sprintf("%6d\t%s\n", rec.lineNum, line))
	}

	content := result.String()
	if content == "" {
		content = "(no matching lines)"
	}
	content = fmt.Sprintf("File: %s\n%s", path, content)

	success := true
	return &tools.ToolOutput{Content: content, Success: &success}, nil
}

// expandUp walks upward from the anchor index, including lines with
// effective indent >= minIndent. When includeSiblings is false, it allows
// at most one sibling block at the minIndent level (plus optional comment
// header above it).
func expandUp(records []lineRecord, effectiveIndents []int, included []bool, anchorIdx, minIndent int, includeSiblings, includeHeader bool) {
	hitSiblingAtMin := false
	for i := anchorIdx - 1; i >= 0; i-- {
		eff := effectiveIndents[i]
		if eff < minIndent {
			break
		}
		if !includeSiblings && eff == minIndent {
			if hitSiblingAtMin {
				// We already saw one sibling at min indent; this is a second.
				// If includeHeader, continue scanning for comments only.
				if includeHeader {
					// Include comment lines immediately above.
					for j := i; j >= 0; j-- {
						if isComment(records[j].raw) {
							included[j] = true
						} else {
							break
						}
					}
				}
				break
			}
			hitSiblingAtMin = true
		}
		included[i] = true
	}
}

// expandDown walks downward from the anchor index, including lines with
// effective indent >= minIndent. When includeSiblings is false, it allows
// at most one sibling block at the minIndent level.
func expandDown(records []lineRecord, effectiveIndents []int, included []bool, anchorIdx, minIndent int, includeSiblings bool) {
	hitSiblingAtMin := false
	for i := anchorIdx + 1; i < len(records); i++ {
		eff := effectiveIndents[i]
		if eff < minIndent {
			break
		}
		if !includeSiblings && eff == minIndent {
			if hitSiblingAtMin {
				break
			}
			hitSiblingAtMin = true
		}
		included[i] = true
	}
}

// readAllLines reads all lines from the file into lineRecord structs.
func readAllLines(file *os.File) ([]lineRecord, error) {
	scanner := bufio.NewScanner(file)
	var records []lineRecord
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		raw := scanner.Text()
		records = append(records, lineRecord{
			raw:     raw,
			indent:  measureIndent(raw),
			lineNum: lineNum,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

// measureIndent counts leading whitespace in a line, treating tabs as
// tabWidth spaces each.
//
// Maps to: codex-rs/core/src/tools/handlers/read_file.rs measure_indent
func measureIndent(line string) int {
	indent := 0
	for _, ch := range line {
		switch ch {
		case ' ':
			indent++
		case '\t':
			indent += tabWidth
		default:
			return indent
		}
	}
	return indent
}

// computeEffectiveIndents returns an indent value for every line, where blank
// lines inherit the indent of the previous non-blank line.
//
// Maps to: codex-rs/core/src/tools/handlers/read_file.rs compute_effective_indents
func computeEffectiveIndents(records []lineRecord) []int {
	eff := make([]int, len(records))
	lastIndent := 0
	for i, rec := range records {
		if strings.TrimSpace(rec.raw) == "" {
			eff[i] = lastIndent
		} else {
			eff[i] = rec.indent
			lastIndent = rec.indent
		}
	}
	return eff
}

// isComment returns true if the line (after stripping leading whitespace)
// starts with a recognized comment prefix.
//
// Maps to: codex-rs/core/src/tools/handlers/read_file.rs is_comment
func isComment(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	for _, prefix := range commentPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// trimBlankLines removes leading and trailing blank lines from a slice of
// lineRecords.
func trimBlankLines(records []lineRecord) []lineRecord {
	start := 0
	for start < len(records) && strings.TrimSpace(records[start].raw) == "" {
		start++
	}
	end := len(records)
	for end > start && strings.TrimSpace(records[end-1].raw) == "" {
		end--
	}
	return records[start:end]
}

// parseIndentationOptions extracts indentation options from the raw argument map.
func parseIndentationOptions(m map[string]interface{}) indentationOptions {
	opts := indentationOptions{}
	if v, ok := m["anchor_line"]; ok {
		opts.anchorLine = toInt(v)
	}
	if v, ok := m["max_levels"]; ok {
		opts.maxLevels = toInt(v)
	}
	if v, ok := m["include_siblings"]; ok {
		if b, ok := v.(bool); ok {
			opts.includeSiblings = b
		}
	}
	if v, ok := m["include_header"]; ok {
		if b, ok := v.(bool); ok {
			opts.includeHeader = b
		}
	}
	if v, ok := m["max_lines"]; ok {
		opts.maxLines = toInt(v)
	}
	return opts
}

// toInt converts a numeric interface value to int.
func toInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case int64:
		return int(n)
	default:
		return 0
	}
}
