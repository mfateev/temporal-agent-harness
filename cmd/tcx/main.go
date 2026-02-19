// tcx is the interactive CLI for temporal-agent-harness workflows.
//
// A TUI-based interface that connects to a Temporal workflow,
// shows conversation items in a scrollable viewport, and lets you type
// follow-up messages.
//
// Usage:
//
//	tcx                               Show session picker (resume or new)
//	tcx -m "hello"                    Start new session with initial message
//	tcx -m "hello" --model gpt-4o    Use a specific model
//	tcx --inline                     Run without alt-screen (inline mode)
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mfateev/temporal-agent-harness/internal/cli"
	"github.com/mfateev/temporal-agent-harness/internal/models"
)

func main() {
	message := flag.String("m", "", "Initial message (starts new workflow, skips session picker)")
	message2 := flag.String("message", "", "Initial message (alias for -m)")
	model := flag.String("model", "gpt-4o-mini", "LLM model to use")
	provider := flag.String("provider", "", "LLM provider override (openai, anthropic, google)")
	temporalHost := flag.String("temporal-host", "", "Temporal server address (overrides envconfig/env vars)")
	noMarkdown := flag.Bool("no-markdown", false, "Disable markdown rendering")
	noColor := flag.Bool("no-color", false, "Disable colored output")
	inline := flag.Bool("inline", false, "Disable alt-screen mode (inline output)")
	fullAuto := flag.Bool("full-auto", false, "Auto-approve all tool calls without prompting")
	approvalMode := flag.String("approval-mode", "", "Approval mode: unless-trusted, never, on-failure")
	sandboxMode := flag.String("sandbox", "", "Sandbox mode: full-access, read-only, workspace-write")
	sandboxWritable := flag.String("sandbox-writable", "", "Comma-separated writable roots for workspace-write sandbox")
	sandboxNetwork := flag.Bool("sandbox-network", true, "Allow network access in sandbox")
	codexHome := flag.String("codex-home", "", "Path to codex config directory (default: ~/.codex)")
	noSuggestions := flag.Bool("no-suggestions", false, "Disable prompt suggestions after turn completion")
	flag.Parse()

	// Support both -m and --message
	msg := *message
	if msg == "" {
		msg = *message2
	}

	var resolvedApproval models.ApprovalMode
	switch {
	case *approvalMode != "":
		resolvedApproval = models.ApprovalMode(*approvalMode)
	case *fullAuto:
		resolvedApproval = models.ApprovalNever
	default:
		resolvedApproval = models.ApprovalUnlessTrusted
	}

	// Parse sandbox writable roots
	var writableRoots []string
	if *sandboxWritable != "" {
		for _, root := range strings.Split(*sandboxWritable, ",") {
			root = strings.TrimSpace(root)
			if root != "" {
				writableRoots = append(writableRoots, root)
			}
		}
	}

	// Smart provider detection from model name
	resolvedProvider := *provider
	if resolvedProvider == "" {
		resolvedProvider = cli.DetectProvider(*model)
	}

	config := cli.Config{
		TemporalHost:         *temporalHost,
		Message:              msg,
		Model:                *model,
		NoMarkdown:           *noMarkdown,
		NoColor:              *noColor,
		ApprovalMode:         resolvedApproval,
		SandboxMode:          *sandboxMode,
		SandboxWritableRoots: writableRoots,
		SandboxNetworkAccess: *sandboxNetwork,
		CodexHome:            *codexHome,
		Provider:             resolvedProvider,
		Inline:               *inline,
		DisableSuggestions:   *noSuggestions,
	}

	if err := cli.Run(config); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
