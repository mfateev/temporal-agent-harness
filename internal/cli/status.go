package cli

import (
	"fmt"
	"strings"
)

// formatStatusDisplay returns a human-readable summary of the current session
// configuration and usage. All data comes from fields already on the Model.
func (m *Model) formatStatusDisplay() string {
	var b strings.Builder

	b.WriteString("Session Status\n")
	b.WriteString("──────────────\n")

	b.WriteString(fmt.Sprintf("  Model:           %s\n", m.modelName))
	b.WriteString(fmt.Sprintf("  Provider:        %s\n", m.provider))
	if m.reasoningEffort != "" {
		b.WriteString(fmt.Sprintf("  Reasoning:       %s\n", m.reasoningEffort))
	}
	b.WriteString(fmt.Sprintf("  Approval mode:   %s\n", m.config.Permissions.ApprovalMode))
	b.WriteString(fmt.Sprintf("  Sandbox:         %s\n", m.config.Permissions.SandboxMode))
	b.WriteString(fmt.Sprintf("  Working dir:     %s\n", m.config.Cwd))

	if m.sessionName != "" {
		b.WriteString(fmt.Sprintf("  Session name:    %s\n", m.sessionName))
	}
	if m.workflowID != "" {
		b.WriteString(fmt.Sprintf("  Workflow ID:     %s\n", m.workflowID))
	}

	b.WriteString(fmt.Sprintf("  Tokens:          %d", m.totalTokens))
	if m.totalCachedTokens > 0 {
		b.WriteString(fmt.Sprintf(" (%d cached)", m.totalCachedTokens))
	}
	b.WriteString("\n")

	if m.contextWindowPct > 0 {
		b.WriteString(fmt.Sprintf("  Context window:  %d%% remaining\n", m.contextWindowPct))
	}

	b.WriteString(fmt.Sprintf("  Turn count:      %d\n", m.turnCount))

	if m.workerVersion != "" {
		b.WriteString(fmt.Sprintf("  Worker version:  %s\n", m.workerVersion))
	}

	if m.plannerActive {
		b.WriteString("  Plan mode:       active\n")
	}

	return b.String()
}
