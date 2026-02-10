// Package cli implements the interactive REPL for codex-temporal-go.
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/mfateev/codex-temporal-go/internal/models"
	"golang.org/x/term"
)

// ANSI color codes.
const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorFaint  = "\033[2m"
	colorYellow = "\033[33m"
	colorGreen  = "\033[32m"
	colorRed    = "\033[31m"
	colorCyan   = "\033[36m"
)

// Renderer writes conversation items to the terminal with ANSI formatting.
type Renderer struct {
	out        io.Writer
	noColor    bool
	noMarkdown bool
	mdRenderer *glamour.TermRenderer // nil if noMarkdown or init failed
}

// NewRenderer creates a renderer that writes to the given writer.
func NewRenderer(out io.Writer, noColor, noMarkdown bool) *Renderer {
	r := &Renderer{out: out, noColor: noColor, noMarkdown: noMarkdown}
	if !noMarkdown {
		width := 80
		if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
			width = w
		}
		md, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle("dark"),
			glamour.WithWordWrap(width),
		)
		if err == nil {
			r.mdRenderer = md
		}
	}
	return r
}

// RenderItem renders a single conversation item to the terminal.
// Returns true if the item was rendered (i.e., produced visible output).
func (r *Renderer) RenderItem(item models.ConversationItem) bool {
	switch item.Type {
	case models.ItemTypeTurnStarted:
		r.renderTurnStarted(item)
		return true
	case models.ItemTypeUserMessage:
		// User messages typed by the user are already echoed by readline.
		// Only render if resuming a session (we detect this by checking if
		// we're rendering history, but the caller handles that).
		return false
	case models.ItemTypeAssistantMessage:
		r.renderAssistantMessage(item)
		return true
	case models.ItemTypeFunctionCall:
		r.renderFunctionCall(item)
		return true
	case models.ItemTypeFunctionCallOutput:
		r.renderFunctionCallOutput(item)
		return true
	case models.ItemTypeTurnComplete:
		// Silent — triggers state transition in the main loop.
		return false
	default:
		return false
	}
}

// RenderItemForResume renders an item during session resume (shows user messages too).
func (r *Renderer) RenderItemForResume(item models.ConversationItem) {
	switch item.Type {
	case models.ItemTypeTurnStarted:
		r.renderTurnStarted(item)
	case models.ItemTypeUserMessage:
		r.renderUserMessage(item)
	case models.ItemTypeAssistantMessage:
		r.renderAssistantMessage(item)
	case models.ItemTypeFunctionCall:
		r.renderFunctionCall(item)
	case models.ItemTypeFunctionCallOutput:
		r.renderFunctionCallOutput(item)
	case models.ItemTypeTurnComplete:
		// Silent
	}
}

// RenderStatusLine prints a summary status after a turn completes.
func (r *Renderer) RenderStatusLine(model string, totalTokens, turnCount int) {
	line := fmt.Sprintf("[%s · %s tokens · turn %d]",
		model, formatTokens(totalTokens), turnCount)
	fmt.Fprintf(r.out, "%s%s%s\n", r.color(colorFaint), line, r.color(colorReset))
}

func (r *Renderer) renderTurnStarted(item models.ConversationItem) {
	fmt.Fprintf(r.out, "%s── Turn %s ──%s\n",
		r.color(colorFaint), item.TurnID, r.color(colorReset))
}

func (r *Renderer) renderUserMessage(item models.ConversationItem) {
	fmt.Fprintf(r.out, "%s> %s%s\n",
		r.color(colorBold), item.Content, r.color(colorReset))
}

func (r *Renderer) renderAssistantMessage(item models.ConversationItem) {
	content := item.Content
	if content == "" {
		return
	}
	if r.mdRenderer != nil {
		rendered, err := r.mdRenderer.Render(content)
		if err == nil {
			fmt.Fprint(r.out, rendered)
			return
		}
	}
	// Plain text fallback when markdown is disabled or rendering fails.
	fmt.Fprintf(r.out, "\n%s\n\n", content)
}

func (r *Renderer) renderFunctionCall(item models.ConversationItem) {
	args := item.Arguments
	if len(args) > 200 {
		args = args[:200] + "..."
	}
	fmt.Fprintf(r.out, "%s⚡ %s%s %s\n",
		r.color(colorYellow), item.Name, r.color(colorReset), args)
}

func (r *Renderer) renderFunctionCallOutput(item models.ConversationItem) {
	if item.Output == nil {
		return
	}

	content := item.Output.Content
	// Truncate long output
	lines := strings.Split(content, "\n")
	if len(lines) > 20 {
		content = strings.Join(lines[:20], "\n") + fmt.Sprintf("\n... (%d more lines)", len(lines)-20)
	}

	color := r.color(colorGreen)
	if item.Output.Success != nil && !*item.Output.Success {
		color = r.color(colorRed)
	}

	fmt.Fprintf(r.out, "%s  %s%s\n", color, indent(content, "  "), r.color(colorReset))
}

func (r *Renderer) color(code string) string {
	if r.noColor {
		return ""
	}
	return code
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
