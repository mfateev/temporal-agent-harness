package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"go.temporal.io/sdk/client"

	"github.com/mfateev/temporal-agent-harness/internal/models"
	"github.com/mfateev/temporal-agent-harness/internal/temporalclient"
	"github.com/mfateev/temporal-agent-harness/internal/version"
	"github.com/mfateev/temporal-agent-harness/internal/workflow"
)

type modelOption struct {
	Provider    string
	Model       string
	DisplayName string
}

func defaultModelOptions() []modelOption {
	return []modelOption{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "openai", Model: "gpt-4o-mini"},
		{Provider: "openai", Model: "gpt-4-turbo"},
		{Provider: "openai", Model: "gpt-3.5-turbo"},
		{Provider: "anthropic", Model: "claude-opus-4-6"},
		{Provider: "anthropic", Model: "claude-opus-4-5"},
		{Provider: "anthropic", Model: "claude-sonnet-4.5-20250929"},
		{Provider: "anthropic", Model: "claude-sonnet-4-0"},
	}
}

func modelSelectorOptions(opts []modelOption) []SelectorOption {
	result := make([]SelectorOption, 0, len(opts))
	for _, opt := range opts {
		label := fmt.Sprintf("%s (%s)", opt.Model, opt.Provider)
		if opt.DisplayName != "" {
			label = fmt.Sprintf("%s (%s: %s)", opt.Model, opt.Provider, opt.DisplayName)
		}
		result = append(result, SelectorOption{Label: label})
	}
	return result
}

// currentModelOptions returns the cached model list if available, otherwise
// the hardcoded default list.
func (m *Model) currentModelOptions() []modelOption {
	if len(m.cachedModelOptions) > 0 {
		return m.cachedModelOptions
	}
	return defaultModelOptions()
}

// modelOptionAt returns the provider and model for the given index in the
// current model options list.
func (m *Model) modelOptionAt(idx int) (provider, model string) {
	opts := m.currentModelOptions()
	if idx < 0 || idx >= len(opts) {
		return "", ""
	}
	return opts[idx].Provider, opts[idx].Model
}

const (
	TaskQueue         = "temporal-agent-harness"
	MaxTextareaHeight = 10 // Maximum height for multi-line input
)

// State represents the CLI state machine state.
type State int

const (
	StateStartup            State = iota
	StateSessionPicker // waiting for user to pick or create a session
	StateInput
	StateWatching
	StateApproval
	StateEscalation
	StateUserInputQuestion
	StateShutdown
)

// Config holds CLI configuration.
type Config struct {
	TemporalHost string
	Message      string // Initial message for new workflow
	Model        string
	NoMarkdown   bool
	NoColor      bool
	Cwd          string
	ApprovalMode models.ApprovalMode

	// Sandbox settings
	SandboxMode          string   // "full-access", "read-only", "workspace-write"
	SandboxWritableRoots []string // Writable roots for workspace-write mode
	SandboxNetworkAccess bool     // Whether network is allowed

	// Codex config
	CodexHome string // Path to codex config directory (default: ~/.codex)

	// TUI settings
	Provider           string // LLM provider (openai, anthropic, google)
	Inline             bool   // Disable alt-screen mode
	DisableSuggestions bool   // Disable prompt suggestions
}

// Model is the bubbletea model for the interactive CLI.
type Model struct {
	// Configuration
	config Config
	client client.Client
	keys   KeyMap
	styles Styles

	// State machine
	state           State
	workflowID      string
	lastRenderedSeq int

	// Sub-models
	viewport viewport.Model
	textarea textarea.Model
	spinner  spinner.Model

	// Layout
	width  int
	height int
	ready  bool

	// Viewport content
	viewportContent string

	// Renderer
	renderer *ItemRenderer

	// Status
	modelName         string
	totalTokens       int
	totalCachedTokens int
	turnCount         int
	spinnerMsg        string
	workerVersion     string

	// Approval state
	pendingApprovals   []workflow.PendingApproval
	autoApprove        bool
	pendingEscalations []workflow.EscalationRequest

	// User input question state
	pendingUserInputReq *workflow.PendingUserInputRequest

	// Selector (replaces textarea for approval/escalation/user-input states)
	selector *SelectorModel

	// Plan mode state
	parentWorkflowID string // saved parent ID while attached to planner
	plannerAgentID   string // agent ID of the planner child
	plannerActive    bool   // whether TUI is attached to the planner child

	// Plan rendering (update_plan tool)
	lastRenderedPlan *workflow.PlanState

	// Prompt suggestion (ghost text shown as placeholder after turn completes)
	suggestion string

	// Paste buffering: multi-line pastes show "[N lines pasted]" placeholder
	pastedContent string
	pasteLabel    string

	// Ctrl+C tracking
	lastInterruptTime time.Time

	// Watching (blocking get_state_update)
	watchCh           chan WatchResult
	watchCancel       context.CancelFunc
	lastPhase         workflow.TurnPhase
	consecutiveErrors int

	// Error/exit state
	err      error
	quitting bool

	// Inline mode (no alt-screen)
	inline bool

	// Provider
	provider string

	// /model command state
	selectingModel     bool
	cachedModelOptions []modelOption
	modelsFetched      bool
	modelsFetching     bool

	// Session picker state
	selectingSession bool
	sessionEntries   []SessionListEntry
}

