package handlers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mfateev/codex-temporal-go/internal/tools"
)

// Default parameter values matching Codex Rust.
const (
	listDirDefaultOffset = 1
	listDirDefaultLimit  = 25
	listDirDefaultDepth  = 2
	maxEntryLength       = 500
	indentSpaces         = 2
)

// ListDirTool lists directory entries with depth traversal and pagination.
//
// Maps to: codex-rs/core/src/tools/handlers/list_dir.rs ListDirHandler
type ListDirTool struct{}

// NewListDirTool creates a new list_dir tool handler.
func NewListDirTool() *ListDirTool {
	return &ListDirTool{}
}

// Name returns the tool's name.
func (t *ListDirTool) Name() string {
	return "list_dir"
}

// Kind returns ToolKindFunction.
func (t *ListDirTool) Kind() tools.ToolKind {
	return tools.ToolKindFunction
}

// IsMutating returns false - listing directories doesn't modify the environment.
func (t *ListDirTool) IsMutating(invocation *tools.ToolInvocation) bool {
	return false
}

// dirEntryKind classifies directory entry types.
//
// Maps to: codex-rs/core/src/tools/handlers/list_dir.rs DirEntryKind
type dirEntryKind int

const (
	dirEntryFile dirEntryKind = iota
	dirEntryDirectory
	dirEntrySymlink
	dirEntryOther
)

// dirEntry represents a collected directory entry for sorting and display.
//
// Maps to: codex-rs/core/src/tools/handlers/list_dir.rs DirEntry
type dirEntry struct {
	sortKey     string       // full relative path for global sorting
	displayName string       // filename only for display
	depth       int          // nesting level for indentation
	kind        dirEntryKind // file type
}

// Handle lists directory entries with optional depth, offset, and limit.
//
// Maps to: codex-rs/core/src/tools/handlers/list_dir.rs ListDirHandler::handle
func (t *ListDirTool) Handle(_ context.Context, invocation *tools.ToolInvocation) (*tools.ToolOutput, error) {
	dirPathArg, ok := invocation.Arguments["dir_path"]
	if !ok {
		return nil, tools.NewValidationError("missing required argument: dir_path")
	}

	dirPath, ok := dirPathArg.(string)
	if !ok {
		return nil, tools.NewValidationError("dir_path must be a string")
	}

	if dirPath == "" {
		return nil, tools.NewValidationError("dir_path cannot be empty")
	}

	if !filepath.IsAbs(dirPath) {
		return nil, tools.NewValidationError("dir_path must be an absolute path")
	}

	offset, err := intArgOrDefault(invocation.Arguments, "offset", listDirDefaultOffset)
	if err != nil {
		return nil, err
	}
	if offset < 1 {
		return nil, tools.NewValidationError("offset must be a 1-indexed entry number")
	}

	limit, err := intArgOrDefault(invocation.Arguments, "limit", listDirDefaultLimit)
	if err != nil {
		return nil, err
	}
	if limit < 1 {
		return nil, tools.NewValidationError("limit must be greater than zero")
	}

	depth, err := intArgOrDefault(invocation.Arguments, "depth", listDirDefaultDepth)
	if err != nil {
		return nil, err
	}
	if depth < 1 {
		return nil, tools.NewValidationError("depth must be greater than zero")
	}

	lines, listErr := listDirSlice(dirPath, offset, limit, depth)
	if listErr != nil {
		success := false
		return &tools.ToolOutput{
			Content: listErr.Error(),
			Success: &success,
		}, nil
	}

	// Prepend "Absolute path: ..." header matching Codex output.
	output := make([]string, 0, len(lines)+1)
	output = append(output, fmt.Sprintf("Absolute path: %s", dirPath))
	output = append(output, lines...)

	success := true
	return &tools.ToolOutput{
		Content: strings.Join(output, "\n"),
		Success: &success,
	}, nil
}

