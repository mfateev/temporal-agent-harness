package tools

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// ReadFileTool reads file contents with optional offset/limit
//
// Maps to: codex-rs/core/src/tools/handlers/read_file.rs
type ReadFileTool struct{}

// NewReadFileTool creates a new read file tool
func NewReadFileTool() *ReadFileTool {
	return &ReadFileTool{}
}

// Name returns the tool's name
func (t *ReadFileTool) Name() string {
	return "read_file"
}

// Execute reads a file and returns its contents with line numbers
func (t *ReadFileTool) Execute(args map[string]interface{}) (string, error) {
	// Extract path argument (required)
	pathArg, ok := args["path"]
	if !ok {
		return "", fmt.Errorf("missing required argument: path")
	}

	path, ok := pathArg.(string)
	if !ok {
		return "", fmt.Errorf("path must be a string")
	}

	if path == "" {
		return "", fmt.Errorf("path cannot be empty")
	}

	// Extract optional offset (default: 0)
	offset := 0
	if offsetArg, ok := args["offset"]; ok {
		switch v := offsetArg.(type) {
		case int:
			offset = v
		case float64:
			offset = int(v)
		default:
			return "", fmt.Errorf("offset must be an integer")
		}
	}

	// Extract optional limit (default: unlimited)
	limit := -1 // -1 means no limit
	if limitArg, ok := args["limit"]; ok {
		switch v := limitArg.(type) {
		case int:
			limit = v
		case float64:
			limit = int(v)
		default:
			return "", fmt.Errorf("limit must be an integer")
		}
	}

	// Open file
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Read file line by line
	scanner := bufio.NewScanner(file)
	var result strings.Builder
	lineNum := 0
	linesRead := 0

	// Skip lines before offset
	for lineNum < offset && scanner.Scan() {
		lineNum++
	}

	// Read lines within limit
	for scanner.Scan() {
		if limit > 0 && linesRead >= limit {
			break
		}

		line := scanner.Text()

		// Truncate long lines (max 2000 chars per line)
		if len(line) > 2000 {
			line = line[:2000] + "... (truncated)"
		}

		// Format with line number (1-indexed for display)
		result.WriteString(fmt.Sprintf("%6d\t%s\n", lineNum+1, line))

		lineNum++
		linesRead++
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading file: %w", err)
	}

	if result.Len() == 0 {
		if offset > 0 {
			return fmt.Sprintf("(file has fewer than %d lines)", offset), nil
		}
		return "(empty file)", nil
	}

	return result.String(), nil
}
