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
//	tcx crews                        List available crew templates
//	tcx start-crew <name> [--input key=value]...  Start a crew session
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mfateev/temporal-agent-harness/internal/cli"
	"github.com/mfateev/temporal-agent-harness/internal/models"
)

func main() {
	// Check for subcommands before flag parsing.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "crews":
			if err := runCrews(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "start-crew":
			if err := runStartCrew(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	message := flag.String("m", "", "Initial message (starts new workflow, skips session picker)")
	message2 := flag.String("message", "", "Initial message (alias for -m)")
	model := flag.String("model", "gpt-4o-mini", "LLM model to use")
	provider := flag.String("provider", "", "LLM provider override (openai, anthropic, google)")
	temporalHost := flag.String("temporal-host", "", "Temporal server address (overrides envconfig/env vars)")
	noMarkdown := flag.Bool("no-markdown", false, "Disable markdown rendering")
	noColor := flag.Bool("no-color", false, "Disable colored output")
	inline := flag.Bool("inline", false, "Disable alt-screen mode (inline output)")
	fullAuto := flag.Bool("full-auto", false, "Auto-approve all tool calls without prompting")
	approvalMode := flag.String("approval-mode", "", "Approval mode: unless-trusted, never, on-failure (deprecated)")
	sandboxMode := flag.String("sandbox", "", "Sandbox mode: full-access, read-only, workspace-write")
	sandboxWritable := flag.String("sandbox-writable", "", "Comma-separated writable roots for workspace-write sandbox")
	sandboxNetwork := flag.Bool("sandbox-network", true, "Allow network access in sandbox")
	codexHome := flag.String("codex-home", "", "Path to codex config directory (default: ~/.codex)")
	noSuggestions := flag.Bool("no-suggestions", false, "Disable prompt suggestions after turn completion")
	memory := flag.Bool("memory", false, "Enable cross-session memory subsystem")
	memoryDb := flag.String("memory-db", "", "Path to memory SQLite DB (default: ~/.codex/state.sqlite)")
	connTimeout := flag.Duration("connection-timeout", 0, "Per-RPC timeout for Temporal calls (e.g. 10s). 0 = no timeout. Env: TCX_CONNECTION_TIMEOUT")
	flag.Parse()

	// Support env var override for connection timeout (used by TUI tests)
	if *connTimeout == 0 {
		if envTimeout := os.Getenv("TCX_CONNECTION_TIMEOUT"); envTimeout != "" {
			if d, err := time.ParseDuration(envTimeout); err == nil {
				*connTimeout = d
			}
		}
	}

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
		TemporalHost: *temporalHost,
		Message:      msg,
		Model:        *model,
		NoMarkdown:   *noMarkdown,
		NoColor:      *noColor,
		Permissions: models.Permissions{
			ApprovalMode:         resolvedApproval,
			SandboxMode:          *sandboxMode,
			SandboxWritableRoots: writableRoots,
			SandboxNetworkAccess: *sandboxNetwork,
		},
		CodexHome:          *codexHome,
		Provider:           resolvedProvider,
		Inline:             *inline,
		DisableSuggestions: *noSuggestions,
		MemoryEnabled:      *memory,
		MemoryDbPath:       *memoryDb,
		ConnectionTimeout:  *connTimeout,
	}

	if err := cli.Run(config); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// resolveCodexHome returns the codex home directory.
func resolveCodexHome(override string) string {
	if override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".codex")
	}
	return filepath.Join(home, ".codex")
}