// listDirSlice collects, sorts, and paginates directory entries.
//
// Maps to: codex-rs/core/src/tools/handlers/list_dir.rs list_dir_slice
func listDirSlice(dirPath string, offset, limit, depth int) ([]string, error) {
	var entries []dirEntry
	if err := collectEntries(dirPath, "", depth, &entries); err != nil {
		return nil, err
	}

	if len(entries) == 0 {
		return nil, nil
	}

	// Global sort by full relative path.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].sortKey < entries[j].sortKey
	})

	startIndex := offset - 1 // convert 1-indexed to 0-indexed
	if startIndex >= len(entries) {
		return nil, fmt.Errorf("offset exceeds directory entry count")
	}

	remaining := len(entries) - startIndex
	cappedLimit := limit
	if cappedLimit > remaining {
		cappedLimit = remaining
	}
	endIndex := startIndex + cappedLimit

	selected := entries[startIndex:endIndex]
	formatted := make([]string, 0, len(selected)+1)
	for _, e := range selected {
		formatted = append(formatted, formatEntryLine(&e))
	}

	if endIndex < len(entries) {
		formatted = append(formatted, fmt.Sprintf("More than %d entries found", cappedLimit))
	}

	return formatted, nil
}

// collectEntries performs BFS traversal collecting entries up to the given depth.
//
// Maps to: codex-rs/core/src/tools/handlers/list_dir.rs collect_entries
func collectEntries(dirPath, relativePrefix string, depth int, entries *[]dirEntry) error {
	type queueItem struct {
		absPath  string
		prefix   string
		remaining int
	}

	queue := []queueItem{{dirPath, relativePrefix, depth}}

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		dirEntries, err := os.ReadDir(item.absPath)
		if err != nil {
			return fmt.Errorf("failed to read directory: %v", err)
		}

		// Collect and sort per-directory for consistent BFS ordering.
		type collected struct {
			absPath      string
			relativePath string
			kind         dirEntryKind
			entry        dirEntry
		}
		var batch []collected

		for _, de := range dirEntries {
			fileName := de.Name()
			var relativePath string
			if item.prefix == "" {
				relativePath = fileName
			} else {
				relativePath = item.prefix + "/" + fileName
			}

			displayName := truncateEntry(fileName)
			displayDepth := 0
			if item.prefix != "" {
				displayDepth = strings.Count(item.prefix, "/") + 1
			}
			sortKey := truncateEntry(relativePath)

			kind := classifyEntry(de)
			batch = append(batch, collected{
				absPath:      filepath.Join(item.absPath, fileName),
				relativePath: relativePath,
				kind:         kind,
				entry: dirEntry{
					sortKey:     sortKey,
					displayName: displayName,
					depth:       displayDepth,
					kind:        kind,
				},
			})
		}

		// Sort per-directory by sort key (matching Rust behavior).
		sort.Slice(batch, func(i, j int) bool {
			return batch[i].entry.sortKey < batch[j].entry.sortKey
		})

		for _, c := range batch {
			if c.kind == dirEntryDirectory && item.remaining > 1 {
				queue = append(queue, queueItem{c.absPath, c.relativePath, item.remaining - 1})
			}
			*entries = append(*entries, c.entry)
		}
	}

	return nil
}

// classifyEntry determines the DirEntryKind from an os.DirEntry.
func classifyEntry(de os.DirEntry) dirEntryKind {
	// Check symlink first (Type() returns ModeSymlink for symlinks).
	if de.Type()&os.ModeSymlink != 0 {
		return dirEntrySymlink
	}
	if de.IsDir() {
		return dirEntryDirectory
	}
	if de.Type().IsRegular() {
		return dirEntryFile
	}
	return dirEntryOther
}

// formatEntryLine formats a directory entry with indentation and type suffix.
//
// Maps to: codex-rs/core/src/tools/handlers/list_dir.rs format_entry_line
func formatEntryLine(e *dirEntry) string {
	indent := strings.Repeat(" ", e.depth*indentSpaces)
	name := e.displayName
	switch e.kind {
	case dirEntryDirectory:
		name += "/"
	case dirEntrySymlink:
		name += "@"
	case dirEntryOther:
		name += "?"
	}
	return indent + name
}

// truncateEntry truncates an entry name to maxEntryLength.
func truncateEntry(s string) string {
	if len(s) > maxEntryLength {
		return s[:maxEntryLength]
	}
	return s
}

// intArgOrDefault extracts an integer argument with a default value.
func intArgOrDefault(args map[string]interface{}, name string, defaultVal int) (int, error) {
	v, ok := args[name]
	if !ok {
		return defaultVal, nil
	}
	switch n := v.(type) {
	case float64:
		return int(n), nil
	case int:
		return n, nil
	default:
		return 0, tools.NewValidationErrorf("%s must be a number", name)
	}
}
