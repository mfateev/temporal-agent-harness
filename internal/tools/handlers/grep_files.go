package handlers

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mfateev/codex-temporal-go/internal/tools"
)

// Default and maximum limits matching Codex Rust.
const (
	grepDefaultLimit = 100
	grepMaxLimit     = 2000
)

// GrepFilesTool searches files using ripgrep and returns matching file paths.
//
// Maps to: codex-rs/core/src/tools/handlers/grep_files.rs GrepFilesHandler
type GrepFilesTool struct{}

// NewGrepFilesTool creates a new grep_files tool handler.
func NewGrepFilesTool() *GrepFilesTool {
	return &GrepFilesTool{}
}

// Name returns the tool's name.
func (t *GrepFilesTool) Name() string {
	return "grep_files"
}

// Kind returns ToolKindFunction.
func (t *GrepFilesTool) Kind() tools.ToolKind {
	return tools.ToolKindFunction
}

// IsMutating returns false - searching files doesn't modify the environment.
func (t *GrepFilesTool) IsMutating(invocation *tools.ToolInvocation) bool {
	return false
}

// Handle searches files using ripgrep and returns matching paths.
//
// Maps to: codex-rs/core/src/tools/handlers/grep_files.rs GrepFilesHandler::handle
func (t *GrepFilesTool) Handle(ctx context.Context, invocation *tools.ToolInvocation) (*tools.ToolOutput, error) {
	patternArg, ok := invocation.Arguments["pattern"]
	if !ok {
		return nil, tools.NewValidationError("missing required argument: pattern")
	}

	pattern, ok := patternArg.(string)
	if !ok {
		return nil, tools.NewValidationError("pattern must be a string")
	}

	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, tools.NewValidationError("pattern must not be empty")
	}

	limit := grepDefaultLimit
	if limitArg, ok := invocation.Arguments["limit"]; ok {
		switch v := limitArg.(type) {
		case float64:
			limit = int(v)
		case int:
			limit = v
		default:
			return nil, tools.NewValidationError("limit must be a number")
		}
	}
	if limit < 1 {
		return nil, tools.NewValidationError("limit must be greater than zero")
	}
	if limit > grepMaxLimit {
		limit = grepMaxLimit
	}

	// Resolve search path: use provided path, invocation Cwd, or process cwd.
	searchPath := ""
	if pathArg, ok := invocation.Arguments["path"]; ok {
		if p, ok := pathArg.(string); ok && strings.TrimSpace(p) != "" {
			searchPath = strings.TrimSpace(p)
		}
	}
	if searchPath == "" && invocation.Cwd != "" {
		searchPath = invocation.Cwd
	}
	if searchPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			success := false
			return &tools.ToolOutput{
				Content: fmt.Sprintf("failed to determine working directory: %v", err),
				Success: &success,
			}, nil
		}
		searchPath = cwd
	}

	// Verify the search path exists.
	if _, err := os.Stat(searchPath); err != nil {
		success := false
		return &tools.ToolOutput{
			Content: fmt.Sprintf("unable to access `%s`: %v", searchPath, err),
			Success: &success,
		}, nil
	}

	// Resolve optional include glob.
	var include string
	if includeArg, ok := invocation.Arguments["include"]; ok {
		if s, ok := includeArg.(string); ok {
			include = strings.TrimSpace(s)
		}
	}

	results, err := runRgSearch(ctx, pattern, include, searchPath, limit)
	if err != nil {
		success := false
		return &tools.ToolOutput{
			Content: err.Error(),
			Success: &success,
		}, nil
	}

	if len(results) == 0 {
		success := false
		return &tools.ToolOutput{
			Content: "No matches found.",
			Success: &success,
		}, nil
	}

	success := true
	return &tools.ToolOutput{
		Content: strings.Join(results, "\n"),
		Success: &success,
	}, nil
}

// runRgSearch executes ripgrep and returns matching file paths.
//
// Maps to: codex-rs/core/src/tools/handlers/grep_files.rs run_rg_search
func runRgSearch(ctx context.Context, pattern, include, searchPath string, limit int) ([]string, error) {
	args := []string{
		"--files-with-matches",
		"--sortr=modified",
		"--regexp", pattern,
		"--no-messages",
	}

	if include != "" {
		args = append(args, "--glob", include)
	}

	args = append(args, "--", searchPath)

	cmd := exec.CommandContext(ctx, "rg", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// rg exit codes: 0 = matches found, 1 = no matches, 2+ = error.
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			if code == 1 {
				// No matches — not an error.
				return nil, nil
			}
			return nil, fmt.Errorf("rg failed: %s", strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("failed to launch rg: %v. Ensure ripgrep is installed and on PATH.", err)
	}

	return parseResults(stdout.Bytes(), limit), nil
}

// parseResults splits rg stdout into file paths, capped at limit.
//
// Maps to: codex-rs/core/src/tools/handlers/grep_files.rs parse_results
func parseResults(stdout []byte, limit int) []string {
	var results []string
	for _, line := range bytes.Split(stdout, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		text := string(line)
		if text == "" {
			continue
		}
		results = append(results, text)
		if len(results) == limit {
			break
		}
	}
	return results
}
