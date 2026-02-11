package cli

import (
	"fmt"
	"regexp"
	"strings"
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
	assert.Contains(t, result, "Ran")
	assert.Contains(t, result, "echo hello")
	assert.Contains(t, result, "•")
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
	assert.Contains(t, result, "└")
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
		longContent += fmt.Sprintf("line %d\n", i+1)
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

	// Middle truncation: first 2 lines + ellipsis + last 2 lines
	assert.Contains(t, result, "line 1")
	assert.Contains(t, result, "line 2")
	assert.Contains(t, result, "… +21 lines")
	assert.Contains(t, result, "line 24")
	assert.Contains(t, result, "line 25")
	assert.NotContains(t, result, "line 10")
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

	plain := stripANSI(result)
	// Verify the content is present in Codex format
	assert.Contains(t, plain, "Ran")
	assert.Contains(t, plain, "ls")
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

func TestItemRenderer_RenderFunctionCall_ReadFile(t *testing.T) {
	r := newTestRenderer()
	result := r.RenderItem(models.ConversationItem{
		Type:      models.ItemTypeFunctionCall,
		Name:      "read_file",
		Arguments: `{"file_path": "/tmp/foo.txt"}`,
	}, false)

	assert.Contains(t, result, "•")
	assert.Contains(t, result, "Read")
	assert.Contains(t, result, "/tmp/foo.txt")
}

func TestItemRenderer_RenderFunctionCall_WriteFile(t *testing.T) {
	r := newTestRenderer()
	result := r.RenderItem(models.ConversationItem{
		Type:      models.ItemTypeFunctionCall,
		Name:      "write_file",
		Arguments: `{"file_path": "/tmp/bar.txt", "content": "hello"}`,
	}, false)

	assert.Contains(t, result, "•")
	assert.Contains(t, result, "Wrote")
	assert.Contains(t, result, "/tmp/bar.txt")
}

func TestItemRenderer_RenderFunctionCall_ApplyPatch(t *testing.T) {
	r := newTestRenderer()
	result := r.RenderItem(models.ConversationItem{
		Type:      models.ItemTypeFunctionCall,
		Name:      "apply_patch",
		Arguments: `{"file_path": "/tmp/foo.go", "patch": "..."}`,
	}, false)

	assert.Contains(t, result, "•")
	assert.Contains(t, result, "Patched")
}

func TestItemRenderer_RenderFunctionCall_ListDir(t *testing.T) {
	r := newTestRenderer()
	result := r.RenderItem(models.ConversationItem{
		Type:      models.ItemTypeFunctionCall,
		Name:      "list_dir",
		Arguments: `{"dir_path": "/tmp"}`,
	}, false)

	assert.Contains(t, result, "•")
	assert.Contains(t, result, "Listed")
	assert.Contains(t, result, "/tmp")
}

func TestItemRenderer_RenderFunctionCall_GrepFiles(t *testing.T) {
	r := newTestRenderer()
	result := r.RenderItem(models.ConversationItem{
		Type:      models.ItemTypeFunctionCall,
		Name:      "grep_files",
		Arguments: `{"pattern": "TODO", "path": "src/"}`,
	}, false)

	assert.Contains(t, result, "•")
	assert.Contains(t, result, "Searched")
	assert.Contains(t, result, `"TODO"`)
	assert.Contains(t, result, "in src/")
}

func TestItemRenderer_RenderFunctionCall_Unknown(t *testing.T) {
	r := newTestRenderer()
	result := r.RenderItem(models.ConversationItem{
		Type:      models.ItemTypeFunctionCall,
		Name:      "custom_tool",
		Arguments: `{"foo": "bar"}`,
	}, false)

	assert.Contains(t, result, "•")
	assert.Contains(t, result, "Ran")
	assert.Contains(t, result, "custom_tool")
}

func TestItemRenderer_RenderFunctionCallOutput_Empty(t *testing.T) {
	r := newTestRenderer()
	success := true
	result := r.RenderItem(models.ConversationItem{
		Type:   models.ItemTypeFunctionCallOutput,
		CallID: "call-1",
		Output: &models.FunctionCallOutputPayload{
			Content: "",
			Success: &success,
		},
	}, false)

	assert.Contains(t, result, "└")
	assert.Contains(t, result, "(no output)")
}

func TestItemRenderer_MiddleTruncation(t *testing.T) {
	r := newTestRenderer()

	// Build 10 distinct lines
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, fmt.Sprintf("output line %d", i))
	}

	success := true
	result := r.RenderItem(models.ConversationItem{
		Type:   models.ItemTypeFunctionCallOutput,
		CallID: "call-1",
		Output: &models.FunctionCallOutputPayload{
			Content: strings.Join(lines, "\n"),
			Success: &success,
		},
	}, false)

	// Should show first 2 lines
	assert.Contains(t, result, "output line 1")
	assert.Contains(t, result, "output line 2")
	// Should show ellipsis with count
	assert.Contains(t, result, "… +6 lines")
	// Should show last 2 lines
	assert.Contains(t, result, "output line 9")
	assert.Contains(t, result, "output line 10")
	// Should NOT show middle lines
	assert.NotContains(t, result, "output line 5")
}

