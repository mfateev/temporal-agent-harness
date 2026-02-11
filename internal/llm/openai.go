package llm

import (
	"context"
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

// OpenAIClient implements LLMClient using OpenAI's API.
//
// Maps to: codex-rs/core/src/client.rs OpenAI implementation
type OpenAIClient struct {
	client openai.Client
}

// NewOpenAIClient creates an OpenAI client.
func NewOpenAIClient() *OpenAIClient {
	apiKey := os.Getenv("OPENAI_API_KEY")
	client := openai.NewClient(option.WithAPIKey(apiKey))
	return &OpenAIClient{client: client}
}

// Call sends a request to OpenAI and returns the complete response.
// The response items match Codex's ResponseItem format:
// - AssistantMessage item for text content
// - Separate FunctionCall items for each tool call
func (c *OpenAIClient) Call(ctx context.Context, request LLMRequest) (LLMResponse, error) {
	messages := c.buildMessages(request)

	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(request.ModelConfig.Model),
		Messages: messages,
	}

	// Pass model parameters — these were previously ignored (bug fix).
	if request.ModelConfig.Temperature > 0 {
		params.Temperature = param.NewOpt(request.ModelConfig.Temperature)
	}
	if request.ModelConfig.MaxTokens > 0 {
		params.MaxTokens = param.NewOpt(int64(request.ModelConfig.MaxTokens))
	}

	if len(request.ToolSpecs) > 0 {
		params.Tools = c.buildToolDefinitions(request.ToolSpecs)
	}

	completion, err := c.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return LLMResponse{}, classifyError(err)
	}

	if len(completion.Choices) == 0 {
		return LLMResponse{}, fmt.Errorf("no choices in response")
	}

	choice := completion.Choices[0]

	// Build response items matching Codex's ResponseItem format
	var items []models.ConversationItem

	// Add assistant message if there's text content
	if choice.Message.Content != "" {
		items = append(items, models.ConversationItem{
			Type:    models.ItemTypeAssistantMessage,
			Content: choice.Message.Content,
		})
	}

	// Add separate FunctionCall items for each tool call
	// Matches Codex's ResponseItem::FunctionCall separation
	finishReason := models.FinishReasonStop
	if len(choice.Message.ToolCalls) > 0 {
		finishReason = models.FinishReasonToolCalls
		for _, tc := range choice.Message.ToolCalls {
			items = append(items, models.ConversationItem{
				Type:      models.ItemTypeFunctionCall,
				CallID:    tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments, // Raw JSON string
			})
		}
	}

	// If no content and no tool calls, add empty assistant message
	if len(items) == 0 {
		items = append(items, models.ConversationItem{
			Type: models.ItemTypeAssistantMessage,
		})
	}

	return LLMResponse{
		Items:        items,
		FinishReason: finishReason,
		TokenUsage: models.TokenUsage{
			PromptTokens:     int(completion.Usage.PromptTokens),
			CompletionTokens: int(completion.Usage.CompletionTokens),
			TotalTokens:      int(completion.Usage.TotalTokens),
		},
	}, nil
}

// buildMessages constructs the full message list: instruction messages + history.
//
// Instruction hierarchy (maps to Codex 3-tier system):
//   - BaseInstructions + UserInstructions → system message
//   - DeveloperInstructions → developer message
func (c *OpenAIClient) buildMessages(request LLMRequest) []openai.ChatCompletionMessageParamUnion {
	var messages []openai.ChatCompletionMessageParamUnion

	// Build system message from BaseInstructions + UserInstructions
	systemContent := request.BaseInstructions
	if request.UserInstructions != "" {
		if systemContent != "" {
			systemContent += "\n\n" + request.UserInstructions
		} else {
			systemContent = request.UserInstructions
		}
	}
	if systemContent != "" {
		messages = append(messages, openai.SystemMessage(systemContent))
	}

	// Developer instructions as a separate developer message
	if request.DeveloperInstructions != "" {
		messages = append(messages, openai.DeveloperMessage(request.DeveloperInstructions))
	}

	// Append conversation history
	messages = append(messages, c.convertHistoryToMessages(request.History)...)
	return messages
}

