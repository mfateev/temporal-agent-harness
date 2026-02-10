package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chzyer/readline"
	"github.com/google/uuid"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/temporalclient"
	"github.com/mfateev/codex-temporal-go/internal/workflow"
)

const (
	TaskQueue    = "codex-temporal"
	PollInterval = 200 * time.Millisecond
)

// State represents the CLI state machine state.
type State int

const (
	StateStartup    State = iota
	StateInput
	StateWatching
	StateApproval   // Waiting for user to approve/deny tool calls
	StateEscalation // Waiting for user to approve/deny sandbox escalation
	StateShutdown
)

// Config holds CLI configuration.
type Config struct {
	TemporalHost string
	Session      string // Resume existing session (workflow ID)
	Message      string // Initial message for new workflow
	Model        string
	NoMarkdown   bool
	NoColor      bool
	EnableShell  bool
	EnableRead   bool
	Cwd          string
	ApprovalMode models.ApprovalMode

	// Sandbox settings
	SandboxMode          string   // "full-access", "read-only", "workspace-write"
	SandboxWritableRoots []string // Writable roots for workspace-write mode
	SandboxNetworkAccess bool     // Whether network is allowed

	// Codex config
	CodexHome string // Path to codex config directory (default: ~/.codex)

	// Instruction sources (populated by CLI main)
	CLIProjectDocs          string // AGENTS.md from CLI's local project
	UserPersonalInstructions string // From ~/.codex/instructions.md
}

// App is the interactive CLI application.
type App struct {
	config   Config
	client   client.Client
	renderer *Renderer
	spinner  *Spinner
	poller   *Poller

	workflowID      string
	state           State
	lastRenderedSeq int

	// Channels
	pollCh  chan PollResult
	inputCh chan string
	sigCh   chan os.Signal

	// Ctrl+C tracking
	lastInterruptTime time.Time
	interruptMu       sync.Mutex

	// Consecutive poll errors (reset on success)
	consecutiveErrors int

	// Approval state
	pendingApprovals   []workflow.PendingApproval
	autoApprove        bool // Set by "always" response; auto-approves future requests
	pendingEscalations []workflow.EscalationRequest

	// Readline instance
	rl *readline.Instance
}

// NewApp creates a new CLI app.
func NewApp(config Config) *App {
	return &App{
		config:          config,
		lastRenderedSeq: -1,
		pollCh:          make(chan PollResult, 1),
		inputCh:         make(chan string, 1),
		sigCh:           make(chan os.Signal, 1),
	}
}

// Run is the main entry point.
func (a *App) Run() error {
	// Connect to Temporal via envconfig (supports env vars, config files, TLS).
	// The --temporal-host flag overrides the envconfig host if set.
	clientOpts, err := temporalclient.LoadClientOptions(a.config.TemporalHost, "")
	if err != nil {
		return fmt.Errorf("failed to load Temporal client config: %w", err)
	}
	c, err := client.Dial(clientOpts)
	if err != nil {
		return fmt.Errorf("failed to connect to Temporal: %w", err)
	}
	defer c.Close()
	a.client = c

	// Set up renderer and spinner
	a.renderer = NewRenderer(os.Stdout, a.config.NoColor, a.config.NoMarkdown)
	a.spinner = NewSpinner(os.Stderr)

	// Set up readline
	a.rl, err = readline.NewEx(&readline.Config{
		Prompt:          "> ",
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		return fmt.Errorf("failed to init readline: %w", err)
	}
	defer a.rl.Close()

	// Set up signal handling
	signal.Notify(a.sigCh, syscall.SIGINT)
	defer signal.Stop(a.sigCh)

	// Startup: either resume or start new workflow
	if a.config.Session != "" {
		if err := a.resumeWorkflow(); err != nil {
			return err
		}
	} else {
		// If no initial message, prompt for one
		if a.config.Message == "" {
			fmt.Fprintf(os.Stderr, "codex-temporal (type /exit to disconnect, /end to terminate session)\n")
			line, err := a.rl.Readline()
			if err != nil {
				return nil // User cancelled
			}
			line = strings.TrimSpace(line)
			if line == "" || line == "/exit" || line == "/quit" {
				return nil
			}
			a.config.Message = line
		}

		if err := a.startWorkflow(); err != nil {
			return err
		}
	}

	// Main loop
	return a.mainLoop()
}

func (a *App) startWorkflow() error {
	a.workflowID = fmt.Sprintf("codex-%s", uuid.New().String()[:8])

	cwd := a.config.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	input := workflow.WorkflowInput{
		ConversationID: a.workflowID,
		UserMessage:    a.config.Message,
		Config: models.SessionConfiguration{
			Model: models.ModelConfig{
				Model:         a.config.Model,
				Temperature:   0.7,
				MaxTokens:     4096,
				ContextWindow: 128000,
			},
			Tools: models.ToolsConfig{
				EnableShell:    a.config.EnableShell,
				EnableReadFile: a.config.EnableRead,
			},
			ApprovalMode:             a.config.ApprovalMode,
			CodexHome:                a.config.CodexHome,
			SandboxMode:              a.config.SandboxMode,
			SandboxWritableRoots:     a.config.SandboxWritableRoots,
			SandboxNetworkAccess:     a.config.SandboxNetworkAccess,
			Cwd:                      cwd,
			SessionSource:            "interactive-cli",
			CLIProjectDocs:           a.config.CLIProjectDocs,
			UserPersonalInstructions: a.config.UserPersonalInstructions,
		},
	}

	ctx := context.Background()
	_, err := a.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        a.workflowID,
		TaskQueue: TaskQueue,
	}, "AgenticWorkflow", input)
	if err != nil {
		return fmt.Errorf("failed to start workflow: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Session: %s\n", a.workflowID)

	if a.config.Message != "" {
		// We sent the initial message, go to watching state
		a.state = StateWatching
	} else {
		a.state = StateInput
	}

	return nil
}