func TestItemRenderer_OutputExactly5Lines(t *testing.T) {
	r := newTestRenderer()

	var lines []string
	for i := 1; i <= 5; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}

	success := true
	result := r.RenderItem(models.ConversationItem{
		Type:   models.ItemTypeFunctionCallOutput,
		CallID: "call-1",
		Output: &models.FunctionCallOutputPayload{
			Content: strings.Join(lines, "\n"),
			Success: &success,
		},
	}, false)

	// All 5 lines should be shown without truncation
	for i := 1; i <= 5; i++ {
		assert.Contains(t, result, fmt.Sprintf("line %d", i))
	}
	assert.NotContains(t, result, "… +")
}

func TestTruncateMiddle(t *testing.T) {
	tests := []struct {
		name        string
		input       []string
		limit       int
		wantLen     int
		wantOmitted int
	}{
		{
			name:        "under limit",
			input:       []string{"a", "b", "c"},
			limit:       5,
			wantLen:     3,
			wantOmitted: 0,
		},
		{
			name:        "at limit",
			input:       []string{"a", "b", "c", "d", "e"},
			limit:       5,
			wantLen:     5,
			wantOmitted: 0,
		},
		{
			name:        "over limit",
			input:       []string{"a", "b", "c", "d", "e", "f", "g", "h"},
			limit:       5,
			wantLen:     5, // 2 head + 1 ellipsis + 2 tail
			wantOmitted: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, omitted := truncateMiddle(tt.input, tt.limit)
			assert.Equal(t, tt.wantLen, len(result))
			assert.Equal(t, tt.wantOmitted, omitted)
			if omitted > 0 {
				assert.Contains(t, result[2], "… +")
			}
		})
	}
}

func TestFormatToolCall(t *testing.T) {
	tests := []struct {
		name       string
		toolName   string
		argsJSON   string
		wantVerb   string
		wantDetail string
	}{
		{"shell", "shell", `{"command": "echo hello"}`, "Ran", "echo hello"},
		{"read_file", "read_file", `{"file_path": "/tmp/foo.txt"}`, "Read", "/tmp/foo.txt"},
		{"write_file", "write_file", `{"file_path": "/tmp/bar.txt"}`, "Wrote", "/tmp/bar.txt"},
		{"apply_patch", "apply_patch", `{"file_path": "/tmp/x.go"}`, "Patched", ""},
		{"list_dir", "list_dir", `{"dir_path": "/tmp"}`, "Listed", "/tmp"},
		{"grep_files", "grep_files", `{"pattern": "TODO", "path": "src/"}`, "Searched", `"TODO" in src/`},
		{"unknown", "my_tool", `{"x": 1}`, "Ran", `my_tool({"x": 1})`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verb, detail := formatToolCall(tt.toolName, tt.argsJSON)
			assert.Equal(t, tt.wantVerb, verb)
			assert.Equal(t, tt.wantDetail, detail)
		})
	}
}
