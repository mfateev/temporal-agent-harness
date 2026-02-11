package cli

import "github.com/charmbracelet/lipgloss"

// Styles holds all lipgloss styles for the TUI.
type Styles struct {
	// Turn separator
	TurnSeparator lipgloss.Style
	// User message
	UserMessage lipgloss.Style
	// Function call name
	FunctionCallName lipgloss.Style
	// Function call arguments
	FunctionCallArgs lipgloss.Style
	// Function output success
	OutputSuccess lipgloss.Style
	// Function output failure
	OutputFailure lipgloss.Style
	// Tool call bullet (• character)
	ToolBullet lipgloss.Style
	// Tool call verb (bold "Ran", "Read", etc.)
	ToolVerb lipgloss.Style
	// Dimmed output text
	OutputDim lipgloss.Style
	// Dimmed output prefix (└, │)
	OutputPrefix lipgloss.Style
	// Status line
	StatusLine lipgloss.Style
	// Approval index
	ApprovalIndex lipgloss.Style
	// Approval tool label
	ApprovalTool lipgloss.Style
	// Approval reason
	ApprovalReason lipgloss.Style
	// Escalation header
	EscalationHeader lipgloss.Style
	// Escalation output
	EscalationOutput lipgloss.Style
	// Separator line between viewport and input
	Separator lipgloss.Style
	// Status bar
	StatusBar lipgloss.Style
	// Spinner message
	SpinnerMessage lipgloss.Style
	// Selector chevron indicator
	SelectorChevron lipgloss.Style
	// Selector highlighted item
	SelectorSelected lipgloss.Style
	// Selector shortcut hint
	SelectorShortcut lipgloss.Style
}

// DefaultStyles returns styles with colors enabled.
func DefaultStyles() Styles {
	return Styles{
		TurnSeparator:    lipgloss.NewStyle().Faint(true),
		UserMessage:      lipgloss.NewStyle().Bold(true),
		FunctionCallName: lipgloss.NewStyle().Foreground(lipgloss.Color("3")), // yellow
		FunctionCallArgs: lipgloss.NewStyle(),
		OutputSuccess:    lipgloss.NewStyle().Foreground(lipgloss.Color("2")), // green
		OutputFailure:    lipgloss.NewStyle().Foreground(lipgloss.Color("1")), // red
		ToolBullet:       lipgloss.NewStyle().Foreground(lipgloss.Color("2")), // green
		ToolVerb:         lipgloss.NewStyle().Bold(true),
		OutputDim:        lipgloss.NewStyle().Faint(true),
		OutputPrefix:     lipgloss.NewStyle().Faint(true),
		StatusLine:       lipgloss.NewStyle().Faint(true),
		ApprovalIndex:    lipgloss.NewStyle().Foreground(lipgloss.Color("6")), // cyan
		ApprovalTool:     lipgloss.NewStyle().Foreground(lipgloss.Color("3")), // yellow
		ApprovalReason:   lipgloss.NewStyle().Faint(true),
		EscalationHeader: lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		EscalationOutput: lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		Separator:        lipgloss.NewStyle().Faint(true),
		StatusBar:        lipgloss.NewStyle().Faint(true),
		SpinnerMessage:   lipgloss.NewStyle().Faint(true),
		SelectorChevron:  lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true),
		SelectorSelected: lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true),
		SelectorShortcut: lipgloss.NewStyle().Faint(true),
	}
}

// NoColorStyles returns styles with no colors (plain text).
func NoColorStyles() Styles {
	return Styles{
		TurnSeparator:    lipgloss.NewStyle(),
		UserMessage:      lipgloss.NewStyle(),
		FunctionCallName: lipgloss.NewStyle(),
		FunctionCallArgs: lipgloss.NewStyle(),
		OutputSuccess:    lipgloss.NewStyle(),
		OutputFailure:    lipgloss.NewStyle(),
		ToolBullet:       lipgloss.NewStyle(),
		ToolVerb:         lipgloss.NewStyle(),
		OutputDim:        lipgloss.NewStyle(),
		OutputPrefix:     lipgloss.NewStyle(),
		StatusLine:       lipgloss.NewStyle(),
		ApprovalIndex:    lipgloss.NewStyle(),
		ApprovalTool:     lipgloss.NewStyle(),
		ApprovalReason:   lipgloss.NewStyle(),
		EscalationHeader: lipgloss.NewStyle(),
		EscalationOutput: lipgloss.NewStyle(),
		Separator:        lipgloss.NewStyle(),
		StatusBar:        lipgloss.NewStyle(),
		SpinnerMessage:   lipgloss.NewStyle(),
		SelectorChevron:  lipgloss.NewStyle(),
		SelectorSelected: lipgloss.NewStyle(),
		SelectorShortcut: lipgloss.NewStyle(),
	}
}
