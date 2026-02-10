// Package cli implements the interactive REPL for codex-temporal-go.
package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/workflow"
	"golang.org/x/term"
)

// ItemRenderer renders conversation items as styled strings for the viewport.
type ItemRenderer struct {
	width      int
	noColor    bool
	noMarkdown bool
	styles     Styles
	mdRenderer *glamour.TermRenderer
}

// NewItemRenderer creates a renderer for conversation items.
func NewItemRenderer(width int, noColor, noMarkdown bool, styles Styles) *ItemRenderer {
	r := &ItemRenderer{
		width:      width,
		noColor:    noColor,
		noMarkdown: noMarkdown,
		styles:     styles,
	}
	if !noMarkdown {
		w := width
		if w <= 0 {
			w = 80
			if tw, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && tw > 0 {
				w = tw
			}
		}
		md, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle("dark"),
			glamour.WithWordWrap(w),
		)
		if err == nil {
			r.mdRenderer = md
		}
	}
	return r
}

// RenderItem renders a single conversation item as a string.
// isResume controls whether user messages are shown (they are during resume).
// Returns empty string if the item produces no visible output.
func (r *ItemRenderer) RenderItem(item models.ConversationItem, isResume bool) string {
	switch item.Type {
	case models.ItemTypeTurnStarted:
		return r.RenderTurnStarted(item)
	case models.ItemTypeUserMessage:
		if isResume {
			return r.RenderUserMessage(item)
		}
		return ""
	case models.ItemTypeAssistantMessage:
		return r.RenderAssistantMessage(item)
	case models.ItemTypeFunctionCall:
		return r.RenderFunctionCall(item)
	case models.ItemTypeFunctionCallOutput:
		return r.RenderFunctionCallOutput(item)
	case models.ItemTypeTurnComplete:
		return ""
	default:
		return ""
	}
}

// RenderTurnStarted renders a turn separator.
func (r *ItemRenderer) RenderTurnStarted(item models.ConversationItem) string {
	line := fmt.Sprintf("── Turn %s ──", item.TurnID)
	return r.styles.TurnSeparator.Render(line) + "\n"
}

// RenderUserMessage renders a user message.
func (r *ItemRenderer) RenderUserMessage(item models.ConversationItem) string {
	return r.styles.UserMessage.Render("> "+item.Content) + "\n"
}

// RenderAssistantMessage renders an assistant message with optional markdown.
func (r *ItemRenderer) RenderAssistantMessage(item models.ConversationItem) string {
	content := item.Content
	if content == "" {
		return ""
	}
	if r.mdRenderer != nil {
		rendered, err := r.mdRenderer.Render(content)
		if err == nil {
			return rendered
		}
	}
	return "\n" + content + "\n\n"
}

// RenderFunctionCall renders a function call invocation.
func (r *ItemRenderer) RenderFunctionCall(item models.ConversationItem) string {
	args := item.Arguments
	if len(args) > 200 {
		args = args[:200] + "..."
	}
	name := r.styles.FunctionCallName.Render("⚡ " + item.Name)
	return name + " " + args + "\n"
}

// RenderFunctionCallOutput renders function call output.
func (r *ItemRenderer) RenderFunctionCallOutput(item models.ConversationItem) string {
	if item.Output == nil {
		return ""
	}

	content := item.Output.Content
	lines := strings.Split(content, "\n")
	if len(lines) > 20 {
		content = strings.Join(lines[:20], "\n") + fmt.Sprintf("\n... (%d more lines)", len(lines)-20)
	}

	style := r.styles.OutputSuccess
	if item.Output.Success != nil && !*item.Output.Success {
		style = r.styles.OutputFailure
	}

	return style.Render("  "+indent(content, "  ")) + "\n"
}

// RenderApprovalPrompt renders the approval prompt for pending tool calls.
func (r *ItemRenderer) RenderApprovalPrompt(approvals []workflow.PendingApproval) string {
	var b strings.Builder
	b.WriteString("\n")
	for i, ap := range approvals {
		idx := r.styles.ApprovalIndex.Render(fmt.Sprintf("[%d]", i+1))
		tool := r.styles.ApprovalTool.Render("Tool:") + " " + ap.ToolName
		b.WriteString(fmt.Sprintf("  %s %s\n", idx, tool))
		b.WriteString(fmt.Sprintf("      %s\n", formatApprovalDetail(ap.ToolName, ap.Arguments)))
		if ap.Reason != "" {
			reason := r.styles.ApprovalReason.Render("Reason:") + " " + ap.Reason
			b.WriteString(fmt.Sprintf("      %s\n", reason))
		}
		b.WriteString("\n")
	}
	if len(approvals) > 1 {
		b.WriteString("Allow? [y]es / [n]o / [a]lways / 1,2 (select by index): ")
	} else {
		b.WriteString("Allow? [y]es / [n]o / [a]lways: ")
	}
	return b.String()
}

// RenderEscalationPrompt renders the escalation prompt for failed sandboxed calls.
func (r *ItemRenderer) RenderEscalationPrompt(escalations []workflow.EscalationRequest) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(r.styles.EscalationHeader.Render("Sandbox failure — escalation needed:") + "\n\n")
	for i, esc := range escalations {
		idx := r.styles.ApprovalIndex.Render(fmt.Sprintf("[%d]", i+1))
		tool := r.styles.ApprovalTool.Render("Tool:") + " " + esc.ToolName
		b.WriteString(fmt.Sprintf("  %s %s\n", idx, tool))
		b.WriteString(fmt.Sprintf("      %s\n", formatApprovalDetail(esc.ToolName, esc.Arguments)))
		if esc.Output != "" {
			outputPreview := esc.Output
			if len(outputPreview) > 200 {
				outputPreview = outputPreview[:200] + "..."
			}
			label := r.styles.EscalationOutput.Render("Output:")
			b.WriteString(fmt.Sprintf("      %s %s\n", label, outputPreview))
		}
		b.WriteString("\n")
	}
	b.WriteString("Re-run without sandbox? [y]es / [n]o: ")
	return b.String()
}

// RenderStatusLine renders a summary status after a turn completes.
func (r *ItemRenderer) RenderStatusLine(model string, totalTokens, turnCount int) string {
	line := fmt.Sprintf("[%s · %s tokens · turn %d]",
		model, formatTokens(totalTokens), turnCount)
	return r.styles.StatusLine.Render(line) + "\n"
}

// PhaseMessage returns a human-friendly message for a turn phase.
func PhaseMessage(phase workflow.TurnPhase, toolsInFlight []string) string {
	switch phase {
	case workflow.PhaseLLMCalling:
		return "Thinking..."
	case workflow.PhaseToolExecuting:
		if len(toolsInFlight) > 0 {
			return fmt.Sprintf("Running %s...", toolsInFlight[0])
		}
		return "Running tool..."
	case workflow.PhaseApprovalPending:
		return "Waiting for approval..."
	case workflow.PhaseEscalationPending:
		return "Waiting for escalation decision..."
	default:
		return "Working..."
	}
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= 1 {
		return s
	}
	for i := 1; i < len(lines); i++ {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func formatTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%d,%03d", n/1000, n%1000)
	}
	return fmt.Sprintf("%d", n)
}
