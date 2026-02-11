package cli

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func testSelectorOptions() []SelectorOption {
	return []SelectorOption{
		{Label: "Yes, allow", Shortcut: "y", ShortcutKey: 'y'},
		{Label: "No, deny", Shortcut: "n", ShortcutKey: 'n'},
		{Label: "Always allow", Shortcut: "a", ShortcutKey: 'a'},
	}
}

func newTestSelector() *SelectorModel {
	return NewSelectorModel(testSelectorOptions(), NoColorStyles())
}

func TestSelector_InitialState(t *testing.T) {
	s := newTestSelector()
	assert.Equal(t, 0, s.Selected())
	assert.False(t, s.Confirmed())
	assert.False(t, s.Cancelled())
	assert.Equal(t, 3, s.Height())
}

func TestSelector_MoveDown(t *testing.T) {
	s := newTestSelector()

	done := s.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.False(t, done)
	assert.Equal(t, 1, s.Selected())

	s.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.Equal(t, 2, s.Selected())

	// Wrap around
	s.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.Equal(t, 0, s.Selected())
}

func TestSelector_MoveUp(t *testing.T) {
	s := newTestSelector()

	// Wrap around from 0
	done := s.Update(tea.KeyMsg{Type: tea.KeyUp})
	assert.False(t, done)
	assert.Equal(t, 2, s.Selected())

	s.Update(tea.KeyMsg{Type: tea.KeyUp})
	assert.Equal(t, 1, s.Selected())

	s.Update(tea.KeyMsg{Type: tea.KeyUp})
	assert.Equal(t, 0, s.Selected())
}

func TestSelector_JKNavigation(t *testing.T) {
	s := newTestSelector()

	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	assert.Equal(t, 1, s.Selected())

	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	assert.Equal(t, 0, s.Selected())
}

func TestSelector_EnterConfirms(t *testing.T) {
	s := newTestSelector()

	// Move to second option
	s.Update(tea.KeyMsg{Type: tea.KeyDown})

	done := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, done)
	assert.True(t, s.Confirmed())
	assert.False(t, s.Cancelled())
	assert.Equal(t, 1, s.Selected())
}

func TestSelector_EscCancels(t *testing.T) {
	s := newTestSelector()

	done := s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.True(t, done)
	assert.True(t, s.Cancelled())
	assert.False(t, s.Confirmed())
}

func TestSelector_NumberKeyJumpsAndConfirms(t *testing.T) {
	s := newTestSelector()

	done := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	assert.True(t, done)
	assert.True(t, s.Confirmed())
	assert.Equal(t, 1, s.Selected()) // '2' maps to index 1
}

func TestSelector_NumberKeyOutOfRange(t *testing.T) {
	s := newTestSelector()

	done := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'9'}})
	assert.False(t, done)
	assert.False(t, s.Confirmed())
	assert.Equal(t, 0, s.Selected()) // cursor unchanged
}

func TestSelector_ShortcutKeyConfirms(t *testing.T) {
	s := newTestSelector()

	done := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	assert.True(t, done)
	assert.True(t, s.Confirmed())
	assert.Equal(t, 2, s.Selected()) // 'a' is the third option
}

func TestSelector_ShortcutKeyCaseInsensitive(t *testing.T) {
	s := newTestSelector()

	done := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	assert.True(t, done)
	assert.True(t, s.Confirmed())
	assert.Equal(t, 0, s.Selected())
}

func TestSelector_ShortcutKeyNoMatch(t *testing.T) {
	s := newTestSelector()

	done := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	assert.False(t, done)
	assert.False(t, s.Confirmed())
}

func TestSelector_ViewRendering(t *testing.T) {
	s := newTestSelector()
	view := s.View()

	// Should contain all labels
	assert.Contains(t, view, "Yes, allow")
	assert.Contains(t, view, "No, deny")
	assert.Contains(t, view, "Always allow")

	// Should contain number prefixes
	assert.Contains(t, view, "1.")
	assert.Contains(t, view, "2.")
	assert.Contains(t, view, "3.")

	// Should contain shortcut hints
	assert.Contains(t, view, "(y)")
	assert.Contains(t, view, "(n)")
	assert.Contains(t, view, "(a)")

	// Should have chevron on first item
	assert.Contains(t, view, ">")
}

func TestSelector_ViewRenderingSelectedItem(t *testing.T) {
	s := newTestSelector()
	s.Update(tea.KeyMsg{Type: tea.KeyDown}) // cursor to index 1

	view := s.View()
	lines := strings.Split(view, "\n")
	assert.Equal(t, 3, len(lines))

	// First line should NOT have chevron
	assert.NotContains(t, lines[0], ">")
	// Second line SHOULD have chevron
	assert.Contains(t, lines[1], ">")
}

func TestSelector_Height(t *testing.T) {
	s := newTestSelector()
	assert.Equal(t, 3, s.Height())

	s2 := NewSelectorModel([]SelectorOption{
		{Label: "A"},
		{Label: "B"},
	}, NoColorStyles())
	assert.Equal(t, 2, s2.Height())
}

func TestSelector_EmptyOptions(t *testing.T) {
	s := NewSelectorModel(nil, NoColorStyles())
	assert.Equal(t, "", s.View())
	assert.Equal(t, 0, s.Height())
}

func TestSelector_SetWidth(t *testing.T) {
	s := newTestSelector()
	s.SetWidth(120)
	assert.Equal(t, 120, s.width)
}