// NewModel creates a new bubbletea model.
func NewModel(config Config, c client.Client) Model {
	styles := DefaultStyles()
	if config.NoColor {
		styles = NoColorStyles()
	}

	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.Prompt = "❯ "
	ta.CharLimit = 0
	ta.SetHeight(1) // Single line until Shift+Enter adds a newline
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(true) // Enable multi-line input
	// Shift+Enter sends ctrl+j (LF) in most terminals, distinct from Enter (CR)
	ta.KeyMap.InsertNewline.SetKeys("ctrl+j")

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	initialState := StateStartup
	if config.Message == "" {
		initialState = StateSessionPicker // show picker while fetching sessions
	}

	return Model{
		config:          config,
		client:          c,
		keys:            DefaultKeyMap(),
		styles:          styles,
		state:           initialState,
		lastRenderedSeq: -1,
		textarea:        ta,
		spinner:         sp,
		watchCh:         make(chan WatchResult, 1),
		modelName:       config.Model,
		provider:        config.Provider,
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.spinner.Tick,
	}

	if m.config.Message != "" {
		// -m provided: start new session immediately (skip picker)
		cmds = append(cmds, startWorkflowCmd(m.client, m.config))
	} else {
		// No message: show session picker, fetch sessions in background
		cwd := m.config.Cwd
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		harnessID := harnessWorkflowID(cwd)
		cmds = append(cmds, fetchSessionsCmd(m.client, harnessID))
	}

	return tea.Batch(cmds...)
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)

	case tea.KeyMsg:
		return m.handleKeyMsg(msg)

	case spinner.TickMsg:
		if m.state == StateWatching || m.state == StateStartup || m.state == StateSessionPicker {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}

	case HarnessSessionsListMsg:
		if msg.Err != nil {
			// Failed to fetch sessions — fall back to input so user can still type
			m.appendToViewport(fmt.Sprintf("Failed to fetch sessions: %v\n", msg.Err))
			m.state = StateInput
			return &m, m.focusTextarea()
		}
		m.sessionEntries = msg.Entries
		m.selectingSession = true
		m.selector = m.buildSessionSelector(msg.Entries)
		m.state = StateSessionPicker
		return &m, nil

	case WorkflowStartedMsg:
		return m.handleWorkflowStarted(msg)

	case WorkflowStartErrorMsg:
		m.err = msg.Err
		m.quitting = true
		return &m, tea.Quit

	case PollResultMsg:
		return m.handlePollResult(msg)

	case WatchResultMsg:
		return m.handleWatchResult(msg)

	case UserInputSentMsg:
		m.state = StateWatching
		m.spinnerMsg = "Thinking..."
		// Render initial items from the response snapshot
		m.renderNewItems(msg.Response.Items)
		// Update status from snapshot
		m.totalTokens = msg.Response.Status.TotalTokens
		m.totalCachedTokens = msg.Response.Status.TotalCachedTokens
		m.turnCount = msg.Response.Status.TurnCount
		if msg.Response.Status.WorkerVersion != "" {
			m.workerVersion = msg.Response.Status.WorkerVersion
		}
		m.lastPhase = msg.Response.Status.Phase
		cmds = append(cmds, m.startWatching())

	case UserInputErrorMsg:
		// Show error, return to input
		m.appendToViewport(fmt.Sprintf("Error: %v\n", msg.Err))
		m.state = StateInput
		cmds = append(cmds, m.focusTextarea())

	case InterruptSentMsg:
		m.spinnerMsg = "Interrupting..."

	case InterruptErrorMsg:
		m.appendToViewport(fmt.Sprintf("Error sending interrupt: %v\n", msg.Err))

	case ShutdownSentMsg:
		if m.plannerActive {
			// In plan mode: shutdown was sent to the planner child.
			// Wait for it to complete, then extract the plan.
			m.spinnerMsg = "Planner shutting down..."
			return &m, waitForCompletionCmd(m.client, m.workflowID)
		}
		m.quitting = true
		return &m, waitForCompletionCmd(m.client, m.workflowID)

	case ShutdownErrorMsg:
		m.appendToViewport(fmt.Sprintf("Error sending shutdown: %v\n", msg.Err))

	case ApprovalSentMsg:
		m.pendingApprovals = nil
		m.selector = nil
		m.state = StateWatching
		m.spinnerMsg = "Running tools..."
		cmds = append(cmds, m.startWatching())

	case ApprovalErrorMsg:
		m.appendToViewport(fmt.Sprintf("Error sending approval: %v\n", msg.Err))

	case EscalationSentMsg:
		m.pendingEscalations = nil
		m.selector = nil
		m.state = StateWatching
		m.spinnerMsg = "Re-running tools..."
		cmds = append(cmds, m.startWatching())

	case EscalationErrorMsg:
		m.appendToViewport(fmt.Sprintf("Error sending escalation response: %v\n", msg.Err))

	case CompactSentMsg:
		m.appendToViewport(m.renderer.RenderSystemMessage("Context compacted."))
		m.state = StateWatching
		m.spinnerMsg = "Compacting..."
		cmds = append(cmds, m.startWatching())

	case CompactErrorMsg:
		m.appendToViewport(fmt.Sprintf("Error compacting context: %v\n", msg.Err))
		m.state = StateInput
		cmds = append(cmds, m.focusTextarea())

	case ModelUpdateSentMsg:
		m.provider = msg.Provider
		m.modelName = msg.Model
		m.appendToViewport(m.renderer.RenderSystemMessage(
			fmt.Sprintf("Model updated to %s (%s).", msg.Model, msg.Provider)))
		m.selectingModel = false
		m.selector = nil
		m.state = StateInput
		cmds = append(cmds, m.focusTextarea())

	case ModelUpdateErrorMsg:
		m.appendToViewport(fmt.Sprintf("Error updating model: %v\n", msg.Err))
		m.selectingModel = false
		m.selector = nil
		m.state = StateInput
		cmds = append(cmds, m.focusTextarea())

	case ModelsFetchedMsg:
		m.modelsFetching = false
		m.modelsFetched = true
		if msg.Models != nil {
			m.cachedModelOptions = msg.Models
		}
		// If user is waiting for the selector, show it now
		if m.selectingModel {
			m.appendToViewport(m.renderer.RenderSystemMessage("Select a model (Esc to cancel):"))
			m.selector = NewSelectorModel(modelSelectorOptions(m.currentModelOptions()), m.styles)
			m.selector.SetWidth(m.width)
			m.state = StateInput
		}

	case UserInputQuestionSentMsg:
		m.pendingUserInputReq = nil
		m.selector = nil
		m.state = StateWatching
		m.spinnerMsg = "Processing answer..."
		cmds = append(cmds, m.startWatching())

	case UserInputQuestionErrorMsg:
		m.appendToViewport(fmt.Sprintf("Error sending user input response: %v\n", msg.Err))

	case PlanRequestAcceptedMsg:
		return m.handlePlanRequestAccepted(msg)

	case PlanRequestErrorMsg:
		m.appendToViewport(fmt.Sprintf("Error starting plan mode: %v\n", msg.Err))
		m.state = StateInput
		cmds = append(cmds, m.focusTextarea())

	case SuggestionPollMsg:
		return m.handleSuggestionPoll(msg)

	case PlannerCompletedMsg:
		return m.handlePlannerCompleted(msg)

	case SessionCompletedMsg:
		if m.plannerActive {
			// Planner child completed — extract plan and return to parent
			m.stopWatching()
			childWfID := m.workflowID
			return &m, queryChildConversationItems(m.client, childWfID)
		}
		m.stopWatching()
		if msg.Result != nil {
			sessionEndMsg := fmt.Sprintf("Session ended. Tokens: %d", msg.Result.TotalTokens)
			if msg.Result.TotalCachedTokens > 0 {
				sessionEndMsg += fmt.Sprintf(" (%d cached)", msg.Result.TotalCachedTokens)
			}
			sessionEndMsg += fmt.Sprintf(", Tools: %d\n", len(msg.Result.ToolCallsExecuted))
			m.appendToViewport(sessionEndMsg)
		} else {
			m.appendToViewport("Session ended.\n")
		}
		m.quitting = true
		return &m, tea.Quit

	case SessionErrorMsg:
		if m.plannerActive {
			// Planner child errored or completed while polling — extract plan
			m.stopWatching()
			childWfID := m.workflowID
			return &m, queryChildConversationItems(m.client, childWfID)
		}
		m.appendToViewport("Session closed.\n")
		m.quitting = true
		return &m, tea.Quit
	}

	return &m, tea.Batch(cmds...)
}

// View implements tea.Model.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	if !m.ready {
		return m.styles.SpinnerMessage.Render(m.spinner.View() + " Starting...")
	}

	// Build viewport content
	vpView := m.viewport.View()

	// Separator
	sep := m.styles.Separator.Render(strings.Repeat("─", m.width))

	// Status bar
	statusBar := m.renderStatusBar()

	// Input area
	var inputView string
	switch m.state {
	case StateSessionPicker:
		if m.selector != nil {
			inputView = m.selector.View()
		} else {
			inputView = m.spinner.View() + " " + m.styles.SpinnerMessage.Render("Loading sessions...")
		}
	case StateInput:
		if m.selectingModel && m.selector != nil {
			inputView = m.selector.View()
		} else {
			inputView = m.textarea.View()
		}
	case StateApproval, StateEscalation, StateUserInputQuestion:
		if m.selector != nil {
			inputView = m.selector.View()
		} else {
			inputView = m.textarea.View()
		}
	default:
		// Watching/Startup: show spinner
		inputView = m.spinner.View() + " " + m.styles.SpinnerMessage.Render(m.spinnerMsg)
	}

	// Bottom separator below input (matches Claude Code layout)
	sepBottom := sep

	return lipgloss.JoinVertical(lipgloss.Left,
		vpView,
		sep,
		inputView,
		sepBottom,
		statusBar,
	)
}

