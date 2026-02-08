package llm

import (
	"testing"

	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/openai/openai-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper: determine the role string from a message union by checking which variant is set.
// The SDK's constant types have zero-value strings, so we check variant pointers directly.
func msgRole(t *testing.T, msg openai.ChatCompletionMessageParamUnion) string {
	t.Helper()
	switch {
	case msg.OfSystem != nil:
		return "system"
	case msg.OfDeveloper != nil:
		return "developer"
	case msg.OfUser != nil:
		return "user"
	case msg.OfAssistant != nil:
		return "assistant"
	case msg.OfTool != nil:
		return "tool"
	default:
		t.Fatal("message has no recognized variant set")
		return ""
	}
}

// Helper: extract the string content from a message union.
func msgContent(t *testing.T, msg openai.ChatCompletionMessageParamUnion) string {
	t.Helper()
	c := msg.GetContent().AsAny()
	require.NotNil(t, c, "content must not be nil")
	s, ok := c.(*string)
	require.True(t, ok, "content must be a *string, got %T", c)
	return *s
}

// --- Tests ported from codex-rs/core/tests/suite/client.rs ---

// TestBuildMessages_BaseInstructionsInRequest verifies that base_instructions
// appear as a system message at the start of the messages list.
//
// Maps to: client.rs includes_base_instructions_override_in_request (line 394)
func TestBuildMessages_BaseInstructionsInRequest(t *testing.T) {
	client := &OpenAIClient{}
	request := LLMRequest{
		History: []models.ConversationItem{
			{Type: models.ItemTypeUserMessage, Content: "hello"},
		},
		ModelConfig:      models.DefaultModelConfig(),
		BaseInstructions: "test instructions",
	}

	messages := client.buildMessages(request)

	require.GreaterOrEqual(t, len(messages), 2, "should have system + user messages")

	// First message: system with base instructions
	assert.NotNil(t, messages[0].OfSystem, "first message should be system")
	assert.Equal(t, "system", msgRole(t, messages[0]))
	assert.Contains(t, msgContent(t, messages[0]), "test instructions")

	// Last message: user
	assert.Equal(t, "user", msgRole(t, messages[len(messages)-1]))
}

// TestBuildMessages_UserInstructionsInRequest verifies that user_instructions
// are appended to the system message content.
//
// Maps to: client.rs includes_user_instructions_message_in_request (line 566)
func TestBuildMessages_UserInstructionsInRequest(t *testing.T) {
	client := &OpenAIClient{}
	request := LLMRequest{
		History: []models.ConversationItem{
			{Type: models.ItemTypeUserMessage, Content: "hello"},
		},
		ModelConfig:      models.DefaultModelConfig(),
		UserInstructions: "be nice",
	}

	messages := client.buildMessages(request)

	require.GreaterOrEqual(t, len(messages), 2)

	// System message should contain user instructions
	assert.NotNil(t, messages[0].OfSystem, "first message should be system")
	assert.Contains(t, msgContent(t, messages[0]), "be nice")
}

// TestBuildMessages_DeveloperInstructionsInRequest verifies that developer_instructions
// appear as a developer message, and user_instructions appear in the system message.
//
// Maps to: client.rs includes_developer_instructions_message_in_request (line 1064)
func TestBuildMessages_DeveloperInstructionsInRequest(t *testing.T) {
	client := &OpenAIClient{}
	request := LLMRequest{
		History: []models.ConversationItem{
			{Type: models.ItemTypeUserMessage, Content: "hello"},
		},
		ModelConfig:           models.DefaultModelConfig(),
		BaseInstructions:      "base prompt",
		DeveloperInstructions: "be useful",
		UserInstructions:      "be nice",
	}

	messages := client.buildMessages(request)

	// Expected order: system (base+user), developer, user history
	require.GreaterOrEqual(t, len(messages), 3, "should have system + developer + user messages")

	// First message: system with base + user instructions
	assert.NotNil(t, messages[0].OfSystem, "first message should be system")
	content := msgContent(t, messages[0])
	assert.Contains(t, content, "base prompt")
	assert.Contains(t, content, "be nice")

	// Second message: developer instructions
	assert.NotNil(t, messages[1].OfDeveloper, "second message should be developer")
	assert.Equal(t, "developer", msgRole(t, messages[1]))
	assert.Equal(t, "be useful", msgContent(t, messages[1]))

	// Third message: user from history
	assert.Equal(t, "user", msgRole(t, messages[2]))
}

// TestBuildMessages_NoInstructions verifies that when no instructions are set,
// no system or developer messages are prepended.
func TestBuildMessages_NoInstructions(t *testing.T) {
	client := &OpenAIClient{}
	request := LLMRequest{
		History: []models.ConversationItem{
			{Type: models.ItemTypeUserMessage, Content: "hello"},
		},
		ModelConfig: models.DefaultModelConfig(),
	}

	messages := client.buildMessages(request)

	require.Len(t, messages, 1, "only the user message from history")
	assert.Equal(t, "user", msgRole(t, messages[0]))
}

// TestBuildMessages_BaseAndUserInstructionsMerged verifies that base and user
// instructions are combined into a single system message (not two separate messages).
func TestBuildMessages_BaseAndUserInstructionsMerged(t *testing.T) {
	client := &OpenAIClient{}
	request := LLMRequest{
		History: []models.ConversationItem{
			{Type: models.ItemTypeUserMessage, Content: "hello"},
		},
		ModelConfig:      models.DefaultModelConfig(),
		BaseInstructions: "system base",
		UserInstructions: "user docs",
	}

	messages := client.buildMessages(request)

	// Should be: 1 system message + 1 user message (not 2 system messages)
	require.Len(t, messages, 2)

	assert.NotNil(t, messages[0].OfSystem)
	content := msgContent(t, messages[0])
	assert.Contains(t, content, "system base")
	assert.Contains(t, content, "user docs")
}

// TestBuildMessages_DeveloperOnlyNoSystem verifies that developer instructions
// work even when base/user instructions are empty (no system message).
func TestBuildMessages_DeveloperOnlyNoSystem(t *testing.T) {
	client := &OpenAIClient{}
	request := LLMRequest{
		History: []models.ConversationItem{
			{Type: models.ItemTypeUserMessage, Content: "hello"},
		},
		ModelConfig:           models.DefaultModelConfig(),
		DeveloperInstructions: "dev only",
	}

	messages := client.buildMessages(request)

	require.Len(t, messages, 2)

	// First: developer
	assert.NotNil(t, messages[0].OfDeveloper)
	assert.Equal(t, "dev only", msgContent(t, messages[0]))

	// Second: user history
	assert.Equal(t, "user", msgRole(t, messages[1]))
}

// TestBuildMessages_EmptyInstructionsIgnored verifies that empty string
// instructions don't produce empty system/developer messages.
//
// Maps to: collaboration_instructions.rs empty_collaboration_instructions_are_ignored
func TestBuildMessages_EmptyInstructionsIgnored(t *testing.T) {
	client := &OpenAIClient{}
	request := LLMRequest{
		History: []models.ConversationItem{
			{Type: models.ItemTypeUserMessage, Content: "hello"},
		},
		ModelConfig:           models.DefaultModelConfig(),
		BaseInstructions:      "",
		DeveloperInstructions: "",
		UserInstructions:      "",
	}

	messages := client.buildMessages(request)

	require.Len(t, messages, 1, "empty instructions should not produce messages")
	assert.Equal(t, "user", msgRole(t, messages[0]))
}

// TestBuildMessages_InstructionOrder verifies the full ordering:
// system → developer → conversation history
//
// Maps to: client.rs message ordering assertions (lines 342-347)
func TestBuildMessages_InstructionOrder(t *testing.T) {
	client := &OpenAIClient{}
	request := LLMRequest{
		History: []models.ConversationItem{
			{Type: models.ItemTypeUserMessage, Content: "user msg 1"},
			{Type: models.ItemTypeAssistantMessage, Content: "assistant msg 1"},
			{Type: models.ItemTypeUserMessage, Content: "user msg 2"},
		},
		ModelConfig:           models.DefaultModelConfig(),
		BaseInstructions:      "system prompt",
		DeveloperInstructions: "dev instructions",
		UserInstructions:      "user docs",
	}

	messages := client.buildMessages(request)

	require.Len(t, messages, 5, "system + developer + 3 history items")

	roles := make([]string, len(messages))
	for i, m := range messages {
		roles[i] = msgRole(t, m)
	}

	assert.Equal(t, []string{"system", "developer", "user", "assistant", "user"}, roles)
}

// --- Tests for convertHistoryToMessages ---

// TestConvertHistory_FunctionCallsGroupedWithAssistant verifies that
// consecutive FunctionCall items after an AssistantMessage are grouped
// into a single assistant message with tool_calls (OpenAI API requirement).
func TestConvertHistory_FunctionCallsGroupedWithAssistant(t *testing.T) {
	client := &OpenAIClient{}
	history := []models.ConversationItem{
		{Type: models.ItemTypeUserMessage, Content: "hello"},
		{Type: models.ItemTypeAssistantMessage, Content: "I'll help"},
		{Type: models.ItemTypeFunctionCall, CallID: "call1", Name: "shell", Arguments: `{"command":"ls"}`},
		{Type: models.ItemTypeFunctionCall, CallID: "call2", Name: "shell", Arguments: `{"command":"pwd"}`},
		{Type: models.ItemTypeFunctionCallOutput, CallID: "call1", Output: &models.FunctionCallOutputPayload{Content: "file.txt"}},
		{Type: models.ItemTypeFunctionCallOutput, CallID: "call2", Output: &models.FunctionCallOutputPayload{Content: "/home"}},
	}

	messages := client.convertHistoryToMessages(history)

	// Should be: user, assistant (with tool_calls), tool, tool
	require.Len(t, messages, 4)

	assert.Equal(t, "user", msgRole(t, messages[0]))

	// Assistant message should contain tool_calls
	assert.NotNil(t, messages[1].OfAssistant)
	require.Len(t, messages[1].OfAssistant.ToolCalls, 2)
	assert.Equal(t, "call1", messages[1].OfAssistant.ToolCalls[0].ID)
	assert.Equal(t, "call2", messages[1].OfAssistant.ToolCalls[1].ID)

	// Tool results
	assert.Equal(t, "tool", msgRole(t, messages[2]))
	assert.Equal(t, "tool", msgRole(t, messages[3]))
}

// TestConvertHistory_OrphanedFunctionCalls verifies that FunctionCall items
// without a preceding AssistantMessage get wrapped in an assistant message.
func TestConvertHistory_OrphanedFunctionCalls(t *testing.T) {
	client := &OpenAIClient{}
	history := []models.ConversationItem{
		{Type: models.ItemTypeUserMessage, Content: "hello"},
		{Type: models.ItemTypeFunctionCall, CallID: "call1", Name: "shell", Arguments: `{"command":"ls"}`},
		{Type: models.ItemTypeFunctionCallOutput, CallID: "call1", Output: &models.FunctionCallOutputPayload{Content: "out"}},
	}

	messages := client.convertHistoryToMessages(history)

	require.Len(t, messages, 3)

	assert.Equal(t, "user", msgRole(t, messages[0]))

	// Orphaned function call wrapped in assistant
	assert.NotNil(t, messages[1].OfAssistant)
	require.Len(t, messages[1].OfAssistant.ToolCalls, 1)
	assert.Equal(t, "call1", messages[1].OfAssistant.ToolCalls[0].ID)

	assert.Equal(t, "tool", msgRole(t, messages[2]))
}