func (a *App) resumeWorkflow() error {
	a.workflowID = a.config.Session

	fmt.Fprintf(os.Stderr, "Resuming session: %s\n", a.workflowID)

	// Fetch and render existing history
	ctx := context.Background()
	poller := NewPoller(a.client, a.workflowID, PollInterval)
	result := poller.Poll(ctx)
	if result.Err != nil {
		return fmt.Errorf("failed to query workflow: %w", result.Err)
	}

	// Render history items
	if len(result.Items) > 0 {
		fmt.Fprintf(os.Stderr, "... %d previous items ...\n", len(result.Items))
		// Show last few items for context
		start := 0
		if len(result.Items) > 20 {
			start = len(result.Items) - 20
			fmt.Fprintf(os.Stderr, "... showing last %d items ...\n", len(result.Items)-start)
		}
		for _, item := range result.Items[start:] {
			a.renderer.RenderItemForResume(item)
		}
		a.lastRenderedSeq = result.Items[len(result.Items)-1].Seq
	}

	// Determine initial state based on turn status
	switch result.Status.Phase {
	case workflow.PhaseWaitingForInput:
		a.state = StateInput
	case workflow.PhaseApprovalPending:
		a.state = StateApproval
		a.renderApprovalPrompt(result.Status.PendingApprovals)
	case workflow.PhaseEscalationPending:
		a.state = StateEscalation
		a.renderEscalationPrompt(result.Status.PendingEscalations)
	default:
		a.state = StateWatching
	}

	return nil
}