func (m Model) renderStatusBar() string {
	model := m.modelName
	if m.provider != "" && m.provider != "openai" {
		model = fmt.Sprintf("%s (%s)", m.modelName, m.provider)
	}

	tokens := formatTokens(m.totalTokens)
	if m.totalCachedTokens > 0 {
		tokens += fmt.Sprintf(" (%s cached)", formatTokens(m.totalCachedTokens))
	}
	turn := fmt.Sprintf("turn %d", m.turnCount)

	var stateLabel string
	if m.plannerActive {
		switch m.state {
		case StateInput:
			stateLabel = "plan mode"
		case StateWatching:
			stateLabel = "planning"
		default:
			stateLabel = "plan mode"
		}
	} else {
		switch m.state {
		case StateSessionPicker:
			stateLabel = "picker"
		case StateInput:
			stateLabel = "ready"
		case StateWatching:
			stateLabel = "working"
		case StateApproval:
			stateLabel = "approval"
		case StateEscalation:
			stateLabel = "escalation"
		case StateUserInputQuestion:
			stateLabel = "question"
		case StateStartup:
			stateLabel = "connecting"
		default:
			stateLabel = ""
		}
	}

	wv := m.workerVersion
	if wv == "" {
		wv = "?"
	}
	left := fmt.Sprintf(" %s · %s tokens · %s · %s", model, tokens, turn, stateLabel)
	right := fmt.Sprintf("cli:%s · worker:%s ", version.GitCommit, wv)
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap) + right
	return m.styles.StatusBar.Render(bar)
}

func (m *Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height

	// Reserve space: separator(1) + input(variable) + separator(1) + status(1)
	taHeight := m.inputAreaHeight()
	vpHeight := m.height - taHeight - 3 // 3 for top separator + bottom separator + status
	if vpHeight < 1 {
		vpHeight = 1
	}

	if !m.ready {
		m.viewport = viewport.New(m.width, vpHeight)
		m.viewport.SetContent(m.viewportContent)

		m.renderer = NewItemRenderer(m.width, m.config.NoColor, m.config.NoMarkdown, m.styles)

		m.textarea.SetWidth(m.width)
		m.ready = true

		// Focus textarea if starting in input mode
		if m.state == StateInput {
			return m, m.focusTextarea()
		}
	} else {
		m.viewport.Width = m.width
		m.viewport.Height = vpHeight
		m.textarea.SetWidth(m.width)

		if m.renderer != nil {
			m.renderer.width = m.width
		}
	}

	return m, nil
}

func (m *Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m.handleCtrlC()
	case tea.KeyCtrlD:
		if m.state == StateInput || m.state == StateSessionPicker {
			// Ctrl+D during input or picker = disconnect/quit
			m.quitting = true
			return m, tea.Quit
		}
	}

	switch m.state {
	case StateSessionPicker:
		return m.handleSessionPickerKey(msg)
	case StateInput:
		return m.handleInputKey(msg)
	case StateWatching:
		return m.handleWatchingKey(msg)
	case StateApproval:
		return m.handleApprovalKey(msg)
	case StateEscalation:
		return m.handleEscalationKey(msg)
	case StateUserInputQuestion:
		return m.handleUserInputQuestionKey(msg)
	}

	return m, nil
}

