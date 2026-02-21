package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mfateev/temporal-agent-harness/internal/models"
	"github.com/mfateev/temporal-agent-harness/internal/tools"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
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
		params.Instructions = param.NewOpt(instructions)
	}

	// Model parameters — reasoning models (o-series, codex) reject temperature
	if request.ModelConfig.Temperature > 0 && !isReasoningModel(request.ModelConfig.Model) {
		params.Temperature = param.NewOpt(request.ModelConfig.Temperature)
	}
	if request.ModelConfig.MaxTokens > 0 {
		params.MaxOutputTokens = param.NewOpt(int64(request.ModelConfig.MaxTokens))
	}

	// Reasoning effort for reasoning models (o-series, codex)
	if request.ModelConfig.ReasoningEffort != "" && isReasoningModel(request.ModelConfig.Model) {
		params.Reasoning = shared.ReasoningParam{
			Effort: shared.ReasoningEffort(request.ModelConfig.ReasoningEffort),
		}
	}

	// Tool definitions (function tools + optional web search)
	if len(request.ToolSpecs) > 0 || request.WebSearchMode != "" {
		params.Tools = c.buildToolDefinitions(request.ToolSpecs, request.WebSearchMode)
	}

	// Previous response ID for incremental sends
	if request.PreviousResponseID != "" {
		params.PreviousResponseID = param.NewOpt(request.PreviousResponseID)
	}

	// Store for response persistence
	params.Store = param.NewOpt(true)

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
						OfString: param.NewOpt(item.Content),
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
					Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
						OfString: param.NewOpt(content),
					},
				},
			})

		case models.ItemTypeWebSearchCall:
			// Web search calls are fed back via OfWebSearchCall so the API
			// maintains conversation state. We reconstruct the action union
			// from the stored action type fields.
			wsParam := &responses.ResponseFunctionWebSearchParam{
				ID:     item.CallID,
				Status: responses.ResponseFunctionWebSearchStatus(item.WebSearchStatus),
			}
			items = append(items, responses.ResponseInputItemUnionParam{
				OfWebSearchCall: wsParam,
			})

		case models.ItemTypeModelSwitch:
			// Model-switch messages are sent as developer-role messages so
			// the new model has context about the transition.
			items = append(items, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role: responses.EasyInputMessageRoleDeveloper,
					Content: responses.EasyInputMessageContentUnionParam{
						OfString: param.NewOpt(item.Content),
					},
				},
			})

		case models.ItemTypeCompaction:
			// Compaction markers are internal tracking items. After compaction,
			// the history contains a summary as an assistant message which is
			// already handled above. Skip the marker itself.

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
			action, url := extractWebSearchAction(outputItem.Action)
			detail := formatWebSearchDetail(action, outputItem.Action)
			items = append(items, models.ConversationItem{
				Type:            models.ItemTypeWebSearchCall,
				CallID:          outputItem.ID,
				Content:         detail,
				WebSearchAction: action,
				WebSearchStatus: outputItem.Status,
				WebSearchURL:    url,
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
// Also appends a web_search tool if WebSearchMode is set.
//
// Maps to: codex-rs/core/src/tools/spec.rs web_search_mode handling
func (c *OpenAIClient) buildToolDefinitions(specs []tools.ToolSpec, webSearchMode models.WebSearchMode) []responses.ToolUnionParam {
	toolDefs := make([]responses.ToolUnionParam, 0, len(specs)+1)

	for _, spec := range specs {
		var paramSchema map[string]interface{}

		if spec.RawJSONSchema != nil {
			// MCP tools provide a full JSON Schema directly
			paramSchema = spec.RawJSONSchema
		} else {
			// Build schema from Parameters
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

			paramSchema = map[string]interface{}{
				"type":       "object",
				"properties": properties,
				"required":   required,
			}
		}

		toolDefs = append(toolDefs, responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        spec.Name,
				Description: param.NewOpt(spec.Description),
				Parameters:  paramSchema,
			},
		})
	}

	// Append web search tool if mode is not disabled/empty.
	// Codex uses external_web_access (not in SDK v3), so we map to SearchContextSize:
	//   cached → low (minimal context, faster)
	//   live   → medium (default, fresh results)
	//
	// Maps to: codex-rs/core/src/tools/spec.rs web_search_mode → ToolSpec::WebSearch
	switch webSearchMode {
	case models.WebSearchCached:
		toolDefs = append(toolDefs, responses.ToolUnionParam{
			OfWebSearch: &responses.WebSearchToolParam{
				Type:              responses.WebSearchToolTypeWebSearch,
				SearchContextSize: responses.WebSearchToolSearchContextSizeLow,
			},
		})
	case models.WebSearchLive:
		toolDefs = append(toolDefs, responses.ToolUnionParam{
			OfWebSearch: &responses.WebSearchToolParam{
				Type:              responses.WebSearchToolTypeWebSearch,
				SearchContextSize: responses.WebSearchToolSearchContextSizeMedium,
			},
		})
	}

	return toolDefs
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

// extractWebSearchAction extracts the action type and URL from a web search
// output item's Action union field.
//
// Maps to: codex-rs/core/src/event_mapping.rs WebSearchCall handling
func extractWebSearchAction(action responses.ResponseOutputItemUnionAction) (actionType, url string) {
	actionType = action.Type
	url = action.URL
	return actionType, url
}

// formatWebSearchDetail formats a web search action for display, matching
// Codex's web_search_action_detail function.
//
//	search       → query (or first of queries + "...")
//	open_page    → URL
//	find_in_page → 'pattern' in URL
//
// Maps to: codex-rs/core/src/web_search.rs web_search_action_detail
func formatWebSearchDetail(actionType string, action responses.ResponseOutputItemUnionAction) string {
	switch actionType {
	case "search":
		query := action.Query
		if query != "" {
			return query
		}
		if len(action.Queries) > 0 {
			first := action.Queries[0]
			if len(action.Queries) > 1 && first != "" {
				return first + " ..."
			}
			return first
		}
		return ""
	case "open_page":
		return action.URL
	case "find_in_page":
		pattern := action.Pattern
		url := action.URL
		switch {
		case pattern != "" && url != "":
			return fmt.Sprintf("'%s' in %s", pattern, url)
		case pattern != "":
			return fmt.Sprintf("'%s'", pattern)
		case url != "":
			return url
		}
		return ""
	default:
		return ""
	}
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
