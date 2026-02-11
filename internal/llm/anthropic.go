package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/mfateev/codex-temporal-go/internal/models"
	"github.com/mfateev/codex-temporal-go/internal/tools"
)

// AnthropicClient implements LLMClient using Anthropic's Claude API.
//
// Maps to: Anthropic Messages API (similar to OpenAI but with differences)
type AnthropicClient struct {
	client anthropic.Client
}

// NewAnthropicClient creates an Anthropic client.
func NewAnthropicClient() *AnthropicClient {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &AnthropicClient{client: client}
}

// Call sends a request to Anthropic and returns the complete response.
// The response items match our ConversationItem format.
func (c *AnthropicClient) Call(ctx context.Context, request LLMRequest) (LLMResponse, error) {
	messages, err := c.buildMessages(request)
	if err != nil {
		return LLMResponse{}, fmt.Errorf("failed to build messages: %w", err)
	}

	// Build system prompt with caching
	systemBlocks := c.buildSystemBlocks(request)

	// Build parameters
	params := anthropic.MessageNewParams{
		Model:     selectAnthropicModel(request.ModelConfig.Model),
		MaxTokens: int64(request.ModelConfig.MaxTokens),
		System:    systemBlocks,
		Messages:  messages,
	}

	// Add temperature if specified
	if request.ModelConfig.Temperature > 0 {
		params.Temperature = anthropic.Float(request.ModelConfig.Temperature)
	}

	// Add tools if provided
	if len(request.ToolSpecs) > 0 {
		toolDefs := c.buildToolDefinitions(request.ToolSpecs)
		params.Tools = toolDefs
	}

	// Call Anthropic API
	response, err := c.client.Messages.New(ctx, params)
	if err != nil {
		return LLMResponse{}, classifyAnthropicError(err)
	}

	// Convert response to our format
	items, finishReason := c.parseResponse(response)

	return LLMResponse{
		Items:        items,
		FinishReason: finishReason,
		TokenUsage: models.TokenUsage{
			PromptTokens:     int(response.Usage.InputTokens),
			CompletionTokens: int(response.Usage.OutputTokens),
			TotalTokens:      int(response.Usage.InputTokens + response.Usage.OutputTokens),
		},
	}, nil
}

// selectAnthropicModel maps model names to Anthropic's Model type.
func selectAnthropicModel(modelName string) anthropic.Model {
	// Map common model names to Anthropic's constants
	switch modelName {
	case "claude-sonnet-4.5", "claude-sonnet-4.5-20250929":
		return anthropic.ModelClaudeSonnet4_5_20250929
	case "claude-opus-4.6", "claude-opus-4-6":
		return anthropic.ModelClaudeOpus4_6
	case "claude-haiku-4.5", "claude-haiku-4.5-20251001", "claude-haiku-4-5-20251001":
		return anthropic.ModelClaudeHaiku4_5_20251001
	case "claude-3.7-sonnet-20250219":
		return anthropic.ModelClaude3_7Sonnet20250219
	case "claude-3-opus-20240229":
		return anthropic.ModelClaude_3_Opus_20240229
	case "claude-3-haiku-20240307":
		return anthropic.ModelClaude_3_Haiku_20240307
	case "claude-3.5-haiku-20241022":
		return anthropic.ModelClaude3_5Haiku20241022
	default:
		// Default to Sonnet 4.5 if model not recognized
		return anthropic.ModelClaudeSonnet4_5_20250929
	}
}

// buildSystemBlocks creates system message blocks with prompt caching enabled.
//
// Anthropic's prompt caching reduces costs by 90% for cached content.
// We cache the base instructions and user instructions as separate blocks.
func (c *AnthropicClient) buildSystemBlocks(request LLMRequest) []anthropic.TextBlockParam {
	var blocks []anthropic.TextBlockParam

	// Base instructions (system prompt) - cacheable
	if request.BaseInstructions != "" {
		blocks = append(blocks, anthropic.TextBlockParam{
			Text: request.BaseInstructions,
			CacheControl: anthropic.CacheControlEphemeralParam{
				TTL: anthropic.CacheControlEphemeralTTLTTL5m,
			},
		})
	}

	// User instructions - also cacheable
	if request.UserInstructions != "" {
		blocks = append(blocks, anthropic.TextBlockParam{
			Text: request.UserInstructions,
			CacheControl: anthropic.CacheControlEphemeralParam{
				TTL: anthropic.CacheControlEphemeralTTLTTL5m,
			},
		})
	}

	return blocks
}