func (a *App) mainLoop() error {
	// Set up poller
	a.poller = NewPoller(a.client, a.workflowID, PollInterval)

	var pollCancel context.CancelFunc
	var inputDone chan struct{}

	startPolling := func() {
		if pollCancel != nil {
			pollCancel()
		}
		var pollCtx context.Context
		pollCtx, pollCancel = context.WithCancel(context.Background())
		go a.poller.RunPolling(pollCtx, a.pollCh)
	}

	stopPolling := func() {
		if pollCancel != nil {
			pollCancel()
			pollCancel = nil
		}
	}

	startInput := func() {
		// Wait for any previous input goroutine to finish
		if inputDone != nil {
			<-inputDone
		}
		inputDone = make(chan struct{})
		go func() {
			defer close(inputDone)
			a.readInput()
		}()
	}

	// Start in the appropriate mode
	switch a.state {
	case StateWatching:
		startPolling()
		a.spinner.Start("Thinking...")
	case StateInput:
		startInput()
	case StateApproval:
		startInput()
	case StateEscalation:
		startInput()
	}

	defer stopPolling()

	for {
		select {
		case line := <-a.inputCh:
			// Handle approval input separately
			if a.state == StateApproval {
				response := a.handleApprovalInput(strings.TrimSpace(line))
				if response != nil {
					if err := a.sendApprovalResponse(*response); err != nil {
						fmt.Fprintf(os.Stderr, "Error sending approval: %v\n", err)
					}
					a.pendingApprovals = nil
					a.state = StateWatching
					a.spinner.Start("Running tools...")
					startPolling()
				} else {
					// Invalid input, re-prompt
					fmt.Fprintf(os.Stderr, "Please enter y(es), n(o), a(lways), or indices (e.g. 1,3): ")
					startInput()
				}
				continue
			}

			// Handle escalation input
			if a.state == StateEscalation {
				response := a.handleEscalationInput(strings.TrimSpace(line))
				if response != nil {
					if err := a.sendEscalationResponse(*response); err != nil {
						fmt.Fprintf(os.Stderr, "Error sending escalation response: %v\n", err)
					}
					a.pendingEscalations = nil
					a.state = StateWatching
					a.spinner.Start("Re-running tools...")
					startPolling()
				} else {
					fmt.Fprintf(os.Stderr, "Please enter y(es) or n(o): ")
					startInput()
				}
				continue
			}

			line = strings.TrimSpace(line)
			if line == "" {
				startInput()
				continue
			}

			// Handle special commands
			if line == "/exit" || line == "/quit" {
				// Disconnect — workflow stays alive
				a.printResumeHint()
				return nil
			}

			if line == "/end" {
				// Explicit shutdown — terminates the workflow
				a.spinner.Start("Ending session...")
				if err := a.sendShutdown(); err != nil {
					fmt.Fprintf(os.Stderr, "Error sending shutdown: %v\n", err)
				}
				return a.waitForCompletion()
			}

			// Send user input to workflow
			if err := a.sendUserInput(line); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				startInput()
				continue
			}

			// Transition to watching
			a.state = StateWatching
			a.spinner.Start("Thinking...")
			startPolling()

		case result := <-a.pollCh:
			if result.Err != nil {
				switch classifyPollError(result.Err) {
				case pollErrorCompleted:
					a.spinner.Stop()
					fmt.Fprintf(os.Stderr, "Session ended.\n")
					return nil
				case pollErrorTransient:
					continue
				case pollErrorFatal:
					a.consecutiveErrors++
					if a.consecutiveErrors >= 5 {
						a.spinner.Stop()
						fmt.Fprintf(os.Stderr, "Error: %v\n", result.Err)
						return result.Err
					}
					continue
				}
			}
			a.consecutiveErrors = 0

			// Render new items
			a.renderNewItems(result.Items)

			// Update spinner message based on phase
			a.spinner.SetMessage(PhaseMessage(result.Status.Phase, result.Status.ToolsInFlight))

			// Check for approval pending
			if result.Status.Phase == workflow.PhaseApprovalPending &&
				len(result.Status.PendingApprovals) > 0 && a.state == StateWatching {
				if a.autoApprove {
					// Auto-approve without prompting (user chose "always")
					callIDs := make([]string, len(result.Status.PendingApprovals))
					for i, ap := range result.Status.PendingApprovals {
						callIDs[i] = ap.CallID
					}
					_ = a.sendApprovalResponse(workflow.ApprovalResponse{Approved: callIDs})
					continue
				}
				a.spinner.Stop()
				stopPolling()
				a.state = StateApproval
				a.renderApprovalPrompt(result.Status.PendingApprovals)
				startInput()
				continue
			}

			// Check for escalation pending (on-failure mode)
			if result.Status.Phase == workflow.PhaseEscalationPending &&
				len(result.Status.PendingEscalations) > 0 && a.state == StateWatching {
				a.spinner.Stop()
				stopPolling()
				a.state = StateEscalation
				a.renderEscalationPrompt(result.Status.PendingEscalations)
				startInput()
				continue
			}

			// Check if turn is complete
			if a.isTurnComplete(result.Items) && result.Status.Phase == workflow.PhaseWaitingForInput {
				a.spinner.Stop()

				// Render status line
				a.renderer.RenderStatusLine(a.config.Model, result.Status.TotalTokens, result.Status.TurnCount)

				// Transition to input
				stopPolling()
				a.state = StateInput
				startInput()
			}

		case <-a.sigCh:
			a.handleInterrupt(startPolling, stopPolling, startInput)
			if a.state == StateShutdown {
				return nil // disconnect cleanly, workflow stays alive
			}
		}
	}
}

