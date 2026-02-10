package cli

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/mfateev/codex-temporal-go/internal/workflow"
)

// Spinner frames for the animated spinner.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner renders an animated status line during active turns.
type Spinner struct {
	out     io.Writer
	mu      sync.Mutex
	wg      sync.WaitGroup // tracks run() goroutine lifetime
	message string
	active  bool
	stopCh  chan struct{}
	frame   int
}

// NewSpinner creates a new spinner that writes to the given writer.
func NewSpinner(out io.Writer) *Spinner {
	return &Spinner{
		out:    out,
		stopCh: make(chan struct{}),
	}
}

// Start begins the spinner animation with the given message.
func (sp *Spinner) Start(message string) {
	sp.mu.Lock()
	if sp.active {
		sp.mu.Unlock()
		sp.SetMessage(message)
		return
	}
	sp.active = true
	sp.message = message
	sp.stopCh = make(chan struct{})
	sp.wg.Add(1)
	sp.mu.Unlock()

	go sp.run()
}

// SetMessage updates the spinner message without stopping.
func (sp *Spinner) SetMessage(message string) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.message = message
}

// Stop stops the spinner and clears the line.
func (sp *Spinner) Stop() {
	sp.mu.Lock()
	if !sp.active {
		sp.mu.Unlock()
		return
	}
	sp.active = false
	close(sp.stopCh)
	sp.mu.Unlock()

	sp.wg.Wait() // wait for run() goroutine to exit

	// Clear the spinner line
	fmt.Fprintf(sp.out, "\r\033[K")
}

func (sp *Spinner) run() {
	defer sp.wg.Done()
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-sp.stopCh:
			return
		case <-ticker.C:
			sp.mu.Lock()
			msg := sp.message
			frame := spinnerFrames[sp.frame%len(spinnerFrames)]
			sp.frame++
			sp.mu.Unlock()

			fmt.Fprintf(sp.out, "\r\033[K%s %s", frame, msg)
		}
	}
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
