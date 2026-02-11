package cli

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

// SelectorOption represents a single selectable option in the selector.
type SelectorOption struct {
	Label       string // Display text, e.g. "Yes, allow"
	Shortcut    string // Displayed shortcut hint, e.g. "y"
	ShortcutKey rune   // Matched against keypress, e.g. 'y'
	Description string // Optional secondary text (unused for now)
}

// SelectorModel is a lightweight bubbletea sub-model for arrow-key navigable
// option selection. Designed for 2-9 options in approval/escalation prompts.
type SelectorModel struct {
	options   []SelectorOption
	cursor    int
	width     int
	styles    Styles
	confirmed bool
	cancelled bool
}

// NewSelectorModel creates a new selector with the given options and styles.
func NewSelectorModel(options []SelectorOption, styles Styles) *SelectorModel {
	return &SelectorModel{
		options: options,
		styles:  styles,
	}
}

// Update processes a key message and returns whether the selector is done
// (confirmed or cancelled).
func (s *SelectorModel) Update(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyUp:
		s.moveUp()
		return false
	case tea.KeyDown:
		s.moveDown()
		return false
	case tea.KeyEnter:
		s.confirmed = true
		return true
	case tea.KeyEsc:
		s.cancelled = true
		return true
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			r := msg.Runes[0]

			// Check number keys 1-9
			if r >= '1' && r <= '9' {
				idx := int(r - '1')
				if idx < len(s.options) {
					s.cursor = idx
					s.confirmed = true
					return true
				}
				return false // out of range, ignore
			}

			// Check j/k navigation
			if r == 'j' {
				s.moveDown()
				return false
			}
			if r == 'k' {
				s.moveUp()
				return false
			}

			// Check shortcut keys (case-insensitive)
			lower := unicode.ToLower(r)
			for i, opt := range s.options {
				if opt.ShortcutKey != 0 && unicode.ToLower(opt.ShortcutKey) == lower {
					s.cursor = i
					s.confirmed = true
					return true
				}
			}
		}
	}

	return false
}

// View renders the selector as a string.
func (s *SelectorModel) View() string {
	if len(s.options) == 0 {
		return ""
	}

	var b strings.Builder
	for i, opt := range s.options {
		// Chevron indicator
		var chevron string
		if i == s.cursor {
			chevron = s.styles.SelectorChevron.Render(" > ")
		} else {
			chevron = "   "
		}

		// Number prefix
		num := fmt.Sprintf("%d. ", i+1)

		// Label (highlighted if selected)
		var label string
		if i == s.cursor {
			label = s.styles.SelectorSelected.Render(num + opt.Label)
		} else {
			label = num + opt.Label
		}

		// Shortcut hint
		var shortcut string
		if opt.Shortcut != "" {
			shortcut = " " + s.styles.SelectorShortcut.Render("("+opt.Shortcut+")")
		}

		b.WriteString(chevron + label + shortcut)
		if i < len(s.options)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

// Selected returns the index of the currently selected option.
func (s *SelectorModel) Selected() int {
	return s.cursor
}

// Confirmed returns whether the user confirmed a selection.
func (s *SelectorModel) Confirmed() bool {
	return s.confirmed
}

// Cancelled returns whether the user cancelled (Esc).
func (s *SelectorModel) Cancelled() bool {
	return s.cancelled
}

// SetWidth sets the available width for rendering.
func (s *SelectorModel) SetWidth(w int) {
	s.width = w
}

// Height returns the number of lines the selector occupies.
func (s *SelectorModel) Height() int {
	return len(s.options)
}

func (s *SelectorModel) moveUp() {
	s.cursor--
	if s.cursor < 0 {
		s.cursor = len(s.options) - 1
	}
}

func (s *SelectorModel) moveDown() {
	s.cursor++
	if s.cursor >= len(s.options) {
		s.cursor = 0
	}
}