func (a *App) readInput() {
	line, err := a.rl.Readline()
	if err != nil {
		if err == readline.ErrInterrupt {
			// Ctrl+C during input — non-blocking send to sigCh
			select {
			case a.sigCh <- syscall.SIGINT:
			default: // signal already pending, skip
			}
			return
		}
		if err == io.EOF {
			// Ctrl+D — exit
			a.inputCh <- "/exit"
			return
		}
		// Unexpected error — signal exit instead of hanging
		a.inputCh <- "/exit"
		return
	}
	a.inputCh <- line
}

func (a *App) sendUserInput(content string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	updateHandle, err := a.client.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   a.workflowID,
		UpdateName:   workflow.UpdateUserInput,
		Args:         []interface{}{workflow.UserInput{Content: content}},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	if err != nil {
		return err
	}

	var accepted workflow.UserInputAccepted
	return updateHandle.Get(ctx, &accepted)
}

func (a *App) sendInterrupt() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	updateHandle, err := a.client.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   a.workflowID,
		UpdateName:   workflow.UpdateInterrupt,
		Args:         []interface{}{workflow.InterruptRequest{}},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	if err != nil {
		return err
	}

	var resp workflow.InterruptResponse
	return updateHandle.Get(ctx, &resp)
}

func (a *App) sendShutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	updateHandle, err := a.client.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   a.workflowID,
		UpdateName:   workflow.UpdateShutdown,
		Args:         []interface{}{workflow.ShutdownRequest{}},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	if err != nil {
		return err
	}

	var resp workflow.ShutdownResponse
	return updateHandle.Get(ctx, &resp)
}

func (a *App) handleInterrupt(startPolling, stopPolling, startInput func()) {
	a.interruptMu.Lock()
	defer a.interruptMu.Unlock()

	now := time.Now()

	switch a.state {
	case StateWatching:
		if now.Sub(a.lastInterruptTime) < 2*time.Second {
			// Second Ctrl+C within 2s — disconnect (workflow stays alive)
			a.spinner.Stop()
			a.printResumeHint()
			a.state = StateShutdown
			return
		}

		// First Ctrl+C — interrupt current turn
		a.lastInterruptTime = now
		a.spinner.Stop()
		fmt.Fprintf(os.Stderr, "\nInterrupting... (Ctrl+C again to disconnect)\n")
		_ = a.sendInterrupt()

		// Stay in watching mode, wait for turn_complete(interrupted)
		a.spinner.Start("Interrupting...")

	case StateApproval:
		// Ctrl+C during approval — interrupt the workflow, skip all tools
		a.lastInterruptTime = now
		fmt.Fprintf(os.Stderr, "\nInterrupting...\n")
		_ = a.sendInterrupt()
		a.pendingApprovals = nil
		a.state = StateWatching
		a.spinner.Start("Interrupting...")
		startPolling()

	case StateInput:
		// Ctrl+C during input — disconnect (workflow stays alive)
		a.printResumeHint()
		a.state = StateShutdown
	}
}

func (a *App) renderNewItems(items []models.ConversationItem) {
	rendered := false
	for _, item := range items {
		if item.Seq <= a.lastRenderedSeq {
			continue
		}
		if !rendered {
			// Stop spinner once before rendering batch
			a.spinner.Stop()
			rendered = true
		}
		a.renderer.RenderItem(item)
		a.lastRenderedSeq = item.Seq
	}
}

func (a *App) isTurnComplete(items []models.ConversationItem) bool {
	for _, item := range items {
		if item.Seq <= a.lastRenderedSeq-1 {
			continue
		}
		if item.Type == models.ItemTypeTurnComplete {
			return true
		}
	}
	return false
}

func (a *App) waitForCompletion() error {
	// Wait briefly for workflow to complete
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	run := a.client.GetWorkflow(ctx, a.workflowID, "")
	var result workflow.WorkflowResult
	if err := run.Get(ctx, &result); err != nil {
		// Workflow might take time to complete, that's OK
		fmt.Fprintf(os.Stderr, "Session closed.\n")
		return nil
	}

	fmt.Fprintf(os.Stderr, "Session ended. Tokens: %d, Tools: %d\n",
		result.TotalTokens, len(result.ToolCallsExecuted))
	return nil
}

