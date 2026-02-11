// Package cli implements the interactive REPL for codex-temporal-go.
package cli

import (
	"encoding/json"
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

// RenderFunctionCall renders a function call invocation in Codex style.
// Example: "• Ran echo hello"
func (r *ItemRenderer) RenderFunctionCall(item models.ConversationItem) string {
	verb, detail := formatToolCall(item.Name, item.Arguments)
	bullet := r.styles.ToolBullet.Render("•")
	styledVerb := r.styles.ToolVerb.Render(verb)
	if detail != "" {
		return bullet + " " + styledVerb + " " + detail + "\n"
	}
	return bullet + " " + styledVerb + "\n"
}

// RenderFunctionCallOutput renders function call output in Codex style.
// Uses 5-line limit with middle truncation and tree-style prefixes.
func (r *ItemRenderer) RenderFunctionCallOutput(item models.ConversationItem) string {
	if item.Output == nil {
		return ""
	}

	isFailure := item.Output.Success != nil && !*item.Output.Success
	content := strings.TrimRight(item.Output.Content, "\n")

	if content == "" {
		line := r.styles.OutputPrefix.Render("  └ ") + r.styles.OutputDim.Render("(no output)")
		return line + "\n"
	}

	lines := strings.Split(content, "\n")
	displayed, _ := truncateMiddle(lines, 5)

	var b strings.Builder
	for i, line := range displayed {
		var prefix string
		if i == 0 {
			prefix = r.styles.OutputPrefix.Render("  └ ")
		} else {
			prefix = r.styles.OutputPrefix.Render("    ")
		}
		if isFailure {
			b.WriteString(prefix + r.styles.OutputFailure.Render(line) + "\n")
		} else {
			b.WriteString(prefix + r.styles.OutputDim.Render(line) + "\n")
		}
	}

	return b.String()
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

// RenderUserInputQuestionPrompt renders the question prompt for a request_user_input call.
func (r *ItemRenderer) RenderUserInputQuestionPrompt(req *workflow.PendingUserInputRequest) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(r.styles.EscalationHeader.Render("The assistant has a question for you:") + "\n\n")

	for i, q := range req.Questions {
		if len(req.Questions) > 1 {
			b.WriteString(fmt.Sprintf("  Q%d. %s\n", i+1, q.Question))
		} else {
			b.WriteString(fmt.Sprintf("  %s\n", q.Question))
		}
		for j, opt := range q.Options {
			idx := r.styles.ApprovalIndex.Render(fmt.Sprintf("[%d]", j+1))
			label := opt.Label
			if opt.Description != "" {
				label += " - " + opt.Description
			}
			b.WriteString(fmt.Sprintf("    %s %s\n", idx, label))
		}
		b.WriteString("\n")
	}

	b.WriteString("Enter option number (or type your answer): ")
	return b.String()
}

// RenderApprovalContext renders the approval details for the viewport without
// the prompt line (selector handles the options). Used when selector is active.
func (r *ItemRenderer) RenderApprovalContext(approvals []workflow.PendingApproval) string {
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
	return b.String()
}

// RenderEscalationContext renders escalation details for the viewport without
// the prompt line (selector handles the options). Used when selector is active.
func (r *ItemRenderer) RenderEscalationContext(escalations []workflow.EscalationRequest) string {
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
	return b.String()
}

// RenderUserInputQuestionContext renders the question details for the viewport
// without the prompt line (selector handles the options).
func (r *ItemRenderer) RenderUserInputQuestionContext(req *workflow.PendingUserInputRequest) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(r.styles.EscalationHeader.Render("The assistant has a question for you:") + "\n\n")

	for i, q := range req.Questions {
		if len(req.Questions) > 1 {
			b.WriteString(fmt.Sprintf("  Q%d. %s\n", i+1, q.Question))
		} else {
			b.WriteString(fmt.Sprintf("  %s\n", q.Question))
		}
		b.WriteString("\n")
	}
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
	case workflow.PhaseUserInputPending:
		return "Waiting for your answer..."
	default:
		return "Working..."
	}
}

// formatToolCall parses the tool name and JSON arguments, returning a
// human-readable verb and detail string matching the Codex output style.
//
//	shell        → ("Ran", "echo hello")
//	read_file    → ("Read", "/tmp/foo.txt")
//	write_file   → ("Wrote", "/tmp/bar.txt")
//	apply_patch  → ("Patched", "")
//	list_dir     → ("Listed", "/tmp")
//	grep_files   → ("Searched", `"TODO" in src/`)
//	unknown      → ("Ran", "unknown_tool(…)")
func formatToolCall(name, argsJSON string) (verb, detail string) {
	var args map[string]interface{}
	_ = json.Unmarshal([]byte(argsJSON), &args)

	switch name {
	case "shell":
		if cmd, ok := args["command"].(string); ok {
			return "Ran", truncateString(cmd, 120)
		}
		return "Ran", truncateString(argsJSON, 120)
	case "read_file":
		if fp, ok := args["file_path"].(string); ok {
			return "Read", fp
		}
		return "Read", ""
	case "write_file":
		if fp, ok := args["file_path"].(string); ok {
			return "Wrote", fp
		}
		return "Wrote", ""
	case "apply_patch":
		return "Patched", ""
	case "list_dir":
		if dp, ok := args["dir_path"].(string); ok {
			return "Listed", dp
		}
		if dp, ok := args["path"].(string); ok {
			return "Listed", dp
		}
		return "Listed", ""
	case "grep_files":
		var parts []string
		if pat, ok := args["pattern"].(string); ok {
			parts = append(parts, fmt.Sprintf("%q", pat))
		}
		if dir, ok := args["path"].(string); ok {
			parts = append(parts, "in "+dir)
		}
		if len(parts) > 0 {
			return "Searched", strings.Join(parts, " ")
		}
		return "Searched", ""
	case "request_user_input":
		return "Asked", "user a question"
	default:
		detail := name + "(" + truncateString(argsJSON, 80) + ")"
		return "Ran", detail
	}
}

// truncateMiddle returns at most limit lines. When the input exceeds the limit,
// it keeps the first 2 and last 2 lines with a "… +N lines" placeholder in between.
// The returned omitted count reflects lines replaced by the placeholder.
func truncateMiddle(lines []string, limit int) (result []string, omitted int) {
	if len(lines) <= limit {
		return lines, 0
	}
	head := 2
	tail := 2
	omitted = len(lines) - head - tail
	result = make([]string, 0, head+1+tail)
	result = append(result, lines[:head]...)
	result = append(result, fmt.Sprintf("… +%d lines", omitted))
	result = append(result, lines[len(lines)-tail:]...)
	return result, omitted
}

// truncateString truncates s to maxLen characters, appending "…" if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
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
