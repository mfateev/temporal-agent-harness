// Package cli implements the interactive REPL for temporal-agent-harness.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/glamour"
	gansi "github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"
	"github.com/mfateev/temporal-agent-harness/internal/models"
	"github.com/mfateev/temporal-agent-harness/internal/workflow"
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
			glamour.WithStyles(darkStyleCleanHeadings()),
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
		// No separator in viewport — the input area has its own separators.
		return ""
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
	case models.ItemTypeCompaction:
		return r.RenderCompaction(item)
	case models.ItemTypeTurnComplete:
		return ""
	default:
		return ""
	}
}

// RenderCompaction renders a compaction marker.
func (r *ItemRenderer) RenderCompaction(item models.ConversationItem) string {
	bullet := r.styles.SystemBullet.Render("●")
	return bullet + " [Context compacted]\n"
}

// RenderTurnSeparator renders a horizontal rule to visually separate turns.
func (r *ItemRenderer) RenderTurnSeparator() string {
	w := r.width
	if w <= 0 {
		w = 80
	}
	return r.styles.TurnSeparator.Render(strings.Repeat("─", w)) + "\n"
}

// RenderSystemMessage renders a system-level message with a yellow bullet.
func (r *ItemRenderer) RenderSystemMessage(text string) string {
	bullet := r.styles.SystemBullet.Render("●")
	return bullet + " " + text + "\n"
}

// RenderUserMessage renders a user message with a chevron prefix.
// Skips internal messages like environment context that aren't user-visible.
func (r *ItemRenderer) RenderUserMessage(item models.ConversationItem) string {
	// Hide internal context messages from display
	if strings.HasPrefix(item.Content, "<environment_context>") {
		return ""
	}
	chevron := r.styles.UserChevron.Render("❯")
	return chevron + " " + item.Content + "\n"
}

// RenderAssistantMessage renders an assistant message with optional markdown.
func (r *ItemRenderer) RenderAssistantMessage(item models.ConversationItem) string {
	content := item.Content
	if content == "" {
		return ""
	}
	bullet := r.styles.AssistantBullet.Render("●")
	if r.mdRenderer != nil {
		rendered, err := r.mdRenderer.Render(content)
		if err == nil {
			return "\n" + bullet + " " + strings.TrimLeft(rendered, " \n")
		}
	}
	return "\n" + bullet + " " + content + "\n"
}

// RenderFunctionCall renders a function call invocation.
// Example: "● Ran echo hello"
func (r *ItemRenderer) RenderFunctionCall(item models.ConversationItem) string {
	verb, detail := formatToolCall(item.Name, item.Arguments)
	bullet := r.styles.ToolBullet.Render("●")
	styledVerb := r.styles.ToolVerb.Render(verb)
	if detail != "" {
		return "\n" + bullet + " " + styledVerb + " " + detail + "\n"
	}
	return "\n" + bullet + " " + styledVerb + "\n"
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

// renderApprovalEntry writes a single tool entry (title + optional preview box + reason)
// into the provided builder.
func (r *ItemRenderer) renderApprovalEntry(b *strings.Builder, index int, info approvalInfo, reason string) {
	idx := r.styles.ApprovalIndex.Render(fmt.Sprintf("[%d]", index))
	title := r.styles.ApprovalTool.Render(info.Title)
	b.WriteString(fmt.Sprintf("  %s %s\n", idx, title))
	if len(info.Preview) > 0 {
		b.WriteString("      " + r.styles.OutputPrefix.Render("╭─") + "\n")
		for _, line := range info.Preview {
			styled := r.styleDiffLine(line)
			b.WriteString("      " + r.styles.OutputPrefix.Render("│") + " " + styled + "\n")
		}
		b.WriteString("      " + r.styles.OutputPrefix.Render("╰─") + "\n")
	}
	if reason != "" {
		reasonStr := r.styles.ApprovalReason.Render("Reason:") + " " + reason
		b.WriteString(fmt.Sprintf("      %s\n", reasonStr))
	}
}

// styleDiffLine applies DiffAdd/DiffRemove/OutputDim styling based on line prefix.
func (r *ItemRenderer) styleDiffLine(line string) string {
	if len(line) > 0 {
		switch line[0] {
		case '+':
			return r.styles.DiffAdd.Render(line)
		case '-':
			return r.styles.DiffRemove.Render(line)
		}
	}
	return r.styles.OutputDim.Render(line)
}

// RenderApprovalPrompt renders the approval prompt for pending tool calls.
func (r *ItemRenderer) RenderApprovalPrompt(approvals []workflow.PendingApproval) string {
	var b strings.Builder
	b.WriteString("\n")
	for i, ap := range approvals {
		info := formatApprovalInfo(ap.ToolName, ap.Arguments)
		r.renderApprovalEntry(&b, i+1, info, ap.Reason)
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
		info := formatApprovalInfo(esc.ToolName, esc.Arguments)
		r.renderApprovalEntry(&b, i+1, info, "")
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
		info := formatApprovalInfo(ap.ToolName, ap.Arguments)
		r.renderApprovalEntry(&b, i+1, info, ap.Reason)
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
		info := formatApprovalInfo(esc.ToolName, esc.Arguments)
		r.renderApprovalEntry(&b, i+1, info, "")
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

// RenderPlan renders the plan state as a block in the viewport.
// Returns empty string if plan is nil or has no steps.
func (r *ItemRenderer) RenderPlan(plan *workflow.PlanState) string {
	if plan == nil || len(plan.Steps) == 0 {
		return ""
	}

	var b strings.Builder
	bullet := r.styles.PlanBullet.Render("●")
	label := r.styles.ToolVerb.Render("Plan")
	if plan.Explanation != "" {
		b.WriteString("\n" + bullet + " " + label + ": " + plan.Explanation + "\n")
	} else {
		b.WriteString("\n" + bullet + " " + label + "\n")
	}

	for _, step := range plan.Steps {
		switch step.Status {
		case workflow.PlanStepCompleted:
			marker := r.styles.PlanCompleted.Render("✓")
			b.WriteString("  " + marker + " " + step.Step + "\n")
		case workflow.PlanStepInProgress:
			marker := r.styles.ToolBullet.Render("●")
			b.WriteString("  " + marker + " " + step.Step + "\n")
		default: // pending
			marker := r.styles.PlanPending.Render("○")
			b.WriteString("  " + marker + " " + step.Step + "\n")
		}
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
	case workflow.PhaseCompacting:
		return "Compacting context..."
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
//	apply_patch  → ("Update", "path/to/file") or ("Patched", "")
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
		if input, _ := args["input"].(string); input != "" {
			paths := patchFilePaths(input)
			if len(paths) == 1 {
				return "Update", paths[0]
			}
			if len(paths) > 1 {
				return "Update", paths[0] + fmt.Sprintf(" +%d files", len(paths)-1)
			}
		}
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
	case "update_plan":
		return "Updated", "plan"
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

// darkStyleCleanHeadings returns a copy of glamour's DarkStyleConfig with
// heading prefixes (##, ###, etc.) removed so headings render as styled text
// without raw markdown markers.
func darkStyleCleanHeadings() gansi.StyleConfig {
	s := glamourstyles.DarkStyleConfig
	// Remove document margin so ● bullets align with other items
	noMargin := uint(0)
	s.Document.Margin = &noMargin
	s.H2.Prefix = ""
	s.H3.Prefix = ""
	s.H4.Prefix = ""
	s.H5.Prefix = ""
	s.H6.Prefix = ""
	return s
}

func formatTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%d,%03d", n/1000, n%1000)
	}
	return fmt.Sprintf("%d", n)
}