func (a *App) renderApprovalPrompt(approvals []workflow.PendingApproval) {
	a.pendingApprovals = approvals
	fmt.Fprintf(os.Stderr, "\n")
	for i, ap := range approvals {
		fmt.Fprintf(os.Stderr, "  %s[%d]%s %sTool:%s %s\n",
			a.renderer.color(colorCyan), i+1, a.renderer.color(colorReset),
			a.renderer.color(colorYellow), a.renderer.color(colorReset), ap.ToolName)
		fmt.Fprintf(os.Stderr, "      %s\n", formatApprovalDetail(ap.ToolName, ap.Arguments))
		if ap.Reason != "" {
			fmt.Fprintf(os.Stderr, "      %sReason:%s %s\n", a.renderer.color(colorFaint), a.renderer.color(colorReset), ap.Reason)
		}
		fmt.Fprintf(os.Stderr, "\n")
	}
	if len(approvals) > 1 {
		fmt.Fprintf(os.Stderr, "Allow? [y]es / [n]o / [a]lways / 1,2 (select by index): ")
	} else {
		fmt.Fprintf(os.Stderr, "Allow? [y]es / [n]o / [a]lways: ")
	}
}

// handleApprovalInput parses the user's response to an approval prompt.
// Supports:
//   - "y"/"yes" — approve all
//   - "n"/"no" — deny all
//   - "a"/"always" — approve all + auto-approve future
//   - "1,3" — approve indices 1 and 3, deny the rest
//
// Returns nil if the input is not recognized.
func (a *App) handleApprovalInput(line string) *workflow.ApprovalResponse {
	line = strings.ToLower(strings.TrimSpace(line))

	allCallIDs := make([]string, len(a.pendingApprovals))
	for i, ap := range a.pendingApprovals {
		allCallIDs[i] = ap.CallID
	}

	switch line {
	case "y", "yes":
		return &workflow.ApprovalResponse{Approved: allCallIDs}
	case "n", "no":
		return &workflow.ApprovalResponse{Denied: allCallIDs}
	case "a", "always":
		a.autoApprove = true
		return &workflow.ApprovalResponse{Approved: allCallIDs}
	}

	// Try index-based selection: "1,3" or "1, 3" or "2"
	indices := parseApprovalIndices(line, len(a.pendingApprovals))
	if indices == nil {
		return nil // not recognized
	}

	approvedSet := make(map[int]bool, len(indices))
	for _, idx := range indices {
		approvedSet[idx] = true
	}

	var approved, denied []string
	for i, callID := range allCallIDs {
		if approvedSet[i+1] { // 1-based indices
			approved = append(approved, callID)
		} else {
			denied = append(denied, callID)
		}
	}

	return &workflow.ApprovalResponse{Approved: approved, Denied: denied}
}

// parseApprovalIndices parses a comma-separated list of 1-based indices.
// Returns nil if the input is not valid.
func parseApprovalIndices(input string, maxIndex int) []int {
	parts := strings.Split(input, ",")
	var indices []int
	seen := make(map[int]bool)

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var idx int
		n, err := fmt.Sscanf(part, "%d", &idx)
		if err != nil || n != 1 || idx < 1 || idx > maxIndex {
			return nil // invalid
		}
		if !seen[idx] {
			seen[idx] = true
			indices = append(indices, idx)
		}
	}

	if len(indices) == 0 {
		return nil
	}
	return indices
}

func (a *App) sendApprovalResponse(resp workflow.ApprovalResponse) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	updateHandle, err := a.client.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   a.workflowID,
		UpdateName:   workflow.UpdateApprovalResponse,
		Args:         []interface{}{resp},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	if err != nil {
		return err
	}

	var ack workflow.ApprovalResponseAck
	return updateHandle.Get(ctx, &ack)
}