func (m *Model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// /model selection uses the selector UI.
	if m.selectingModel {
		if m.selector != nil {
			if m.isViewportScrollKey(msg) {
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}

			done := m.selector.Update(msg)
			if done {
				m.selectingModel = false
				if m.selector.Cancelled() {
					m.selector = nil
					m.state = StateInput
					return m, m.focusTextarea()
				}
				idx := m.selector.Selected()
				provider, model := m.modelOptionAt(idx)
				m.selector = nil
				if provider == "" || model == "" {
					m.appendToViewport("Invalid model selection.\n")
					m.state = StateInput
					return m, m.focusTextarea()
				}
				if m.workflowID == "" {
					m.provider = provider
					m.modelName = model
					m.appendToViewport(m.renderer.RenderSystemMessage(
						fmt.Sprintf("Model set to %s (%s). Start a session to apply.", model, provider)))
					m.state = StateInput
					return m, m.focusTextarea()
				}
				m.spinnerMsg = "Updating model..."
				m.state = StateWatching
				m.textarea.Blur()
				return m, sendUpdateModelCmd(m.client, m.workflowID, provider, model)
			}
			return m, nil
		}
		// Selector not ready yet (still fetching) — allow Esc to cancel, ignore other keys
		if msg.Type == tea.KeyEsc {
			m.selectingModel = false
			m.modelsFetching = false
			m.state = StateInput
			return m, m.focusTextarea()
		}
		return m, nil
	}

	// Intercept multi-line paste: show "[N lines pasted]" placeholder
	if msg.Paste && msg.Type == tea.KeyRunes && strings.ContainsRune(string(msg.Runes), '\n') {
		content := string(msg.Runes)
		lines := strings.Count(content, "\n") + 1
		m.pastedContent = content
		m.pasteLabel = fmt.Sprintf("[%d lines pasted]", lines)
		// Insert the placeholder at the cursor via a synthetic rune message
		synthetic := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(m.pasteLabel)}
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(synthetic)
		return m, cmd
	}

	// Tab key: accept suggestion if present and textarea is empty
	if msg.Type == tea.KeyTab {
		if m.suggestion != "" && m.textarea.Value() == "" {
			m.textarea.SetValue(m.suggestion)
			m.textarea.CursorEnd()
			m.clearSuggestion()
		}
		return m, nil
	}

	// Ignore Enter during a bracketed paste (don't submit mid-paste)
	if msg.Paste && msg.Type == tea.KeyEnter {
		return m, nil
	}

	// Handle Enter for submit
	if msg.Type == tea.KeyEnter {
		line := strings.TrimSpace(m.expandPastedContent(m.textarea.Value()))
		m.textarea.Reset()
		m.pastedContent = ""
		m.pasteLabel = ""
		m.clearSuggestion()

		// Reset textarea to initial height after submit
		m.textarea.SetHeight(1)
		// Recalculate viewport
		vpHeight := m.height - 1 - 2
		if vpHeight < 1 {
			vpHeight = 1
		}
		m.viewport.Height = vpHeight

		if line == "" {
			return m, nil
		}

		// Handle special commands
		if line == "/exit" || line == "/quit" {
			m.quitting = true
			return m, tea.Quit
		}
		if line == "/end" {
			m.spinnerMsg = "Ending session..."
			m.state = StateWatching
			m.textarea.Blur()
			return m, sendShutdownCmd(m.client, m.workflowID)
		}
		if line == "/compact" {
			if m.workflowID == "" {
				m.appendToViewport("No active session to compact.\n")
				return m, nil
			}
			m.spinnerMsg = "Compacting context..."
			m.state = StateWatching
			m.textarea.Blur()
			return m, sendCompactCmd(m.client, m.workflowID)
		}
		if line == "/model" {
			if m.modelsFetched {
				// Models already cached — show selector immediately
				m.appendToViewport(m.renderer.RenderSystemMessage("Select a model (Esc to cancel):"))
				m.selector = NewSelectorModel(modelSelectorOptions(m.currentModelOptions()), m.styles)
				m.selector.SetWidth(m.width)
				m.selectingModel = true
				m.state = StateInput
				m.textarea.Blur()
				return m, nil
			}
			if !m.modelsFetching {
				// Fire async fetch
				m.modelsFetching = true
				m.selectingModel = true
				m.appendToViewport(m.renderer.RenderSystemMessage("Fetching available models..."))
				m.textarea.Blur()
				return m, fetchModelsCmd()
			}
			// Already fetching — just wait
			return m, nil
		}
		if strings.HasPrefix(line, "/plan") {
			if m.workflowID == "" {
				m.appendToViewport("No active session. Start a session first.\n")
				return m, nil
			}
			if m.plannerActive {
				m.appendToViewport("Already in plan mode. Use /done to finish.\n")
				return m, nil
			}
			planMsg := strings.TrimSpace(strings.TrimPrefix(line, "/plan"))
			if planMsg == "" {
				m.appendToViewport("Usage: /plan <message>\n")
				return m, nil
			}
			m.appendToViewport(m.renderer.RenderSystemMessage("Starting plan mode..."))
			m.spinnerMsg = "Starting planner..."
			m.state = StateWatching
			m.textarea.Blur()
			return m, sendPlanRequestCmd(m.client, m.workflowID, planMsg)
		}
		if line == "/done" {
			if !m.plannerActive {
				m.appendToViewport("Not in plan mode. Use /plan <message> to start.\n")
				return m, nil
			}
			m.appendToViewport(m.renderer.RenderSystemMessage("Ending plan mode..."))
			m.spinnerMsg = "Shutting down planner..."
			m.state = StateWatching
			m.textarea.Blur()
			return m, sendShutdownCmd(m.client, m.workflowID)
		}

		// Show user message in viewport (❯ prefix, no separators)
		m.appendToViewport(m.renderer.RenderUserMessage(models.ConversationItem{
			Type:    models.ItemTypeUserMessage,
			Content: line,
		}))

		m.state = StateWatching
		m.spinnerMsg = "Thinking..."
		m.textarea.Blur()

		// If no workflow yet, start one with this message
		if m.workflowID == "" {
			m.config.Message = line
			return m, startWorkflowCmd(m.client, m.config)
		}
		return m, sendUserInputCmd(m.client, m.workflowID, line)
	}

	// Pre-expand textarea height for newline insertion (Shift+Enter / ctrl+j)
	// so the internal viewport has room before the newline is added.
	if msg.Type == tea.KeyCtrlJ {
		newHeight := m.calculateTextareaHeight() + 1
		if newHeight > MaxTextareaHeight {
			newHeight = MaxTextareaHeight
		}
		if newHeight != m.textarea.Height() {
			m.textarea.SetHeight(newHeight)
			vpHeight := m.height - newHeight - 2
			if vpHeight < 1 {
				vpHeight = 1
			}
			m.viewport.Height = vpHeight
		}
	}

	// Handle Shift+Enter and other input (textarea handles newlines automatically)
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)

	// Dynamically adjust textarea height based on content
	newHeight := m.calculateTextareaHeight()
	if newHeight != m.textarea.Height() {
		m.textarea.SetHeight(newHeight)
		vpHeight := m.height - newHeight - 2
		if vpHeight < 1 {
			vpHeight = 1
		}
		m.viewport.Height = vpHeight
	}
	
	// Route scroll keys to viewport (textarea is single-line, doesn't need them)
	if m.isScrollKey(msg) {
		var vpCmd tea.Cmd
		m.viewport, vpCmd = m.viewport.Update(msg)
		return m, vpCmd
	}

	return m, cmd
}

func (m *Model) handleWatchingKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// During watching, only allow viewport scrolling
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m *Model) handleSessionPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.selector == nil {
		// Still loading — ignore input
		return m, nil
	}

	if m.isViewportScrollKey(msg) {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	done := m.selector.Update(msg)
	if done {
		if m.selector.Cancelled() {
			// Esc — quit
			m.selector = nil
			m.selectingSession = false
			m.quitting = true
			return m, tea.Quit
		}
		idx := m.selector.Selected()
		m.selector = nil
		m.selectingSession = false

		if idx == 0 {
			// "New session" selected — go to input
			m.state = StateInput
			return m, m.focusTextarea()
		}

		// Existing session selected
		entry := m.sessionEntries[idx-1]
		m.state = StateWatching
		m.spinnerMsg = "Connecting..."
		return m, resumeWorkflowCmd(m.client, entry.WorkflowID)
	}
	return m, nil
}