// runCrews lists available crew templates.
func runCrews() error {
	fs := flag.NewFlagSet("crews", flag.ExitOnError)
	codexHome := fs.String("codex-home", "", "Path to codex config directory (default: ~/.codex)")
	fs.Parse(os.Args[2:])

	crewDir := filepath.Join(resolveCodexHome(*codexHome), "crews")
	entries, err := os.ReadDir(crewDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No crews found. Create crew templates in ~/.codex/crews/*.toml")
			return nil
		}
		return fmt.Errorf("failed to read crews directory: %w", err)
	}

	var found bool
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(crewDir, entry.Name()))
		if err != nil {
			continue
		}

		crew, err := models.ParseCrewType(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: %s: %v\n", entry.Name(), err)
			continue
		}

		s := crew.Summary()
		if !found {
			fmt.Printf("%-20s %-14s %-40s %s\n", "NAME", "MODE", "DESCRIPTION", "INPUTS")
			found = true
		}
		inputs := "-"
		if len(s.Inputs) > 0 {
			inputs = strings.Join(s.Inputs, ", ")
		}
		fmt.Printf("%-20s %-14s %-40s %s\n", s.Name, s.Mode, truncate(s.Description, 40), inputs)
	}

	if !found {
		fmt.Println("No crews found. Create crew templates in ~/.codex/crews/*.toml")
	}

	return nil
}

// runStartCrew starts a crew session.
func runStartCrew() error {
	fs := flag.NewFlagSet("start-crew", flag.ExitOnError)
	codexHome := fs.String("codex-home", "", "Path to codex config directory (default: ~/.codex)")
	model := fs.String("model", "", "Override model (default: from crew definition)")
	provider := fs.String("provider", "", "LLM provider override")
	temporalHost := fs.String("temporal-host", "", "Temporal server address")
	inline := fs.Bool("inline", false, "Disable alt-screen mode")
	fullAuto := fs.Bool("full-auto", false, "Auto-approve all tool calls")
	noMarkdown := fs.Bool("no-markdown", false, "Disable markdown rendering")
	noColor := fs.Bool("no-color", false, "Disable colored output")
	connTimeout := fs.Duration("connection-timeout", 0, "Per-RPC timeout for Temporal calls")
	memory := fs.Bool("memory", false, "Enable cross-session memory subsystem")
	memoryDb := fs.String("memory-db", "", "Path to memory SQLite DB")

	// Custom parsing for --input flags (can appear multiple times).
	var inputFlags []string
	// Pre-scan args for --input flags before flag parsing.
	var filteredArgs []string
	args := os.Args[2:]
	for i := 0; i < len(args); i++ {
		if args[i] == "--input" || args[i] == "-input" {
			if i+1 < len(args) {
				inputFlags = append(inputFlags, args[i+1])
				i++ // skip the value
				continue
			}
		}
		if strings.HasPrefix(args[i], "--input=") {
			inputFlags = append(inputFlags, strings.TrimPrefix(args[i], "--input="))
			continue
		}
		if strings.HasPrefix(args[i], "-input=") {
			inputFlags = append(inputFlags, strings.TrimPrefix(args[i], "-input="))
			continue
		}
		filteredArgs = append(filteredArgs, args[i])
	}

	fs.Parse(filteredArgs)

	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: tcx start-crew <name> [--input key=value]...\n")
		os.Exit(1)
	}
	crewName := fs.Arg(0)

	// Load crew template
	crewPath := filepath.Join(resolveCodexHome(*codexHome), "crews", crewName+".toml")
	data, err := os.ReadFile(crewPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("crew %q not found at %s", crewName, crewPath)
		}
		return fmt.Errorf("failed to read crew: %w", err)
	}

	crew, err := models.ParseCrewType(data)
	if err != nil {
		return fmt.Errorf("failed to parse crew: %w", err)
	}

	// Parse --input flags into map
	inputs := make(map[string]string)
	for _, kv := range inputFlags {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid input format %q (expected key=value)", kv)
		}
		inputs[parts[0]] = parts[1]
	}

	// Apply crew to config
	cfg := models.DefaultSessionConfiguration()
	crewAgents, err := models.ApplyCrewType(crew, inputs, &cfg)
	if err != nil {
		return err
	}

	// Determine user message
	var msg string
	if crew.Mode == models.CrewModeAutonomous {
		msg = crew.InterpolatedInitialPrompt(inputs)
	} else {
		// Interactive mode — use remaining args as message, or empty for picker
		if fs.NArg() > 1 {
			msg = strings.Join(fs.Args()[1:], " ")
		}
	}

	// Resolve model/provider
	resolvedModel := cfg.Model.Model
	if *model != "" {
		resolvedModel = *model
	}
	resolvedProvider := *provider
	if resolvedProvider == "" {
		resolvedProvider = cli.DetectProvider(resolvedModel)
	}

	var resolvedApproval models.ApprovalMode
	if *fullAuto {
		resolvedApproval = models.ApprovalNever
	} else if cfg.Permissions.ApprovalMode != "" {
		resolvedApproval = cfg.Permissions.ApprovalMode
	} else {
		resolvedApproval = models.ApprovalUnlessTrusted
	}

	cliConfig := cli.Config{
		TemporalHost: *temporalHost,
		Message:      msg,
		Model:        resolvedModel,
		NoMarkdown:   *noMarkdown,
		NoColor:      *noColor,
		Permissions: models.Permissions{
			ApprovalMode: resolvedApproval,
		},
		CodexHome:         *codexHome,
		Provider:          resolvedProvider,
		Inline:            *inline,
		MemoryEnabled:     *memory,
		MemoryDbPath:      *memoryDb,
		ConnectionTimeout: *connTimeout,

		// Crew-specific fields
		CrewAgents:    crewAgents,
		CrewMainAgent: crew.MainAgent,
		CrewType:      crew.Name,
	}

	return cli.Run(cliConfig)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
