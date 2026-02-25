package cli

import (
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

// buildReviewMessage constructs a code review prompt from a git diff.
// Returns the prompt string to send as a user message, or empty if no diff.
func buildReviewMessage(diff string) string {
	if diff == "" || diff == "No changes detected." || diff == "Not in a git repository." {
		return ""
	}
	return "Review the following code changes for bugs, security issues, and improvements:\n\n" + diff
}

// runReviewDiffCmd returns a tea.Cmd that runs git diff and returns a ReviewResultMsg.
func runReviewDiffCmd(cwd string) tea.Cmd {
	return func() tea.Msg {
		abs, err := filepath.Abs(cwd)
		if err != nil {
			abs = cwd
		}
		return ReviewResultMsg{Output: runGitDiff(abs)}
	}
}