// formatApprovalDetail extracts a human-readable detail string from tool arguments.
func formatApprovalDetail(toolName, arguments string) string {
	var args map[string]interface{}
	if json.Unmarshal([]byte(arguments), &args) == nil {
		switch toolName {
		case "shell":
			if cmd, ok := args["command"].(string); ok {
				return "Command: " + cmd
			}
		case "write_file":
			if path, ok := args["file_path"].(string); ok {
				return "Path: " + path
			}
		case "apply_patch":
			if path, ok := args["file_path"].(string); ok {
				return "Path: " + path
			}
		}
	}
	display := arguments
	if len(display) > 300 {
		display = display[:300] + "..."
	}
	return "Args: " + display
}

func (a *App) renderEscalationPrompt(escalations []workflow.EscalationRequest) {
	a.pendingEscalations = escalations
	fmt.Fprintf(os.Stderr, "\n%sSandbox failure — escalation needed:%s\n\n",
		a.renderer.color(colorYellow), a.renderer.color(colorReset))
	for i, esc := range escalations {
		fmt.Fprintf(os.Stderr, "  %s[%d]%s %sTool:%s %s\n",
			a.renderer.color(colorCyan), i+1, a.renderer.color(colorReset),
			a.renderer.color(colorYellow), a.renderer.color(colorReset), esc.ToolName)
		fmt.Fprintf(os.Stderr, "      %s\n", formatApprovalDetail(esc.ToolName, esc.Arguments))
		if esc.Output != "" {
			outputPreview := esc.Output
			if len(outputPreview) > 200 {
				outputPreview = outputPreview[:200] + "..."
			}
			fmt.Fprintf(os.Stderr, "      %sOutput:%s %s\n",
				a.renderer.color(colorRed), a.renderer.color(colorReset), outputPreview)
		}
		fmt.Fprintf(os.Stderr, "\n")
	}
	fmt.Fprintf(os.Stderr, "Re-run without sandbox? [y]es / [n]o: ")
}

func (a *App) handleEscalationInput(line string) *workflow.EscalationResponse {
	line = strings.ToLower(strings.TrimSpace(line))

	allCallIDs := make([]string, len(a.pendingEscalations))
	for i, esc := range a.pendingEscalations {
		allCallIDs[i] = esc.CallID
	}

	switch line {
	case "y", "yes":
		return &workflow.EscalationResponse{Approved: allCallIDs}
	case "n", "no":
		return &workflow.EscalationResponse{Denied: allCallIDs}
	}

	// Try index-based selection
	indices := parseApprovalIndices(line, len(a.pendingEscalations))
	if indices == nil {
		return nil
	}

	approvedSet := make(map[int]bool, len(indices))
	for _, idx := range indices {
		approvedSet[idx] = true
	}

	var approved, denied []string
	for i, callID := range allCallIDs {
		if approvedSet[i+1] {
			approved = append(approved, callID)
		} else {
			denied = append(denied, callID)
		}
	}

	return &workflow.EscalationResponse{Approved: approved, Denied: denied}
}

func (a *App) sendEscalationResponse(resp workflow.EscalationResponse) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	updateHandle, err := a.client.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   a.workflowID,
		UpdateName:   workflow.UpdateEscalationResponse,
		Args:         []interface{}{resp},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	if err != nil {
		return err
	}

	var ack workflow.EscalationResponseAck
	return updateHandle.Get(ctx, &ack)
}

func (a *App) printResumeHint() {
	fmt.Fprintf(os.Stderr, "\nSession suspended. Resume with:\n  cli --session %s\n", a.workflowID)
}

// pollErrorKind classifies errors from workflow queries.
type pollErrorKind int

const (
	pollErrorTransient pollErrorKind = iota // retry silently
	pollErrorCompleted                      // workflow done, exit
	pollErrorFatal                          // show error, exit
)

// classifyPollError categorizes a poll error using Temporal SDK typed errors.
func classifyPollError(err error) pollErrorKind {
	// Typed checks first
	var notFoundErr *serviceerror.NotFound
	if errors.As(err, &notFoundErr) {
		return pollErrorCompleted
	}

	var notReadyErr *serviceerror.WorkflowNotReady
	if errors.As(err, &notReadyErr) {
		return pollErrorTransient
	}

	var queryFailedErr *serviceerror.QueryFailed
	if errors.As(err, &queryFailedErr) {
		return pollErrorTransient
	}

	// Fallback string check for edge cases
	if strings.Contains(err.Error(), "workflow execution already completed") {
		return pollErrorCompleted
	}

	return pollErrorFatal
}
