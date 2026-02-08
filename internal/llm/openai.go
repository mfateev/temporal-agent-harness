package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/tools"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"
)

// OpenAIClient implements LLMClient using OpenAI's API
//
// Maps to: codex-rs/core/src/client.rs OpenAI implementation
type OpenAIClient struct {
	client openai.Client
}

// NewOpenAIClient creates an OpenAI client
func NewOpenAIClient() *OpenAIClient {
	apiKey := os.Getenv("OPENAI_API_KEY")
	client := openai.NewClient(option.WithAPIKey(apiKey))

	return &OpenAIClient{
		client: client,
	}
}

// Call sends a request to OpenAI and returns the complete response
func (c *OpenAIClient) Call(ctx context.Context, request LLMRequest) (LLMResponse, error) {
	// Convert history to OpenAI messages format
	messages := c.convertHistoryToMessages(request.History)

	// Prepare parameters
	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(request.ModelConfig.Model),
		Messages: messages,
	}

	// Add tool definitions if tools are provided
	if len(request.ToolSpecs) > 0 {
		params.Tools = c.buildToolDefinitions(request.ToolSpecs)
	}

	// Call OpenAI
	completion, err := c.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return LLMResponse{}, classifyError(err)
	}

	if len(completion.Choices) == 0 {
		return LLMResponse{}, fmt.Errorf("no choices in response")
	}

	choice := completion.Choices[0]

	response := LLMResponse{
		Content:      choice.Message.Content,
		FinishReason: models.FinishReasonStop,
		TokenUsage: models.TokenUsage{
			PromptTokens:     int(completion.Usage.PromptTokens),
			CompletionTokens: int(completion.Usage.CompletionTokens),
			TotalTokens:      int(completion.Usage.TotalTokens),
		},
	}

	// Parse tool calls if present
	if len(choice.Message.ToolCalls) > 0 {
		toolCalls := make([]models.ToolCall, 0, len(choice.Message.ToolCalls))
		for _, tc := range choice.Message.ToolCalls {
			var args map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				args = map[string]interface{}{"_raw": tc.Function.Arguments}
			}

			toolCalls = append(toolCalls, models.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: args,
			})
		}
		response.ToolCalls = toolCalls
		response.FinishReason = models.FinishReasonToolCalls
	}

	return response, nil
}

// convertHistoryToMessages converts conversation history to OpenAI messages format.
//
// OpenAI requires that tool result messages are preceded by an assistant message
// containing the corresponding tool_calls. This function constructs the proper
// message sequence including tool calls in assistant messages.
func (c *OpenAIClient) convertHistoryToMessages(history []models.ConversationItem) []openai.ChatCompletionMessageParamUnion {
	messages := make([]openai.ChatCompletionMessageParamUnion, 0, len(history))

	for _, item := range history {
		switch item.Type {
		case models.ItemTypeUserMessage:
			messages = append(messages, openai.UserMessage(item.Content))

		case models.ItemTypeAssistantMessage:
			if len(item.ToolCalls) > 0 {
				// Build tool calls for the assistant message
				toolCalls := make([]openai.ChatCompletionMessageToolCallParam, 0, len(item.ToolCalls))
				for _, tc := range item.ToolCalls {
					argsJSON, _ := json.Marshal(tc.Arguments)
					toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallParam{
						ID: tc.ID,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      tc.Name,
							Arguments: string(argsJSON),
						},
					})
				}

				// Create assistant message with tool calls via OfAssistant pointer
				assistantMsg := &openai.ChatCompletionAssistantMessageParam{
					ToolCalls: toolCalls,
				}
				if item.Content != "" {
					assistantMsg.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
						OfString: param.NewOpt(item.Content),
					}
				}
				messages = append(messages, openai.ChatCompletionMessageParamUnion{
					OfAssistant: assistantMsg,
				})
			} else {
				messages = append(messages, openai.AssistantMessage(item.Content))
			}

		case models.ItemTypeToolResult:
			// Tool result message - must follow an assistant message with tool_calls
			content := item.ToolOutput
			if item.ToolError != "" {
				content = fmt.Sprintf("Error: %s", item.ToolError)
			}
			messages = append(messages, openai.ToolMessage(content, item.ToolCallID))
		}
	}

	return messages
}

// buildToolDefinitions converts ToolSpecs to OpenAI tool definitions
func (c *OpenAIClient) buildToolDefinitions(specs []tools.ToolSpec) []openai.ChatCompletionToolParam {
	toolDefs := make([]openai.ChatCompletionToolParam, 0, len(specs))

	for _, spec := range specs {
		// Convert parameters to JSON schema
		properties := make(map[string]interface{})
		required := make([]string, 0)

		for _, p := range spec.Parameters {
			properties[p.Name] = map[string]interface{}{
				"type":        p.Type,
				"description": p.Description,
			}

			if p.Required {
				required = append(required, p.Name)
			}
		}

		funcDef := shared.FunctionDefinitionParam{
			Name: spec.Name,
			Description: param.NewOpt(spec.Description),
			Parameters: shared.FunctionParameters{
				"type":       "object",
				"properties": properties,
				"required":   required,
			},
		}

		toolDefs = append(toolDefs, openai.ChatCompletionToolParam{
			Function: funcDef,
		})
	}

	return toolDefs
}

// classifyError categorizes an OpenAI API error
func classifyError(err error) error {
	errMsg := strings.ToLower(err.Error())
	if strings.Contains(errMsg, "context_length") || strings.Contains(errMsg, "maximum context length") {
		return models.NewContextOverflowError(err.Error())
	}
	if strings.Contains(errMsg, "rate_limit") || strings.Contains(errMsg, "rate limit") {
		return models.NewAPILimitError(err.Error())
	}
	return models.NewTransientError(fmt.Sprintf("OpenAI API error: %v", err))
}
