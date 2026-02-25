package cli

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func TestBuildReviewMessage_WithDiff(t *testing.T) {
	diff := "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new"
	msg := buildReviewMessage(diff)
	assert.Contains(t, msg, "Review the following code changes")
	assert.Contains(t, msg, diff)
}

func TestBuildReviewMessage_EmptyDiff(t *testing.T) {
	assert.Equal(t, "", buildReviewMessage(""))
}

func TestBuildReviewMessage_NoChanges(t *testing.T) {
	assert.Equal(t, "", buildReviewMessage("No changes detected."))
}

func TestBuildReviewMessage_NotGitRepo(t *testing.T) {
	assert.Equal(t, "", buildReviewMessage("Not in a git repository."))
}

func TestModel_ReviewCommand_NoSession(t *testing.T) {
	m := newTestModel()
	m.workflowID = "" // No active session

	m.textarea.SetValue("/review")
	result, _ := m.handleInputKey(tea.KeyMsg{Type: tea.KeyEnter})
	rm := result.(*Model)
	assert.Equal(t, StateInput, rm.state)
	assert.Contains(t, rm.viewportContent, "No active session")
}

func TestModel_ReviewCommand_WithSession(t *testing.T) {
	m := newTestModel()
	m.workflowID = "test-wf"

	m.textarea.SetValue("/review")
	_, cmd := m.handleInputKey(tea.KeyMsg{Type: tea.KeyEnter})
	// Should return a command (runGitDiffCmd wrapped for review)
	assert.NotNil(t, cmd)
}