func (m *Model) handleApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// When selector is active, delegate to it
	if m.selector != nil {
		if m.isViewportScrollKey(msg) {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}

		done := m.selector.Update(msg)
		if done {
			if m.selector.Confirmed() {
				selected := m.selector.Selected()
				if len(m.pendingApprovals) > 1 && selected == 3 {
					m.selector = nil
					m.textarea.SetValue("")
					return m, m.focusTextarea()
				}
				response, setAutoApprove := ApprovalSelectionToResponse(selected, m.pendingApprovals)
				if response != nil {
					if setAutoApprove {
						m.autoApprove = true
					}
					m.selector = nil
					return m, sendApprovalResponseCmd(m.client, m.workflowID, *response)
				}
			}
			if m.selector.Cancelled() {
				allCallIDs := make([]string, len(m.pendingApprovals))
				for i, ap := range m.pendingApprovals {
					allCallIDs[i] = ap.CallID
				}
				m.selector = nil
				return m, sendApprovalResponseCmd(m.client, m.workflowID, workflow.ApprovalResponse{Denied: allCallIDs})
			}
		}
		vpHeight := m.height - m.inputAreaHeight() - 2
		if vpHeight < 1 {
			vpHeight = 1
		}
		m.viewport.Height = vpHeight
		return m, nil
	}

	// Textarea fallback (for "Select individually..." mode)
	if msg.Type == tea.KeyEnter {
		line := strings.TrimSpace(m.textarea.Value())
		m.textarea.Reset()

		response, setAutoApprove := HandleApprovalInput(line, m.pendingApprovals)
		if response != nil {
			if setAutoApprove {
				m.autoApprove = true
			}
			m.textarea.Blur()
			return m, sendApprovalResponseCmd(m.client, m.workflowID, *response)
		}
		m.appendToViewport("Please enter y(es), n(o), a(lways), or indices (e.g. 1,3):\n")
		return m, nil
	}

	if m.isScrollKey(msg) {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

func (m *Model) handleEscalationKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.selector != nil {
		if m.isViewportScrollKey(msg) {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}

		done := m.selector.Update(msg)
		if done {
			if m.selector.Confirmed() {
				response := EscalationSelectionToResponse(m.selector.Selected(), m.pendingEscalations)
				if response != nil {
					m.selector = nil
					return m, sendEscalationResponseCmd(m.client, m.workflowID, *response)
				}
			}
			if m.selector.Cancelled() {
				allCallIDs := make([]string, len(m.pendingEscalations))
				for i, esc := range m.pendingEscalations {
					allCallIDs[i] = esc.CallID
				}
				m.selector = nil
				return m, sendEscalationResponseCmd(m.client, m.workflowID, workflow.EscalationResponse{Denied: allCallIDs})
			}
		}
		return m, nil
	}

	if msg.Type == tea.KeyEnter {
		line := strings.TrimSpace(m.textarea.Value())
		m.textarea.Reset()

		response := HandleEscalationInput(line, m.pendingEscalations)
		if response != nil {
			m.textarea.Blur()
			return m, sendEscalationResponseCmd(m.client, m.workflowID, *response)
		}
		m.appendToViewport("Please enter y(es) or n(o):\n")
		return m, nil
	}

	if m.isScrollKey(msg) {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

func (m *Model) handleUserInputQuestionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.selector != nil {
		if m.isViewportScrollKey(msg) {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}

		done := m.selector.Update(msg)
		if done {
			if m.selector.Confirmed() {
				selected := m.selector.Selected()
				response := UserInputSelectionToResponse(selected, m.pendingUserInputReq)
				if response != nil {
					m.selector = nil
					return m, sendUserInputQuestionResponseCmd(m.client, m.workflowID, *response)
				}
				// "Other" selected — fall back to textarea
				m.selector = nil
				m.textarea.SetValue("")
				return m, m.focusTextarea()
			}
			if m.selector.Cancelled() {
				// Esc = fall back to textarea for freeform
				m.selector = nil
				m.textarea.SetValue("")
				return m, m.focusTextarea()
			}
		}
		return m, nil
	}

	// Textarea fallback
	if msg.Type == tea.KeyEnter {
		line := strings.TrimSpace(m.textarea.Value())
		m.textarea.Reset()

		response := HandleUserInputQuestionInput(line, m.pendingUserInputReq)
		if response != nil {
			m.textarea.Blur()
			return m, sendUserInputQuestionResponseCmd(m.client, m.workflowID, *response)
		}
		m.appendToViewport("Please enter a valid option number:\n")
		return m, nil
	}

	if m.isScrollKey(msg) {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

// isScrollKey returns true if the key should be routed to the viewport
// for scrolling rather than to the textarea.
func (m *Model) isScrollKey(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
		return true
	}
	switch msg.String() {
	case "k", "j":
		return true
	}
	return false
}

func (m *Model) handleCtrlC() (tea.Model, tea.Cmd) {
	now := time.Now()

	switch m.state {
	case StateWatching:
		if m.plannerActive && now.Sub(m.lastInterruptTime) < 2*time.Second {
			// Second Ctrl+C in plan mode within 2s — detach from planner
			m.appendToViewport("\nDetaching from planner...\n")
			m.lastInterruptTime = now
			m.spinnerMsg = "Shutting down planner..."
			return m, sendShutdownCmd(m.client, m.workflowID)
		}
		if !m.plannerActive && now.Sub(m.lastInterruptTime) < 2*time.Second {
			// Second Ctrl+C within 2s — disconnect
			m.stopWatching()
			m.quitting = true
			return m, tea.Quit
		}
		// First Ctrl+C — interrupt
		m.lastInterruptTime = now
		if m.plannerActive {
			m.appendToViewport("\nInterrupting planner... (Ctrl+C again to detach)\n")
		} else {
			m.appendToViewport("\nInterrupting... (Ctrl+C again to disconnect)\n")
		}
		return m, sendInterruptCmd(m.client, m.workflowID)

	case StateApproval:
		m.lastInterruptTime = now
		m.appendToViewport("\nInterrupting...\n")
		m.pendingApprovals = nil
		m.selector = nil
		m.state = StateWatching
		m.spinnerMsg = "Interrupting..."
		m.textarea.Blur()
		cmds := []tea.Cmd{
			sendInterruptCmd(m.client, m.workflowID),
			m.startWatching(),
		}
		return m, tea.Batch(cmds...)

	case StateEscalation:
		m.lastInterruptTime = now
		m.appendToViewport("\nInterrupting...\n")
		m.pendingEscalations = nil
		m.selector = nil
		m.state = StateWatching
		m.spinnerMsg = "Interrupting..."
		m.textarea.Blur()
		cmds := []tea.Cmd{
			sendInterruptCmd(m.client, m.workflowID),
			m.startWatching(),
		}
		return m, tea.Batch(cmds...)

	case StateUserInputQuestion:
		m.lastInterruptTime = now
		m.appendToViewport("\nInterrupting...\n")
		m.pendingUserInputReq = nil
		m.selector = nil
		m.state = StateWatching
		m.spinnerMsg = "Interrupting..."
		m.textarea.Blur()
		cmds := []tea.Cmd{
			sendInterruptCmd(m.client, m.workflowID),
			m.startWatching(),
		}
		return m, tea.Batch(cmds...)

	case StateSessionPicker:
		// Ctrl+C during session picker — quit
		m.quitting = true
		return m, tea.Quit

	case StateInput:
		// Ctrl+C during input — disconnect
		m.quitting = true
		return m, tea.Quit
	}

	return m, nil
}

func (m *Model) handleWorkflowStarted(msg WorkflowStartedMsg) (tea.Model, tea.Cmd) {
	m.workflowID = msg.WorkflowID

	if msg.IsResume {
		// Render resume history
		if len(msg.Items) > 0 {
			m.appendToViewport(fmt.Sprintf("... %d previous items ...\n", len(msg.Items)))
			start := 0
			if len(msg.Items) > 20 {
				start = len(msg.Items) - 20
				m.appendToViewport(fmt.Sprintf("... showing last %d items ...\n", len(msg.Items)-start))
			}
			for _, item := range msg.Items[start:] {
				rendered := m.renderer.RenderItem(item, true)
				if rendered != "" {
					m.appendToViewport(rendered)
				}
			}
			m.lastRenderedSeq = msg.Items[len(msg.Items)-1].Seq
		}

		// Render plan if resuming a session that had an active plan
		if msg.Status.Plan != nil && len(msg.Status.Plan.Steps) > 0 {
			rendered := m.renderer.RenderPlan(msg.Status.Plan)
			if rendered != "" {
				m.appendToViewport(rendered)
			}
			m.lastRenderedPlan = msg.Status.Plan
		}

		// Set state based on turn status
		switch msg.Status.Phase {
		case workflow.PhaseWaitingForInput:
			m.state = StateInput
			return m, m.focusTextarea()
		case workflow.PhaseApprovalPending:
			m.state = StateApproval
			m.pendingApprovals = msg.Status.PendingApprovals
			m.appendToViewport(m.renderer.RenderApprovalContext(msg.Status.PendingApprovals))
			m.selector = m.buildApprovalSelector(msg.Status.PendingApprovals)
			return m, nil
		case workflow.PhaseEscalationPending:
			m.state = StateEscalation
			m.pendingEscalations = msg.Status.PendingEscalations
			m.appendToViewport(m.renderer.RenderEscalationContext(msg.Status.PendingEscalations))
			m.selector = m.buildEscalationSelector()
			return m, nil
		case workflow.PhaseUserInputPending:
			if msg.Status.PendingUserInputRequest != nil {
				m.state = StateUserInputQuestion
				m.pendingUserInputReq = msg.Status.PendingUserInputRequest
				sel := m.buildUserInputSelector(msg.Status.PendingUserInputRequest)
				if sel != nil {
					m.appendToViewport(m.renderer.RenderUserInputQuestionContext(msg.Status.PendingUserInputRequest))
					m.selector = sel
					return m, nil
				}
				m.appendToViewport(m.renderer.RenderUserInputQuestionPrompt(msg.Status.PendingUserInputRequest))
				return m, m.focusTextarea()
			}
			fallthrough
		default:
			m.state = StateWatching
			m.spinnerMsg = "Thinking..."
			return m, m.startWatching()
		}
	}

	// New workflow
	m.appendToViewport(m.renderer.RenderSystemMessage(fmt.Sprintf("Started session %s", m.workflowID)))
	if m.config.Message != "" {
		m.state = StateWatching
		m.spinnerMsg = "Thinking..."
		return m, m.startWatching()
	}
	m.state = StateInput
	return m, m.focusTextarea()
}

func (m *Model) handlePollResult(msg PollResultMsg) (tea.Model, tea.Cmd) {
	result := msg.Result

	if result.Err != nil {
		switch classifyPollError(result.Err) {
		case pollErrorCompleted:
			if m.plannerActive {
				// Planner child completed — extract plan and return to parent
				m.stopWatching()
				childWfID := m.workflowID
				return m, queryChildConversationItems(m.client, childWfID)
			}
			m.stopWatching()
			m.appendToViewport("Session ended.\n")
			m.quitting = true
			return m, tea.Quit
		case pollErrorTransient:
			return m, m.waitForWatchResult()
		case pollErrorFatal:
			m.consecutiveErrors++
			if m.consecutiveErrors >= 5 {
				m.stopWatching()
				m.appendToViewport(fmt.Sprintf("Error: %v\n", result.Err))
				m.err = result.Err
				m.quitting = true
				return m, tea.Quit
			}
			return m, m.waitForWatchResult()
		}
	}
	m.consecutiveErrors = 0

	// Render new items
	m.renderNewItems(result.Items)

	// Update status
	m.spinnerMsg = PhaseMessage(result.Status.Phase, result.Status.ToolsInFlight)
	m.totalTokens = result.Status.TotalTokens
	m.totalCachedTokens = result.Status.TotalCachedTokens
	m.turnCount = result.Status.TurnCount
	if result.Status.WorkerVersion != "" {
		m.workerVersion = result.Status.WorkerVersion
	}

	// Check for plan changes and render
	if planChanged(m.lastRenderedPlan, result.Status.Plan) {
		rendered := m.renderer.RenderPlan(result.Status.Plan)
		if rendered != "" {
			m.appendToViewport(rendered)
		}
		m.lastRenderedPlan = result.Status.Plan
	}

	// Check for approval pending
	if result.Status.Phase == workflow.PhaseApprovalPending &&
		len(result.Status.PendingApprovals) > 0 && m.state == StateWatching {
		if m.autoApprove {
			callIDs := make([]string, len(result.Status.PendingApprovals))
			for i, ap := range result.Status.PendingApprovals {
				callIDs[i] = ap.CallID
			}
			return m, sendApprovalResponseCmd(m.client, m.workflowID, workflow.ApprovalResponse{Approved: callIDs})
		}
		m.stopWatching()
		m.state = StateApproval
		m.pendingApprovals = result.Status.PendingApprovals
		m.appendToViewport(m.renderer.RenderApprovalContext(result.Status.PendingApprovals))
		m.selector = m.buildApprovalSelector(result.Status.PendingApprovals)
		return m, nil
	}

	// Check for escalation pending
	if result.Status.Phase == workflow.PhaseEscalationPending &&
		len(result.Status.PendingEscalations) > 0 && m.state == StateWatching {
		m.stopWatching()
		m.state = StateEscalation
		m.pendingEscalations = result.Status.PendingEscalations
		m.appendToViewport(m.renderer.RenderEscalationContext(result.Status.PendingEscalations))
		m.selector = m.buildEscalationSelector()
		return m, nil
	}

	// Check for user input question pending
	if result.Status.Phase == workflow.PhaseUserInputPending &&
		result.Status.PendingUserInputRequest != nil && m.state == StateWatching {
		m.stopWatching()
		m.state = StateUserInputQuestion
		m.pendingUserInputReq = result.Status.PendingUserInputRequest
		sel := m.buildUserInputSelector(result.Status.PendingUserInputRequest)
		if sel != nil {
			m.appendToViewport(m.renderer.RenderUserInputQuestionContext(result.Status.PendingUserInputRequest))
			m.selector = sel
			return m, nil
		}
		// Multi-question: fall back to textarea
		m.appendToViewport(m.renderer.RenderUserInputQuestionPrompt(result.Status.PendingUserInputRequest))
		return m, m.focusTextarea()
	}

	// Check if turn is complete (only transition from Watching to avoid duplicates
	// when a stale poll result arrives after we already transitioned to Input)
	if m.isTurnComplete(result.Items) && result.Status.Phase == workflow.PhaseWaitingForInput && m.state == StateWatching {
		m.stopWatching()
		m.state = StateInput
		m.suggestion = ""

		cmds := []tea.Cmd{m.focusTextarea()}

		// Apply suggestion if already available; otherwise schedule a delayed poll
		if result.Status.Suggestion != "" {
			m.applySuggestion(result.Status.Suggestion)
		} else if !m.config.DisableSuggestions {
			cmds = append(cmds, m.scheduleSuggestionPoll())
		}
		return m, tea.Batch(cmds...)
	}

	// Continue polling
	return m, m.waitForWatchResult()
}

func (m *Model) handleWatchResult(msg WatchResultMsg) (tea.Model, tea.Cmd) {
	result := msg.Result

	if result.Err != nil {
		switch classifyPollError(result.Err) {
		case pollErrorCompleted:
			if m.plannerActive {
				m.stopWatching()
				childWfID := m.workflowID
				return m, queryChildConversationItems(m.client, childWfID)
			}
			m.stopWatching()
			m.appendToViewport("Session ended.\n")
			m.quitting = true
			return m, tea.Quit
		case pollErrorTransient:
			return m, m.waitForWatchResult()
		case pollErrorFatal:
			m.consecutiveErrors++
			if m.consecutiveErrors >= 5 {
				m.stopWatching()
				m.appendToViewport(fmt.Sprintf("Error: %v\n", result.Err))
				m.err = result.Err
				m.quitting = true
				return m, tea.Quit
			}
			return m, m.waitForWatchResult()
		}
	}
	m.consecutiveErrors = 0

	// Handle compaction: reset rendered seq to re-render all items
	if result.Compacted {
		m.lastRenderedSeq = -1
	}

	// Render new items
	m.renderNewItems(result.Items)

	// Update status
	m.spinnerMsg = PhaseMessage(result.Status.Phase, result.Status.ToolsInFlight)
	m.totalTokens = result.Status.TotalTokens
	m.totalCachedTokens = result.Status.TotalCachedTokens
	m.turnCount = result.Status.TurnCount
	if result.Status.WorkerVersion != "" {
		m.workerVersion = result.Status.WorkerVersion
	}
	m.lastPhase = result.Status.Phase

	// Check for plan changes and render
	if planChanged(m.lastRenderedPlan, result.Status.Plan) {
		rendered := m.renderer.RenderPlan(result.Status.Plan)
		if rendered != "" {
			m.appendToViewport(rendered)
		}
		m.lastRenderedPlan = result.Status.Plan
	}

	// Check for approval pending
	if result.Status.Phase == workflow.PhaseApprovalPending &&
		len(result.Status.PendingApprovals) > 0 && m.state == StateWatching {
		if m.autoApprove {
			callIDs := make([]string, len(result.Status.PendingApprovals))
			for i, ap := range result.Status.PendingApprovals {
				callIDs[i] = ap.CallID
			}
			return m, sendApprovalResponseCmd(m.client, m.workflowID, workflow.ApprovalResponse{Approved: callIDs})
		}
		m.stopWatching()
		m.state = StateApproval
		m.pendingApprovals = result.Status.PendingApprovals
		m.appendToViewport(m.renderer.RenderApprovalContext(result.Status.PendingApprovals))
		m.selector = m.buildApprovalSelector(result.Status.PendingApprovals)
		return m, nil
	}

	// Check for escalation pending
	if result.Status.Phase == workflow.PhaseEscalationPending &&
		len(result.Status.PendingEscalations) > 0 && m.state == StateWatching {
		m.stopWatching()
		m.state = StateEscalation
		m.pendingEscalations = result.Status.PendingEscalations
		m.appendToViewport(m.renderer.RenderEscalationContext(result.Status.PendingEscalations))
		m.selector = m.buildEscalationSelector()
		return m, nil
	}

	// Check for user input question pending
	if result.Status.Phase == workflow.PhaseUserInputPending &&
		result.Status.PendingUserInputRequest != nil && m.state == StateWatching {
		m.stopWatching()
		m.state = StateUserInputQuestion
		m.pendingUserInputReq = result.Status.PendingUserInputRequest
		sel := m.buildUserInputSelector(result.Status.PendingUserInputRequest)
		if sel != nil {
			m.appendToViewport(m.renderer.RenderUserInputQuestionContext(result.Status.PendingUserInputRequest))
			m.selector = sel
			return m, nil
		}
		m.appendToViewport(m.renderer.RenderUserInputQuestionPrompt(result.Status.PendingUserInputRequest))
		return m, m.focusTextarea()
	}

	// Check if completed
	if result.Completed {
		m.stopWatching()
		if m.plannerActive {
			childWfID := m.workflowID
			return m, queryChildConversationItems(m.client, childWfID)
		}
		m.appendToViewport("Session ended.\n")
		m.quitting = true
		return m, tea.Quit
	}

	// Check if turn is complete
	if m.isTurnComplete(result.Items) && result.Status.Phase == workflow.PhaseWaitingForInput && m.state == StateWatching {
		m.stopWatching()
		m.state = StateInput
		m.suggestion = ""

		cmds := []tea.Cmd{m.focusTextarea()}

		if result.Status.Suggestion != "" {
			m.applySuggestion(result.Status.Suggestion)
		} else if !m.config.DisableSuggestions {
			cmds = append(cmds, m.scheduleSuggestionPoll())
		}
		return m, tea.Batch(cmds...)
	}

	// Continue watching
	return m, m.waitForWatchResult()
}

func (m *Model) renderNewItems(items []models.ConversationItem) {
	for _, item := range items {
		if item.Seq <= m.lastRenderedSeq {
			continue
		}
		rendered := m.renderer.RenderItem(item, false)
		if rendered != "" {
			m.appendToViewport(rendered)
		}
		m.lastRenderedSeq = item.Seq
	}
}

func (m *Model) isTurnComplete(items []models.ConversationItem) bool {
	for _, item := range items {
		if item.Seq <= m.lastRenderedSeq-1 {
			continue
		}
		if item.Type == models.ItemTypeTurnComplete {
			return true
		}
	}
	return false
}

func (m *Model) handlePlanRequestAccepted(msg PlanRequestAcceptedMsg) (tea.Model, tea.Cmd) {
	// Save parent workflow ID and switch to planner child
	m.parentWorkflowID = m.workflowID
	m.plannerAgentID = msg.AgentID
	m.plannerActive = true

	// Switch to the planner child's workflow ID
	m.workflowID = msg.WorkflowID
	m.lastRenderedSeq = -1

	m.appendToViewport(m.renderer.RenderSystemMessage(
		fmt.Sprintf("Plan mode active (agent: %s). Use /done to finish.", msg.AgentID)))

	m.state = StateWatching
	m.spinnerMsg = "Planning..."
	return m, m.startWatching()
}

func (m *Model) handlePlannerCompleted(msg PlannerCompletedMsg) (tea.Model, tea.Cmd) {
	// Switch back to parent workflow
	m.workflowID = m.parentWorkflowID
	m.parentWorkflowID = ""
	m.plannerAgentID = ""
	m.plannerActive = false
	m.lastRenderedSeq = -1

	if msg.PlanText != "" {
		m.appendToViewport(m.renderer.RenderSystemMessage("Plan mode ended. Sending plan to parent..."))
		// Send the plan as user input to the parent workflow
		planInput := "Implement the following plan:\n\n" + msg.PlanText
		m.state = StateWatching
		m.spinnerMsg = "Thinking..."
		return m, sendUserInputCmd(m.client, m.workflowID, planInput)
	}

	m.appendToViewport(m.renderer.RenderSystemMessage("Plan mode ended (no plan produced)."))
	m.state = StateInput
	return m, m.focusTextarea()
}

func (m *Model) appendToViewport(content string) {
	wasAtBottom := m.viewport.AtBottom()

	if m.viewportContent != "" {
		m.viewportContent += content
	} else {
		m.viewportContent = content
	}
	m.viewport.SetContent(m.viewportContent)

	if wasAtBottom || !m.ready {
		m.viewport.GotoBottom()
	}
}

// focusTextarea safely focuses the textarea and returns a blink command.
// In test environments where the cursor context isn't available, this recovers
// from panics gracefully.
func (m *Model) focusTextarea() tea.Cmd {
	defer func() { recover() }()
	m.textarea.Focus()
	return textarea.Blink
}

func (m *Model) startWatching() tea.Cmd {
	m.stopWatching()

	if m.client == nil {
		return nil // No client (test mode) — skip watching
	}

	var watchCtx context.Context
	watchCtx, m.watchCancel = context.WithCancel(context.Background())

	watcher := NewWatcher(m.client, m.workflowID)
	go watcher.RunWatching(watchCtx, m.watchCh, m.lastRenderedSeq, m.lastPhase)

	return m.waitForWatchResult()
}

func (m *Model) waitForWatchResult() tea.Cmd {
	ch := m.watchCh
	return func() tea.Msg {
		result, ok := <-ch
		if !ok {
			return SessionCompletedMsg{}
		}
		return WatchResultMsg{Result: result}
	}
}

func (m *Model) stopWatching() {
	if m.watchCancel != nil {
		m.watchCancel()
		m.watchCancel = nil
	}
}

// calculateTextareaHeight returns the appropriate height for the textarea
// based on the number of lines in the current content.
func (m *Model) calculateTextareaHeight() int {
	value := m.textarea.Value()
	lines := strings.Count(value, "\n") + 1
	
	// Minimum 3 lines for initial display, maximum MaxTextareaHeight
	if lines < 1 {
		lines = 1
	}
	if lines > MaxTextareaHeight {
		lines = MaxTextareaHeight
	}
	
	return lines
}

// expandPastedContent replaces the "[N lines pasted]" placeholder in the
// textarea value with the actual buffered paste content before submission.
func (m *Model) expandPastedContent(value string) string {
	if m.pastedContent != "" && m.pasteLabel != "" {
		return strings.Replace(value, m.pasteLabel, m.pastedContent, 1)
	}
	return value
}

// buildApprovalSelector creates a selector for approval prompts.
func (m *Model) buildApprovalSelector(approvals []workflow.PendingApproval) *SelectorModel {
	options := []SelectorOption{
		{Label: "Yes, allow", Shortcut: "y", ShortcutKey: 'y'},
		{Label: "No, deny", Shortcut: "n", ShortcutKey: 'n'},
		{Label: "Always allow for this session", Shortcut: "a", ShortcutKey: 'a'},
	}
	if len(approvals) > 1 {
		options = append(options, SelectorOption{
			Label:       "Select individually...",
			Shortcut:    "s",
			ShortcutKey: 's',
		})
	}
	sel := NewSelectorModel(options, m.styles)
	sel.SetWidth(m.width)
	return sel
}

// buildEscalationSelector creates a selector for escalation prompts.
func (m *Model) buildEscalationSelector() *SelectorModel {
	options := []SelectorOption{
		{Label: "Yes, re-run without sandbox", Shortcut: "y", ShortcutKey: 'y'},
		{Label: "No, deny", Shortcut: "n", ShortcutKey: 'n'},
	}
	sel := NewSelectorModel(options, m.styles)
	sel.SetWidth(m.width)
	return sel
}

// buildUserInputSelector creates a selector for single-question user input prompts.
// Returns nil for multi-question requests (fall back to textarea).
func (m *Model) buildUserInputSelector(req *workflow.PendingUserInputRequest) *SelectorModel {
	if req == nil || len(req.Questions) != 1 {
		return nil
	}
	q := req.Questions[0]
	var options []SelectorOption
	for _, opt := range q.Options {
		options = append(options, SelectorOption{
			Label: opt.Label,
		})
	}
	options = append(options, SelectorOption{
		Label:       "Other (type your answer)...",
		Shortcut:    "o",
		ShortcutKey: 'o',
	})
	sel := NewSelectorModel(options, m.styles)
	sel.SetWidth(m.width)
	return sel
}

// buildSessionSelector creates the session picker selector.
// The first option is always "New session"; subsequent options are existing sessions.
func (m *Model) buildSessionSelector(entries []SessionListEntry) *SelectorModel {
	opts := []SelectorOption{
		{Label: "New session", Shortcut: "n", ShortcutKey: 'n'},
	}
	for _, e := range entries {
		// Extract the last path segment (e.g. "sess-20260219-150405-1")
		short := e.WorkflowID
		if idx := strings.LastIndex(short, "/"); idx >= 0 {
			short = short[idx+1:]
		}
		icon := sessionStatusIcon(e.Status)
		label := fmt.Sprintf("%-32s %s %-10s  %s",
			short, icon, e.Status, e.StartTime.Local().Format("Jan 02, 15:04"))
		opts = append(opts, SelectorOption{Label: label})
	}
	sel := NewSelectorModel(opts, m.styles)
	sel.SetWidth(m.width)
	return sel
}

// sessionStatusIcon returns a Unicode bullet/symbol for a session status string.
func sessionStatusIcon(status string) string {
	switch status {
	case "running":
		return "●"
	case "completed":
		return "✓"
	case "failed":
		return "✗"
	case "canceled":
		return "○"
	case "timed_out":
		return "⏱"
	default:
		return "?"
	}
}

// isViewportScrollKey returns true for keys that should scroll the viewport
// even when the selector is active. Only page/home/end keys, not up/down/j/k.
func (m *Model) isViewportScrollKey(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
		return true
	}
	return false
}

// inputAreaHeight returns the height of the current input area (selector or textarea).
func (m *Model) inputAreaHeight() int {
	if m.selector != nil {
		return m.selector.Height()
	}
	return m.calculateTextareaHeight()
}

// applySuggestion sets the suggestion and updates the textarea placeholder.
func (m *Model) applySuggestion(suggestion string) {
	m.suggestion = suggestion
	if suggestion != "" {
		m.textarea.Placeholder = suggestion
	}
}

// clearSuggestion resets the suggestion and restores the default placeholder.
func (m *Model) clearSuggestion() {
	m.suggestion = ""
	m.textarea.Placeholder = "Type a message..."
}

// scheduleSuggestionPoll returns a tea.Cmd that waits 500ms, then queries the
// workflow for the suggestion field. This handles the case where the suggestion
// isn't ready yet when the turn completes.
func (m *Model) scheduleSuggestionPoll() tea.Cmd {
	c := m.client
	wfID := m.workflowID
	return func() tea.Msg {
		time.Sleep(500 * time.Millisecond)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		resp, err := c.QueryWorkflow(ctx, wfID, "", workflow.QueryGetTurnStatus)
		if err != nil {
			return SuggestionPollMsg{}
		}

		var status workflow.TurnStatus
		if err := resp.Get(&status); err != nil {
			return SuggestionPollMsg{}
		}

		return SuggestionPollMsg{Suggestion: status.Suggestion}
	}
}

// handleSuggestionPoll processes the delayed suggestion poll result.
func (m *Model) handleSuggestionPoll(msg SuggestionPollMsg) (tea.Model, tea.Cmd) {
	// Only apply if we're still in input state with no text typed yet
	if m.state == StateInput && m.textarea.Value() == "" && msg.Suggestion != "" {
		m.applySuggestion(msg.Suggestion)
	}
	return m, nil
}

// planChanged reports whether the plan has changed between old and new.
func planChanged(old, new *workflow.PlanState) bool {
	if old == nil && new == nil {
		return false
	}
	if old == nil || new == nil {
		return true
	}
	if old.Explanation != new.Explanation {
		return true
	}
	if len(old.Steps) != len(new.Steps) {
		return true
	}
	for i := range old.Steps {
		if old.Steps[i].Step != new.Steps[i].Step || old.Steps[i].Status != new.Steps[i].Status {
			return true
		}
	}
	return false
}

// Run is the main entry point for the CLI.
func Run(config Config) error {
	// Create Temporal client
	clientOpts, err := temporalclient.LoadClientOptions(config.TemporalHost, "")
	if err != nil {
		return fmt.Errorf("failed to load Temporal client config: %w", err)
	}
	c, err := client.Dial(clientOpts)
	if err != nil {
		return fmt.Errorf("failed to connect to Temporal: %w", err)
	}
	defer c.Close()

	model := NewModel(config, c)

	var opts []tea.ProgramOption
	if !config.Inline {
		opts = append(opts, tea.WithAltScreen())
	}
	p := tea.NewProgram(model, opts...)

	// Enable CSI 1007 alternate scroll mode: the terminal translates mouse
	// wheel events into arrow key sequences. This gives us wheel scrolling
	// without capturing the mouse, so normal text selection keeps working.
	fmt.Fprint(os.Stderr, "\x1b[?1007h")
	defer fmt.Fprint(os.Stderr, "\x1b[?1007l")

	finalModel, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	// Print resume hint after exiting TUI
	fm := finalModel.(*Model)
	if fm.workflowID != "" && fm.err == nil {
		fmt.Fprintf(os.Stderr, "\nSession suspended. Run tcx to resume from the session picker.\n")
	}

	if fm.err != nil {
		return fm.err
	}
	return nil
}
