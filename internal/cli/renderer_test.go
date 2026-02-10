package cli

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/workflow"
)

// stripANSI removes ANSI escape sequences from a string for test assertions.
var ansiRegexp = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiRegexp.ReplaceAllString(s, "")
}

func newTestRenderer() *ItemRenderer {
	return NewItemRenderer(80, true, true, NoColorStyles()) // noColor=true, noMarkdown=true
}

func TestItemRenderer_RenderAssistantMessage(t *testing.T) {
	r := newTestRenderer()
	result := r.RenderItem(models.ConversationItem{
		Type:    models.ItemTypeAssistantMessage,
		Content: "Hello, world!",
	}, false)

	assert.NotEmpty(t, result)
	assert.Contains(t, result, "Hello, world!")
}

func TestItemRenderer_RenderFunctionCall(t *testing.T) {
	r := newTestRenderer()
	result := r.RenderItem(models.ConversationItem{
		Type:      models.ItemTypeFunctionCall,
		Name:      "shell",
		Arguments: `{"command": "echo hello"}`,
	}, false)

	assert.NotEmpty(t, result)
	assert.Contains(t, result, "shell")
	assert.Contains(t, result, "echo hello")
}

func TestItemRenderer_RenderFunctionCallOutput_Success(t *testing.T) {
	r := newTestRenderer()
	success := true
	result := r.RenderItem(models.ConversationItem{
		Type:   models.ItemTypeFunctionCallOutput,
		CallID: "call-1",
		Output: &models.FunctionCallOutputPayload{
			Content: "hello\n",
			Success: &success,
		},
	}, false)

	assert.NotEmpty(t, result)
	assert.Contains(t, result, "hello")
}

func TestItemRenderer_RenderFunctionCallOutput_Failure(t *testing.T) {
	r := newTestRenderer()
	failure := false
	result := r.RenderItem(models.ConversationItem{
		Type:   models.ItemTypeFunctionCallOutput,
		CallID: "call-1",
		Output: &models.FunctionCallOutputPayload{
			Content: "command not found",
			Success: &failure,
		},
	}, false)

	assert.NotEmpty(t, result)
	assert.Contains(t, result, "command not found")
}

func TestItemRenderer_RenderTurnStarted(t *testing.T) {
	r := newTestRenderer()
	result := r.RenderItem(models.ConversationItem{
		Type:   models.ItemTypeTurnStarted,
		TurnID: "turn-123",
	}, false)

	assert.NotEmpty(t, result)
	assert.Contains(t, result, "turn-123")
}

func TestItemRenderer_TurnCompleteNotRendered(t *testing.T) {
	r := newTestRenderer()
	result := r.RenderItem(models.ConversationItem{
		Type:   models.ItemTypeTurnComplete,
		TurnID: "turn-123",
	}, false)

	assert.Empty(t, result)
}

func TestItemRenderer_UserMessageNotRendered(t *testing.T) {
	r := newTestRenderer()
	result := r.RenderItem(models.ConversationItem{
		Type:    models.ItemTypeUserMessage,
		Content: "Hello",
	}, false)

	assert.Empty(t, result, "User messages should not be rendered during live conversation")
}

func TestItemRenderer_UserMessageRenderedOnResume(t *testing.T) {
	r := newTestRenderer()
	result := r.RenderItem(models.ConversationItem{
		Type:    models.ItemTypeUserMessage,
		Content: "Hello from resume",
	}, true)

	assert.NotEmpty(t, result)
	assert.Contains(t, result, "Hello from resume")
}

func TestItemRenderer_RenderStatusLine(t *testing.T) {
	r := newTestRenderer()
	result := r.RenderStatusLine("gpt-4o-mini", 1234, 3)

	assert.Contains(t, result, "gpt-4o-mini")
	assert.Contains(t, result, "1,234")
	assert.Contains(t, result, "turn 3")
}

func TestItemRenderer_LongOutputTruncated(t *testing.T) {
	r := newTestRenderer()

	longContent := ""
	for i := 0; i < 25; i++ {
		longContent += "line\n"
	}

	success := true
	result := r.RenderItem(models.ConversationItem{
		Type:   models.ItemTypeFunctionCallOutput,
		CallID: "call-1",
		Output: &models.FunctionCallOutputPayload{
			Content: longContent,
			Success: &success,
		},
	}, false)

	assert.Contains(t, result, "more lines")
}

func TestItemRenderer_ColorDisabled(t *testing.T) {
	r := NewItemRenderer(80, true, true, NoColorStyles())
	result := r.RenderItem(models.ConversationItem{
		Type:      models.ItemTypeFunctionCall,
		Name:      "shell",
		Arguments: `{"command": "ls"}`,
	}, false)

	// NoColorStyles should not produce ANSI codes
	assert.NotContains(t, result, "\033[")
}

