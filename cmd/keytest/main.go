// Throwaway POC to see what bubbletea receives for Shift+Enter vs Enter.
// Run: go run cmd/keytest/main.go
// Then press Enter, Shift+Enter, Ctrl+J, Alt+Enter, etc.
package main

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type model struct {
	log []string
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		desc := fmt.Sprintf("Type=%d(%s) String=%q Runes=%v Alt=%v Paste=%v",
			msg.Type, keyTypeName(msg.Type), msg.String(), msg.Runes, msg.Alt, msg.Paste)
		m.log = append(m.log, desc)

		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString("Press keys to see what bubbletea receives. Ctrl+C to quit.\n\n")

	start := 0
	if len(m.log) > 20 {
		start = len(m.log) - 20
	}
	for _, line := range m.log[start:] {
		b.WriteString("  " + line + "\n")
	}
	return b.String()
}

func keyTypeName(t tea.KeyType) string {
	names := map[tea.KeyType]string{
		tea.KeyEnter:    "KeyEnter/KeyCtrlM",
		tea.KeyTab:      "KeyTab",
		tea.KeyEsc:      "KeyEsc",
		tea.KeySpace:    "KeySpace",
		tea.KeyRunes:    "KeyRunes",
		tea.KeyUp:       "KeyUp",
		tea.KeyDown:     "KeyDown",
		tea.KeyCtrlC:    "KeyCtrlC",
		tea.KeyCtrlJ:    "KeyCtrlJ",
		tea.KeyShiftTab: "KeyShiftTab",
	}
	if name, ok := names[t]; ok {
		return name
	}
	return fmt.Sprintf("KeyType(%d)", t)
}

func main() {
	p := tea.NewProgram(model{}, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
