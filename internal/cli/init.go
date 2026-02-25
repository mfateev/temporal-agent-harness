package cli

import (
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

// agentsMdTemplate returns the default AGENTS.md scaffold content.
func agentsMdTemplate() string {
	return `# Project Instructions

## Description
<!-- Brief description of your project -->

## Conventions
<!-- Coding style, naming conventions, patterns to follow -->

## Tool Preferences
<!-- Which tools to prefer/avoid, sandbox settings, etc. -->

## Important Files
<!-- Key files and directories the agent should know about -->
`
}

// runInitCmd scaffolds an AGENTS.md file in the given directory.
// Returns an InitResultMsg on success or InitErrorMsg on failure.
func runInitCmd(cwd string) tea.Cmd {
	return func() tea.Msg {
		abs, err := filepath.Abs(cwd)
		if err != nil {
			return InitErrorMsg{Err: err}
		}

		path := filepath.Join(abs, "AGENTS.md")

		// Check if file already exists
		if _, err := os.Stat(path); err == nil {
			return InitResultMsg{Path: path, AlreadyExists: true}
		}

		// Write the template
		if err := os.WriteFile(path, []byte(agentsMdTemplate()), 0644); err != nil {
			return InitErrorMsg{Err: err}
		}

		return InitResultMsg{Path: path, Created: true}
	}
}