// convertHistoryToMessages converts conversation history to OpenAI messages format.
//
// OpenAI requires that tool result messages are preceded by an assistant message
// containing the corresponding tool_calls. Since our history stores FunctionCall
// items separately (matching Codex), we need to group consecutive FunctionCall
// items into a single assistant message with tool_calls.
func (c *OpenAIClient) convertHistoryToMessages(history []models.ConversationItem) []openai.ChatCompletionMessageParamUnion {
	messages := make([]openai.ChatCompletionMessageParamUnion, 0, len(history))

	i := 0
	for i < len(history) {
		item := history[i]

		switch item.Type {
		case models.ItemTypeUserMessage:
			messages = append(messages, openai.UserMessage(item.Content))
			i++

		case models.ItemTypeAssistantMessage:
			// Check if followed by FunctionCall items - if so, bundle them
			// into the assistant message's tool_calls (OpenAI format requirement)
			toolCalls := collectFollowingFunctionCalls(history, i+1)
			if len(toolCalls) > 0 {
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
				i += 1 + len(toolCalls) // Skip assistant + function call items
			} else {
				messages = append(messages, openai.AssistantMessage(item.Content))
				i++
			}

		case models.ItemTypeFunctionCall:
			// Orphaned FunctionCall without preceding AssistantMessage -
			// create an assistant message with just tool_calls
			toolCalls := collectFunctionCallsFrom(history, i)
			assistantMsg := &openai.ChatCompletionAssistantMessageParam{
				ToolCalls: toolCalls,
			}
			messages = append(messages, openai.ChatCompletionMessageParamUnion{
				OfAssistant: assistantMsg,
			})
			i += len(toolCalls)

		case models.ItemTypeFunctionCallOutput:
			content := ""
			if item.Output != nil {
				content = item.Output.Content
			}
			messages = append(messages, openai.ToolMessage(content, item.CallID))
			i++

		default:
			i++
		}
	}

	return messages
}

// collectFollowingFunctionCalls collects consecutive FunctionCall items starting at index.
func collectFollowingFunctionCalls(history []models.ConversationItem, startIdx int) []openai.ChatCompletionMessageToolCallParam {
	var toolCalls []openai.ChatCompletionMessageToolCallParam
	for j := startIdx; j < len(history); j++ {
		if history[j].Type != models.ItemTypeFunctionCall {
			break
		}
		toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallParam{
			ID: history[j].CallID,
			Function: openai.ChatCompletionMessageToolCallFunctionParam{
				Name:      history[j].Name,
				Arguments: history[j].Arguments,
			},
		})
	}
	return toolCalls
}

// collectFunctionCallsFrom collects consecutive FunctionCall items starting at index.
func collectFunctionCallsFrom(history []models.ConversationItem, startIdx int) []openai.ChatCompletionMessageToolCallParam {
	return collectFollowingFunctionCalls(history, startIdx)
}

// buildToolDefinitions converts ToolSpecs to OpenAI tool definitions.
func (c *OpenAIClient) buildToolDefinitions(specs []tools.ToolSpec) []openai.ChatCompletionToolParam {
	toolDefs := make([]openai.ChatCompletionToolParam, 0, len(specs))

	for _, spec := range specs {
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
			Name:        spec.Name,
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

// classifyError categorizes an OpenAI API error using the HTTP status code
// when available, falling back to message-based heuristics.
func classifyError(err error) error {
	// Check message-based patterns first (works regardless of error type)
	errMsg := strings.ToLower(err.Error())
	if strings.Contains(errMsg, "context_length") || strings.Contains(errMsg, "maximum context length") {
		return models.NewContextOverflowError(err.Error())
	}

	// Use typed error for status-code-based classification
	if apiErr, ok := err.(*openai.Error); ok {
		return classifyByStatusCode(apiErr.StatusCode, err)
	}

	// Fallback: message-based heuristics for non-typed errors (e.g., network errors)
	if strings.Contains(errMsg, "rate_limit") || strings.Contains(errMsg, "rate limit") {
		return models.NewAPILimitError(err.Error())
	}
	return models.NewTransientError(fmt.Sprintf("OpenAI API error: %v", err))
}