// buildMessages converts conversation history to Anthropic's message format.
//
// Key differences from OpenAI:
// 1. Tool calls are content blocks, not separate from assistant messages
// 2. Tool results go in user messages, not tool messages
// 3. System prompt is separate from messages
func (c *AnthropicClient) buildMessages(request LLMRequest) ([]anthropic.MessageParam, error) {
	messages := make([]anthropic.MessageParam, 0)

	// Add developer instructions as a user message if present
	if request.DeveloperInstructions != "" {
		messages = append(messages, anthropic.MessageParam{
			Role: anthropic.MessageParamRoleUser,
			Content: []anthropic.ContentBlockParamUnion{{
				OfText: &anthropic.TextBlockParam{
					Text: request.DeveloperInstructions,
				},
			}},
		})
	}

	// Convert conversation history
	historyMessages, err := c.convertHistoryToMessages(request.History)
	if err != nil {
		return nil, err
	}
	messages = append(messages, historyMessages...)

	return messages, nil
}

// convertHistoryToMessages converts our ConversationItem format to Anthropic messages.
//
// Anthropic format rules:
// - Messages alternate between user and assistant
// - Tool use blocks are part of assistant message content
// - Tool results are part of user message content
func (c *AnthropicClient) convertHistoryToMessages(history []models.ConversationItem) ([]anthropic.MessageParam, error) {
	messages := make([]anthropic.MessageParam, 0)

	i := 0
	for i < len(history) {
		item := history[i]

		switch item.Type {
		case models.ItemTypeUserMessage:
			// Simple user message
			messages = append(messages, anthropic.MessageParam{
				Role: anthropic.MessageParamRoleUser,
				Content: []anthropic.ContentBlockParamUnion{{
					OfText: &anthropic.TextBlockParam{
						Text: item.Content,
					},
				}},
			})
			i++

		case models.ItemTypeAssistantMessage:
			// Check if followed by FunctionCall items
			content := make([]anthropic.ContentBlockParamUnion, 0)

			// Add text content if present
			if item.Content != "" {
				content = append(content, anthropic.ContentBlockParamUnion{
					OfText: &anthropic.TextBlockParam{
						Text: item.Content,
					},
				})
			}

			// Collect following tool calls
			j := i + 1
			for j < len(history) && history[j].Type == models.ItemTypeFunctionCall {
				toolCall := history[j]

				// Parse arguments JSON string to map
				var inputMap map[string]interface{}
				if err := json.Unmarshal([]byte(toolCall.Arguments), &inputMap); err != nil {
					return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
				}

				content = append(content, anthropic.ContentBlockParamUnion{
					OfToolUse: &anthropic.ToolUseBlockParam{
						ID:    toolCall.CallID,
						Name:  toolCall.Name,
						Input: inputMap,
					},
				})
				j++
			}

			if len(content) > 0 {
				messages = append(messages, anthropic.MessageParam{
					Role:    anthropic.MessageParamRoleAssistant,
					Content: content,
				})
			}
			i = j

		case models.ItemTypeFunctionCall:
			// Orphaned function call - create assistant message
			content := make([]anthropic.ContentBlockParamUnion, 0)

			j := i
			for j < len(history) && history[j].Type == models.ItemTypeFunctionCall {
				toolCall := history[j]

				var inputMap map[string]interface{}
				if err := json.Unmarshal([]byte(toolCall.Arguments), &inputMap); err != nil {
					return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
				}

				content = append(content, anthropic.ContentBlockParamUnion{
					OfToolUse: &anthropic.ToolUseBlockParam{
						ID:    toolCall.CallID,
						Name:  toolCall.Name,
						Input: inputMap,
					},
				})
				j++
			}

			if len(content) > 0 {
				messages = append(messages, anthropic.MessageParam{
					Role:    anthropic.MessageParamRoleAssistant,
					Content: content,
				})
			}
			i = j

		case models.ItemTypeFunctionCallOutput:
			// Tool results go in user message
			isError := item.Output.Success != nil && !*item.Output.Success

			content := []anthropic.ContentBlockParamUnion{{
				OfToolResult: &anthropic.ToolResultBlockParam{
					ToolUseID: item.CallID,
					Content: []anthropic.ToolResultBlockParamContentUnion{{
					OfText: &anthropic.TextBlockParam{
						Text: item.Output.Content,
					},
				}},
					IsError:   anthropic.Bool(isError),
				},
			}}

			messages = append(messages, anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleUser,
				Content: content,
			})
			i++

		default:
			// Skip unknown types (turn markers, etc.)
			i++
		}
	}

	return messages, nil
}

