package cli

import (
	"bytes"
	"testing"
)

// TestSpinner_RapidStartStop cycles Start/Stop rapidly under -race to detect
// goroutine overlap or panics from closing an already-closed channel.
func TestSpinner_RapidStartStop(t *testing.T) {
	var buf bytes.Buffer
	sp := NewSpinner(&buf)

	for i := 0; i < 100; i++ {
		sp.Start("msg")
		sp.Stop()
	}
}

// TestSpinner_StopWithoutStart is a no-op — should not panic.
func TestSpinner_StopWithoutStart(t *testing.T) {
	var buf bytes.Buffer
	sp := NewSpinner(&buf)
	sp.Stop() // should be safe
}

// TestSpinner_DoubleStop should not panic.
func TestSpinner_DoubleStop(t *testing.T) {
	var buf bytes.Buffer
	sp := NewSpinner(&buf)
	sp.Start("test")
	sp.Stop()
	sp.Stop() // second stop should be safe
}

// TestSpinner_SetMessageWhileRunning should not race.
func TestSpinner_SetMessageWhileRunning(t *testing.T) {
	var buf bytes.Buffer
	sp := NewSpinner(&buf)
	sp.Start("initial")
	sp.SetMessage("updated")
	sp.SetMessage("updated again")
	sp.Stop()
}

// TestSpinner_StartWhileActive updates message without restarting goroutine.
func TestSpinner_StartWhileActive(t *testing.T) {
	var buf bytes.Buffer
	sp := NewSpinner(&buf)
	sp.Start("first")
	sp.Start("second") // should update message, not start new goroutine
	sp.Stop()
}
