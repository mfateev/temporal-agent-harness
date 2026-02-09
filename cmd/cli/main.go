// Interactive CLI for codex-temporal-go workflows.
//
// A REPL-style interface that connects to a Temporal workflow,
// shows conversation items as they appear, and lets you type
// follow-up messages.
//
// Usage:
//
//	cli -m "hello"                    Start new session with initial message
//	cli                               Start new session, enter input immediately
//	cli --session <id>               Resume existing session
//	cli -m "hello" --model gpt-4o    Use a specific model
package main

import (
	"flag"
	"fmt"
	"os"

	"go.temporal.io/sdk/client"

	"github.com/mfateev/codex-temporal-go/internal/cli"
)

func main() {
	message := flag.String("m", "", "Initial message (starts new workflow)")
	message2 := flag.String("message", "", "Initial message (alias for -m)")
	session := flag.String("session", "", "Resume existing session")
	workflowID := flag.String("workflow-id", "", "Resume existing session (alias for --session)")
	model := flag.String("model", "gpt-4o-mini", "LLM model to use")
	temporalHost := flag.String("temporal-host", client.DefaultHostPort, "Temporal server address")
	noMarkdown := flag.Bool("no-markdown", false, "Disable markdown rendering")
	noColor := flag.Bool("no-color", false, "Disable colored output")
	enableShell := flag.Bool("enable-shell", true, "Enable shell tool")
	enableRead := flag.Bool("enable-read-file", true, "Enable read_file tool")
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

	config := cli.Config{
		TemporalHost: *temporalHost,
		Session:      sess,
		Message:      msg,
		Model:        *model,
		NoMarkdown:   *noMarkdown,
		NoColor:      *noColor,
		EnableShell:  *enableShell,
		EnableRead:   *enableRead,
	}

	app := cli.NewApp(config)
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
