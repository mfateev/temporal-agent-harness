package handlers

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mfateev/codex-temporal-go/internal/tools"
)

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
	pathArg, ok := invocation.Arguments["path"]
	if !ok {
		return nil, tools.NewValidationError("missing required argument: path")
	}

	path, ok := pathArg.(string)
	if !ok {
		return nil, tools.NewValidationError("path must be a string")
	}

	if path == "" {
		return nil, tools.NewValidationError("path cannot be empty")
	}

	offset := 0
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

	file, err := os.Open(path)
	if err != nil {
		success := false
		return &tools.ToolOutput{
			Content: fmt.Sprintf("Failed to open file: %v", err),
			Success: &success,
		}, nil
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var result strings.Builder
	lineNum := 0
	linesRead := 0

	for lineNum < offset && scanner.Scan() {
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
	// This prevents smaller models from losing track during multi-tool turns.
	content = fmt.Sprintf("File: %s\n%s", path, content)

	success := true
	return &tools.ToolOutput{
		Content: content,
		Success: &success,
	}, nil
}
