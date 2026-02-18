package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/mfateev/temporal-agent-harness/internal/models"
	"github.com/mfateev/temporal-agent-harness/internal/tools"
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
			PromptTokens:        int(response.Usage.InputTokens),
			CompletionTokens:    int(response.Usage.OutputTokens),
			TotalTokens:         int(response.Usage.InputTokens + response.Usage.OutputTokens),
			CachedTokens:        int(response.Usage.CacheReadInputTokens),
			CacheCreationTokens: int(response.Usage.CacheCreationInputTokens),
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
			Text:         request.BaseInstructions,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		})
	}

	// User instructions - also cacheable
	if request.UserInstructions != "" {
		blocks = append(blocks, anthropic.TextBlockParam{
			Text:         request.UserInstructions,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
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

	// Add cache breakpoint to the last content block of the penultimate message.
	// This caches all conversation history before the current user turn, so
	// repeated turns in a long session skip re-processing prior context.
	if len(messages) >= 2 {
		penultimate := &messages[len(messages)-2]
		if len(penultimate.Content) > 0 {
			if cc := penultimate.Content[len(penultimate.Content)-1].GetCacheControl(); cc != nil {
				*cc = anthropic.NewCacheControlEphemeralParam()
			}
		}
	}

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

	// Add cache breakpoint on the last tool definition to cache all tool specs.
	// This avoids re-processing the tool list on every turn within a session.
	if len(toolDefs) > 0 {
		if last := toolDefs[len(toolDefs)-1].OfTool; last != nil {
			last.CacheControl = anthropic.NewCacheControlEphemeralParam()
		}
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

// Compact performs local compaction via LLM summarization.
// Sends the current history with a compaction prompt, extracts the summary,
// and rebuilds history with summary + recent user messages.
//
// Maps to: codex-rs/core/src/compact.rs local compaction path
func (c *AnthropicClient) Compact(ctx context.Context, request CompactRequest) (CompactResponse, error) {
	// Build a summarization request with the compaction prompt appended
	historyWithPrompt := make([]models.ConversationItem, len(request.Input))
	copy(historyWithPrompt, request.Input)
	historyWithPrompt = append(historyWithPrompt, models.ConversationItem{
		Type:    models.ItemTypeUserMessage,
		Content: compactionPrompt,
	})

	llmRequest := LLMRequest{
		History: historyWithPrompt,
		ModelConfig: models.ModelConfig{
			Provider:      "anthropic",
			Model:         request.Model,
			MaxTokens:     4096,
			ContextWindow: 128000,
		},
		BaseInstructions: request.Instructions,
	}

	resp, err := c.Call(ctx, llmRequest)
	if err != nil {
		return CompactResponse{}, fmt.Errorf("compaction LLM call failed: %w", err)
	}

	// Extract the summary from the last assistant message
	summary := extractLastAssistantMessage(resp.Items)
	if summary == "" {
		return CompactResponse{}, fmt.Errorf("compaction produced empty summary")
	}

	// Collect recent user messages within a 20k token budget
	recentItems := collectRecentUserMessages(request.Input, 20_000)

	// Build compacted history: compaction marker + summary + recent items
	compactedItems := buildCompactedHistory(summary, recentItems)

	return CompactResponse{
		Items:      compactedItems,
		TokenUsage: resp.TokenUsage,
	}, nil
}

// compactionPrompt is the prompt sent to the LLM for local context compaction.
// Ported from: codex-rs/core/templates/compact/compact_prompt.md
const compactionPrompt = `You are performing a CONTEXT CHECKPOINT COMPACTION.

Your task is to create a concise but comprehensive summary of the conversation so far.
This summary will replace the conversation history, so it must contain ALL information
needed to continue the task without loss.

Include:
1. The original user request/goal
2. All significant decisions made and their rationale
3. Current state of the work (what's done, what's in progress, what's remaining)
4. File paths, function names, and other specific identifiers that were discussed
5. Any errors encountered and how they were resolved
6. Key code changes or architectural decisions
7. Tool calls made and their results (summarized)

Format your response as a structured summary. Be thorough but concise.
Do NOT include any conversational pleasantries or meta-commentary about the compaction.`

// compactionSummaryPrefix is prepended to the summary when rebuilding history.
// Ported from: codex-rs/core/templates/compact/compact_summary_prefix.md
const compactionSummaryPrefix = `Another language model started to solve this problem and ran out of context. Here is a summary of the conversation so far:

`

// extractLastAssistantMessage finds the last assistant message content from items.
func extractLastAssistantMessage(items []models.ConversationItem) string {
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Type == models.ItemTypeAssistantMessage && items[i].Content != "" {
			return items[i].Content
		}
	}
	return ""
}

// collectRecentUserMessages iterates backwards through items, collecting user
// messages and their associated tool call items within a token budget.
// Uses ~4 chars/token estimate.
func collectRecentUserMessages(items []models.ConversationItem, tokenBudget int) []models.ConversationItem {
	charBudget := tokenBudget * 4
	var collected []models.ConversationItem
	usedChars := 0

	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		// Skip compaction markers, turn markers
		if item.Type == models.ItemTypeCompaction ||
			item.Type == models.ItemTypeTurnStarted ||
			item.Type == models.ItemTypeTurnComplete {
			continue
		}

		// Estimate chars for this item
		itemChars := len(item.Content) + len(item.Arguments)
		if item.Output != nil {
			itemChars += len(item.Output.Content)
		}

		if usedChars+itemChars > charBudget && len(collected) > 0 {
			break
		}

		collected = append(collected, item)
		usedChars += itemChars
	}

	// Reverse to restore chronological order
	for i, j := 0, len(collected)-1; i < j; i, j = i+1, j-1 {
		collected[i], collected[j] = collected[j], collected[i]
	}

	return collected
}

// buildCompactedHistory assembles the compacted history from a summary and recent items.
// Returns: [compaction marker, summary as assistant message, recent items...]
func buildCompactedHistory(summary string, recentItems []models.ConversationItem) []models.ConversationItem {
	items := make([]models.ConversationItem, 0, 2+len(recentItems))

	// Compaction marker
	items = append(items, models.ConversationItem{
		Type:    models.ItemTypeCompaction,
		Content: "context_compacted",
	})

	// Summary as an assistant message with the prefix
	items = append(items, models.ConversationItem{
		Type:    models.ItemTypeAssistantMessage,
		Content: compactionSummaryPrefix + summary,
	})

	// Recent items
	items = append(items, recentItems...)

	return items
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
