package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mfateev/temporal-agent-harness/internal/models"
	"github.com/mfateev/temporal-agent-harness/internal/tools"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

// OpenAIClient implements LLMClient using OpenAI's Responses API.
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

// Call sends a request to OpenAI's Responses API and returns the complete response.
// The response items match Codex's ResponseItem format:
// - AssistantMessage item for text content
// - Separate FunctionCall items for each tool call
func (c *OpenAIClient) Call(ctx context.Context, request LLMRequest) (LLMResponse, error) {
	input := c.buildInput(request.History)

	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(request.ModelConfig.Model),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: responses.ResponseInputParam(input),
		},
	}

	// Instructions (combined base + user)
	instructions := c.buildInstructions(request)
	if instructions != "" {
		params.Instructions = openai.String(instructions)
	}

	// Model parameters — reasoning models (o-series, codex) reject temperature
	if request.ModelConfig.Temperature > 0 && !isReasoningModel(request.ModelConfig.Model) {
		params.Temperature = openai.Float(request.ModelConfig.Temperature)
	}
	if request.ModelConfig.MaxTokens > 0 {
		params.MaxOutputTokens = openai.Int(int64(request.ModelConfig.MaxTokens))
	}

	// Reasoning effort for reasoning models (o-series, codex)
	if request.ModelConfig.ReasoningEffort != "" && isReasoningModel(request.ModelConfig.Model) {
		params.Reasoning = shared.ReasoningParam{
			Effort: shared.ReasoningEffort(request.ModelConfig.ReasoningEffort),
		}
	}

	// Tool definitions (function tools + optional native web_search)
	toolDefs := c.buildToolDefinitions(request.ToolSpecs, request.WebSearchMode)
	if len(toolDefs) > 0 {
		params.Tools = toolDefs
	}

	// Previous response ID for incremental sends
	if request.PreviousResponseID != "" {
		params.PreviousResponseID = openai.String(request.PreviousResponseID)
	}

	// Store for response persistence
	params.Store = openai.Bool(true)

	resp, err := c.client.Responses.New(ctx, params)
	if err != nil {
		return LLMResponse{}, classifyError(err)
	}

	items, finishReason := c.parseOutput(resp)

	return LLMResponse{
		Items:        items,
		FinishReason: finishReason,
		ResponseID:   resp.ID,
		TokenUsage: models.TokenUsage{
			PromptTokens:     int(resp.Usage.InputTokens),
			CompletionTokens: int(resp.Usage.OutputTokens),
			TotalTokens:      int(resp.Usage.TotalTokens),
			CachedTokens:     int(resp.Usage.InputTokensDetails.CachedTokens),
		},
	}, nil
}

// buildInput converts conversation history to Responses API input items.
//
// Type mapping:
//   - user_message → EasyInputMessageParam{Role: "user"}
//   - assistant_message → ResponseOutputMessageParam (fed back as input)
//   - function_call → ResponseFunctionToolCallParam
//   - function_call_output → ResponseInputItemFunctionCallOutputParam
//   - turn_started/turn_complete → skipped (internal markers)
func (c *OpenAIClient) buildInput(history []models.ConversationItem) []responses.ResponseInputItemUnionParam {
	items := make([]responses.ResponseInputItemUnionParam, 0, len(history))

	for _, item := range history {
		switch item.Type {
		case models.ItemTypeUserMessage:
			items = append(items, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role: responses.EasyInputMessageRoleUser,
					Content: responses.EasyInputMessageContentUnionParam{
						OfString: openai.String(item.Content),
					},
				},
			})

		case models.ItemTypeAssistantMessage:
			items = append(items, responses.ResponseInputItemUnionParam{
				OfOutputMessage: &responses.ResponseOutputMessageParam{
					Content: []responses.ResponseOutputMessageContentUnionParam{
						{
							OfOutputText: &responses.ResponseOutputTextParam{
								Text:        item.Content,
								Annotations: []responses.ResponseOutputTextAnnotationUnionParam{},
							},
						},
					},
					Status: responses.ResponseOutputMessageStatusCompleted,
				},
			})

		case models.ItemTypeFunctionCall:
			items = append(items, responses.ResponseInputItemUnionParam{
				OfFunctionCall: &responses.ResponseFunctionToolCallParam{
					CallID:    item.CallID,
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})

		case models.ItemTypeFunctionCallOutput:
			content := ""
			if item.Output != nil {
				content = item.Output.Content
			}
			items = append(items, responses.ResponseInputItemUnionParam{
				OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
					CallID: item.CallID,
					Output: content,
				},
			})

		case models.ItemTypeCompaction:
			// Compaction markers are internal tracking items. After compaction,
			// the history contains a summary as an assistant message which is
			// already handled above. Skip the marker itself.

		case models.ItemTypeWebSearchCall:
			// Web search call items are informational metadata from the API.
			// The model already incorporates search results into its text
			// response, so these are not valid input items. Skip them.

		default:
			// Skip turn_started, turn_complete markers (internal only)
		}
	}

	return items
}