func TestItemRenderer_ColorEnabled(t *testing.T) {
	r := NewItemRenderer(80, false, false, DefaultStyles())
	result := r.RenderItem(models.ConversationItem{
		Type:      models.ItemTypeFunctionCall,
		Name:      "shell",
		Arguments: `{"command": "ls"}`,
	}, false)

	// Verify the content is present (lipgloss may not emit ANSI in non-TTY test env)
	assert.Contains(t, result, "shell")
	assert.Contains(t, result, "ls")
}

func TestItemRenderer_MarkdownRendersFormattedOutput(t *testing.T) {
	r := NewItemRenderer(80, false, false, DefaultStyles())

	mdContent := "# Heading\n\nSome **bold** text and a list:\n\n- item one\n- item two\n"
	result := r.RenderItem(models.ConversationItem{
		Type:    models.ItemTypeAssistantMessage,
		Content: mdContent,
	}, false)

	plain := stripANSI(result)
	assert.NotEqual(t, "\n"+mdContent+"\n\n", result, "Markdown renderer should transform the content")
	assert.Contains(t, plain, "Heading")
	assert.Contains(t, plain, "item one")
}

func TestItemRenderer_NoMarkdownProducesPlainText(t *testing.T) {
	r := NewItemRenderer(80, true, true, NoColorStyles())

	mdContent := "# Heading\n\nSome **bold** text."
	result := r.RenderItem(models.ConversationItem{
		Type:    models.ItemTypeAssistantMessage,
		Content: mdContent,
	}, false)

	assert.Equal(t, "\n"+mdContent+"\n\n", result)
}

func TestItemRenderer_MarkdownEmptyContent(t *testing.T) {
	r := NewItemRenderer(80, false, false, DefaultStyles())
	result := r.RenderItem(models.ConversationItem{
		Type:    models.ItemTypeAssistantMessage,
		Content: "",
	}, false)

	assert.Empty(t, result)
}

func TestItemRenderer_MarkdownCodeBlockPreserved(t *testing.T) {
	r := NewItemRenderer(80, false, false, DefaultStyles())

	mdContent := "Here is code:\n\n```go\nfmt.Println(\"hello\")\n```\n"
	result := r.RenderItem(models.ConversationItem{
		Type:    models.ItemTypeAssistantMessage,
		Content: mdContent,
	}, false)

	plain := stripANSI(result)
	assert.Contains(t, plain, "hello", "Code block content should be preserved")
	assert.Contains(t, plain, "Println", "Code block content should be preserved")
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{1234, "1,234"},
		{12345, "12,345"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, formatTokens(tt.input))
	}
}

func TestPhaseMessage(t *testing.T) {
	tests := []struct {
		phase         string
		toolsInFlight []string
		expected      string
	}{
		{"llm_calling", nil, "Thinking..."},
		{"tool_executing", []string{"shell"}, "Running shell..."},
		{"tool_executing", nil, "Running tool..."},
		{"waiting_for_input", nil, "Working..."},
	}

	for _, tt := range tests {
		result := PhaseMessage(workflow.TurnPhase(tt.phase), tt.toolsInFlight)
		assert.Equal(t, tt.expected, result)
	}
}

func TestItemRenderer_RenderApprovalPrompt(t *testing.T) {
	r := newTestRenderer()
	result := r.RenderApprovalPrompt([]workflow.PendingApproval{
		{CallID: "c1", ToolName: "shell", Arguments: `{"command": "rm -rf /"}`, Reason: "dangerous"},
		{CallID: "c2", ToolName: "write_file", Arguments: `{"file_path": "/etc/passwd"}`},
	})

	assert.Contains(t, result, "shell")
	assert.Contains(t, result, "write_file")
	assert.Contains(t, result, "dangerous")
	assert.Contains(t, result, "1,2 (select by index)")
}

func TestItemRenderer_RenderApprovalPromptSingle(t *testing.T) {
	r := newTestRenderer()
	result := r.RenderApprovalPrompt([]workflow.PendingApproval{
		{CallID: "c1", ToolName: "shell", Arguments: `{"command": "ls"}`},
	})

	assert.Contains(t, result, "shell")
	assert.NotContains(t, result, "select by index")
}

func TestItemRenderer_RenderEscalationPrompt(t *testing.T) {
	r := newTestRenderer()
	result := r.RenderEscalationPrompt([]workflow.EscalationRequest{
		{CallID: "c1", ToolName: "shell", Arguments: `{"command": "ls"}`, Output: "permission denied"},
	})

	assert.Contains(t, result, "Sandbox failure")
	assert.Contains(t, result, "shell")
	assert.Contains(t, result, "permission denied")
}
