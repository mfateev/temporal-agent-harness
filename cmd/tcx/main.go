// tcx is the interactive CLI for codex-temporal-go workflows.
//
// A REPL-style interface that connects to a Temporal workflow,
// shows conversation items as they appear, and lets you type
// follow-up messages.
//
// Usage:
//
//	tcx -m "hello"                    Start new session with initial message
//	tcx                               Start new session, enter input immediately
//	tcx --session <id>               Resume existing session
//	tcx -m "hello" --model gpt-4o    Use a specific model
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mfateev/codex-temporal-go/internal/cli"
	"github.com/mfateev/codex-temporal-go/internal/instructions"
	"github.com/mfateev/codex-temporal-go/internal/models"
)

func main() {
	message := flag.String("m", "", "Initial message (starts new workflow)")
	message2 := flag.String("message", "", "Initial message (alias for -m)")
	session := flag.String("session", "", "Resume existing session")
	workflowID := flag.String("workflow-id", "", "Resume existing session (alias for --session)")
	model := flag.String("model", "gpt-4o-mini", "LLM model to use")
	temporalHost := flag.String("temporal-host", "", "Temporal server address (overrides envconfig/env vars)")
	noMarkdown := flag.Bool("no-markdown", false, "Disable markdown rendering")
	noColor := flag.Bool("no-color", false, "Disable colored output")
	enableShell := flag.Bool("enable-shell", true, "Enable shell tool")
	enableRead := flag.Bool("enable-read-file", true, "Enable read_file tool")
	fullAuto := flag.Bool("full-auto", false, "Auto-approve all tool calls without prompting")
	approvalMode := flag.String("approval-mode", "", "Approval mode: unless-trusted, never, on-failure")
	sandboxMode := flag.String("sandbox", "", "Sandbox mode: full-access, read-only, workspace-write")
	sandboxWritable := flag.String("sandbox-writable", "", "Comma-separated writable roots for workspace-write sandbox")
	sandboxNetwork := flag.Bool("sandbox-network", true, "Allow network access in sandbox")
	codexHome := flag.String("codex-home", "", "Path to codex config directory (default: ~/.codex)")
	flag.Parse()

	// Support both -m and --message
	msg := *message
	if msg == "" {
		msg = *message2
	}

	// Support both --session and --workflow-id (backward compat)
	sess := *session
	if sess == "" {
		sess = *workflowID
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

	// Load CLI-side project docs (AGENTS.md from current project)
	cwd, _ := os.Getwd()
	var cliProjectDocs string
	if gitRoot, err := instructions.FindGitRoot(cwd); err == nil && gitRoot != "" {
		cliProjectDocs, _ = instructions.LoadProjectDocs(gitRoot, cwd)
	}

	// Load user personal instructions (~/.codex/instructions.md)
	var userPersonalInstructions string
	configDir := *codexHome
	if configDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			configDir = filepath.Join(home, ".codex")
		}
	}
	if configDir != "" {
		if data, err := os.ReadFile(filepath.Join(configDir, "instructions.md")); err == nil {
			userPersonalInstructions = string(data)
		}
	}

	config := cli.Config{
		TemporalHost:             *temporalHost,
		Session:                  sess,
		Message:                  msg,
		Model:                    *model,
		NoMarkdown:               *noMarkdown,
		NoColor:                  *noColor,
		EnableShell:              *enableShell,
		EnableRead:               *enableRead,
		ApprovalMode:             resolvedApproval,
		SandboxMode:              *sandboxMode,
		SandboxWritableRoots:     writableRoots,
		SandboxNetworkAccess:     *sandboxNetwork,
		CodexHome:                configDir,
		CLIProjectDocs:           cliProjectDocs,
		UserPersonalInstructions: userPersonalInstructions,
	}

	app := cli.NewApp(config)
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