// buildInstructions combines BaseInstructions + UserInstructions into a single
// instructions string for the Responses API Instructions parameter.
// DeveloperInstructions are prepended with a [Developer] header.
func (c *OpenAIClient) buildInstructions(request LLMRequest) string {
	// Build system-level instructions from base + user
	systemContent := request.BaseInstructions
	if request.UserInstructions != "" {
		if systemContent != "" {
			systemContent += "\n\n" + request.UserInstructions
		} else {
			systemContent = request.UserInstructions
		}
	}

	// Developer instructions are combined into the instructions field
	if request.DeveloperInstructions != "" {
		if systemContent != "" {
			systemContent += "\n\n[Developer Instructions]\n" + request.DeveloperInstructions
		} else {
			systemContent = request.DeveloperInstructions
		}
	}

	return systemContent
}

// parseOutput converts Responses API output items to ConversationItems.
// Returns the items and the inferred finish reason.
//
// Uses flat fields from ResponseOutputItemUnion directly (rather than
// .AsMessage()/.AsFunctionCall() which rely on internal JSON state).
func (c *OpenAIClient) parseOutput(resp *responses.Response) ([]models.ConversationItem, models.FinishReason) {
	var items []models.ConversationItem
	hasFunctionCalls := false

	for _, outputItem := range resp.Output {
		switch outputItem.Type {
		case "message":
			var text string
			for _, content := range outputItem.Content {
				if content.Type == "output_text" {
					text += content.Text
				}
			}
			if text != "" {
				items = append(items, models.ConversationItem{
					Type:    models.ItemTypeAssistantMessage,
					Content: text,
				})
			}

		case "function_call":
			hasFunctionCalls = true
			items = append(items, models.ConversationItem{
				Type:      models.ItemTypeFunctionCall,
				CallID:    outputItem.CallID,
				Name:      outputItem.Name,
				Arguments: outputItem.Arguments,
			})

		case "web_search_call":
			// Informational: record what was searched. The model already
			// incorporates search results into its text response.
			query := extractWebSearchQuery(outputItem)
			items = append(items, models.ConversationItem{
				Type:    models.ItemTypeWebSearchCall,
				Content: query,
			})
		}
	}

	// If no items were parsed, add an empty assistant message
	if len(items) == 0 {
		items = append(items, models.ConversationItem{
			Type: models.ItemTypeAssistantMessage,
		})
	}

	finishReason := models.FinishReasonStop
	if hasFunctionCalls {
		finishReason = models.FinishReasonToolCalls
	}

	return items, finishReason
}

// buildToolDefinitions converts ToolSpecs to Responses API tool definitions.
// If webSearchMode is set, the OpenAI-native web_search_preview tool is appended.
func (c *OpenAIClient) buildToolDefinitions(specs []tools.ToolSpec, webSearchMode models.WebSearchMode) []responses.ToolUnionParam {
	toolDefs := make([]responses.ToolUnionParam, 0, len(specs)+1)

	for _, spec := range specs {
		properties := make(map[string]interface{})
		required := make([]string, 0)

		for _, p := range spec.Parameters {
			prop := map[string]interface{}{
				"type":        p.Type,
				"description": p.Description,
			}
			if p.Items != nil {
				prop["items"] = p.Items
			}
			properties[p.Name] = prop
			if p.Required {
				required = append(required, p.Name)
			}
		}

		toolDefs = append(toolDefs, responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        spec.Name,
				Description: openai.String(spec.Description),
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": properties,
					"required":   required,
				},
			},
		})
	}

	// Append OpenAI-native web_search tool if enabled
	if webSearchMode == models.WebSearchCached || webSearchMode == models.WebSearchLive {
		toolDefs = append(toolDefs, responses.ToolParamOfWebSearchPreview(
			responses.WebSearchToolTypeWebSearchPreview,
		))
	}

	return toolDefs
}

