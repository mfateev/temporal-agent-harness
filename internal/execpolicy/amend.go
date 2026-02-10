package execpolicy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AppendAllowPrefixRule appends a prefix_rule with decision="allow" to the
// specified rules file. Creates the file and parent directories if needed.
//
// Maps to: codex-rs/execpolicy/src/lib.rs append_allow_prefix_rule
func AppendAllowPrefixRule(rulesFile string, prefix []string) error {
	if len(prefix) == 0 {
		return &RuleError{Message: "prefix must not be empty"}
	}

	// Build the Starlark prefix_rule line
	line := buildPrefixRuleLine(prefix)

	// Ensure parent directory exists
	dir := filepath.Dir(rulesFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create rules directory: %w", err)
	}

	// Read existing content to check for duplicates
	existing, err := os.ReadFile(rulesFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read rules file: %w", err)
	}

	// Check if this exact rule already exists
	if strings.Contains(string(existing), line) {
		return nil // Already present, skip
	}

	// Append the rule
	f, err := os.OpenFile(rulesFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open rules file: %w", err)
	}
	defer f.Close()

	// Add newline before if file is non-empty and doesn't end with newline
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		if _, err := f.WriteString("\n"); err != nil {
			return fmt.Errorf("failed to write newline: %w", err)
		}
	}

	if _, err := f.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("failed to write rule: %w", err)
	}

	return nil
}

// buildPrefixRuleLine builds a Starlark prefix_rule call string.
func buildPrefixRuleLine(prefix []string) string {
	parts := make([]string, len(prefix))
	for i, p := range prefix {
		parts[i] = fmt.Sprintf("%q", p)
	}
	return fmt.Sprintf("prefix_rule(pattern=[%s], decision=\"allow\")", strings.Join(parts, ", "))
}