// buildToolDefinitions converts ToolSpecs to Anthropic tool definitions.
func (c *AnthropicClient) buildToolDefinitions(specs []tools.ToolSpec) []anthropic.ToolUnionParam {
	toolDefs := make([]anthropic.ToolUnionParam, 0, len(specs))

	for _, spec := range specs {
		// Convert our []ToolParameter to Anthropic's format
		properties := make(map[string]interface{})
		required := make([]string, 0)

		for _, param := range spec.Parameters {
			prop := map[string]interface{}{
				"type":        param.Type,
				"description": param.Description,
			}
			if param.Items != nil {
				prop["items"] = param.Items
			}
			properties[param.Name] = prop
			if param.Required {
				required = append(required, param.Name)
			}
		}

		inputSchema := anthropic.ToolInputSchemaParam{
			Properties: properties,
		}

		if len(required) > 0 {
			inputSchema.Required = required
		}

		toolDefs = append(toolDefs, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        spec.Name,
				Description: anthropic.String(spec.Description),
				InputSchema: inputSchema,
			},
		})
	}

	return toolDefs
}

// parseResponse converts Anthropic's response to our ConversationItem format.
func (c *AnthropicClient) parseResponse(response *anthropic.Message) ([]models.ConversationItem, models.FinishReason) {
	items := make([]models.ConversationItem, 0)
	finishReason := models.FinishReasonStop

	// Process content blocks
	for _, contentBlock := range response.Content {
		switch contentBlock.Type {
		case "text":
			// Text content
			textBlock := contentBlock.AsText()
			if textBlock.Text != "" {
				items = append(items, models.ConversationItem{
					Type:    models.ItemTypeAssistantMessage,
					Content: textBlock.Text,
				})
			}

		case "tool_use":
			// Tool call
			toolBlock := contentBlock.AsToolUse()
			finishReason = models.FinishReasonToolCalls

			// Convert input map to JSON string
			argsJSON, err := json.Marshal(toolBlock.Input)
			if err != nil {
				argsJSON = []byte("{}")
			}

			items = append(items, models.ConversationItem{
				Type:      models.ItemTypeFunctionCall,
				CallID:    toolBlock.ID,
				Name:      toolBlock.Name,
				Arguments: string(argsJSON),
			})
		}
	}

	// If no items, add empty assistant message
	if len(items) == 0 {
		items = append(items, models.ConversationItem{
			Type: models.ItemTypeAssistantMessage,
		})
	}

	// Map stop reason
	switch response.StopReason {
	case anthropic.StopReasonEndTurn:
		finishReason = models.FinishReasonStop
	case anthropic.StopReasonToolUse:
		finishReason = models.FinishReasonToolCalls
	case anthropic.StopReasonMaxTokens:
		finishReason = models.FinishReasonLength
	case anthropic.StopReasonStopSequence:
		finishReason = models.FinishReasonStop
	}

	return items, finishReason
}

// classifyAnthropicError categorizes an Anthropic API error using the HTTP
// status code when available, falling back to message-based heuristics.
func classifyAnthropicError(err error) error {
	errMsg := strings.ToLower(err.Error())

	// Context overflow detection
	if strings.Contains(errMsg, "context_length") || strings.Contains(errMsg, "too many tokens") {
		return models.NewContextOverflowError(err.Error())
	}

	// Use typed error for status-code-based classification
	if apiErr, ok := err.(*anthropic.Error); ok {
		return classifyByStatusCode(apiErr.StatusCode, err)
	}

	// Fallback for non-typed errors
	if strings.Contains(errMsg, "rate_limit") || strings.Contains(errMsg, "rate limit") {
		return models.NewAPILimitError(err.Error())
	}
	return models.NewTransientError(fmt.Sprintf("Anthropic API error: %v", err))
}