// extractWebSearchQuery extracts a human-readable search description from a
// web_search_call output item. Returns the search query if available.
func extractWebSearchQuery(item responses.ResponseOutputItemUnion) string {
	if item.Action.Type == "search" && item.Action.Query != "" {
		return item.Action.Query
	}
	if item.Action.Type == "open_page" && item.Action.URL != "" {
		return item.Action.URL
	}
	if item.Action.Type == "find" && item.Action.Pattern != "" {
		return item.Action.Pattern
	}
	return "web search"
}

// Compact performs remote compaction via OpenAI's POST /responses/compact endpoint.
// Returns opaque compaction items that can be fed back as input to subsequent calls.
//
// Maps to: codex-rs/core/src/compact.rs remote compaction path
func (c *OpenAIClient) Compact(ctx context.Context, request CompactRequest) (CompactResponse, error) {
	input := c.buildInput(request.Input)

	// Build the raw payload for POST /responses/compact
	// The SDK doesn't have a Compact method, so we use raw HTTP.
	payload := map[string]interface{}{
		"model": request.Model,
		"input": input,
	}
	if request.Instructions != "" {
		payload["instructions"] = request.Instructions
	}

	var rawResp map[string]interface{}
	err := c.client.Post(ctx, "responses/compact", payload, &rawResp)
	if err != nil {
		return CompactResponse{}, fmt.Errorf("compact API call failed: %w", err)
	}

	// Parse the compacted output items from the response
	items, tokenUsage := parseCompactResponse(rawResp)

	return CompactResponse{
		Items:      items,
		TokenUsage: tokenUsage,
	}, nil
}

// parseCompactResponse extracts compacted items and token usage from the raw
// /responses/compact response. Opaque compaction items are stored as
// ItemTypeCompaction with the raw JSON in Content.
func parseCompactResponse(raw map[string]interface{}) ([]models.ConversationItem, models.TokenUsage) {
	var items []models.ConversationItem
	var usage models.TokenUsage

	// Extract output items
	if output, ok := raw["output"].([]interface{}); ok {
		for _, item := range output {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			itemType, _ := itemMap["type"].(string)

			switch itemType {
			case "message":
				// Preserved user/assistant messages
				role, _ := itemMap["role"].(string)
				if content, ok := itemMap["content"].([]interface{}); ok {
					var text string
					for _, c := range content {
						if cm, ok := c.(map[string]interface{}); ok {
							if t, ok := cm["text"].(string); ok {
								text += t
							}
						}
					}
					if role == "user" {
						items = append(items, models.ConversationItem{
							Type:    models.ItemTypeUserMessage,
							Content: text,
						})
					} else {
						items = append(items, models.ConversationItem{
							Type:    models.ItemTypeAssistantMessage,
							Content: text,
						})
					}
				}

			default:
				// Opaque compaction items (compaction_content, etc.) —
				// store the raw JSON so it can be passed back to OpenAI.
				rawJSON, err := json.Marshal(item)
				if err != nil {
					continue
				}
				items = append(items, models.ConversationItem{
					Type:    models.ItemTypeCompaction,
					Content: string(rawJSON),
				})
			}
		}
	}

	// Extract token usage
	if usageMap, ok := raw["usage"].(map[string]interface{}); ok {
		if v, ok := usageMap["input_tokens"].(float64); ok {
			usage.PromptTokens = int(v)
		}
		if v, ok := usageMap["output_tokens"].(float64); ok {
			usage.CompletionTokens = int(v)
		}
		if v, ok := usageMap["total_tokens"].(float64); ok {
			usage.TotalTokens = int(v)
		}
		if details, ok := usageMap["input_tokens_details"].(map[string]interface{}); ok {
			if v, ok := details["cached_tokens"].(float64); ok {
				usage.CachedTokens = int(v)
			}
		}
	}

	return items, usage
}

// isReasoningModel returns true for OpenAI reasoning models (o-series and codex)
// that do not support the temperature parameter and use reasoning effort instead.
func isReasoningModel(model string) bool {
	return strings.HasPrefix(model, "o1") ||
		strings.HasPrefix(model, "o3") ||
		strings.HasPrefix(model, "o4") ||
		strings.Contains(model, "codex")
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
