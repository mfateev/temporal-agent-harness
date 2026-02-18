// Package workflow contains Temporal workflow definitions.
//
// suggestions.go implements post-turn prompt suggestion generation.
package workflow

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/mfateev/temporal-agent-harness/internal/activities"
	"github.com/mfateev/temporal-agent-harness/internal/instructions"
	"github.com/mfateev/temporal-agent-harness/internal/models"
)

// generateSuggestion runs the GenerateSuggestions activity synchronously to
// populate ctrl.suggestion. Called after TurnComplete marker is added but before
// the next awaitWithIdleTimeout. The CLI has already seen the TurnComplete via
// polling and can show the input prompt; the suggestion appears ~300-500ms later
// when the CLI's delayed poll picks it up.
//
// Best-effort: errors are silently ignored.
func (s *SessionState) generateSuggestion(ctx workflow.Context, ctrl *LoopControl) {
	input := s.buildSuggestionInput()
	if input == nil {
		return
	}

	suggCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1, // No retries — best-effort
		},
	})

	var out activities.SuggestionOutput
	err := workflow.ExecuteActivity(suggCtx, "GenerateSuggestions", *input).Get(ctx, &out)
	if err == nil && out.Suggestion != "" {
		ctrl.SetSuggestion(out.Suggestion)
	}
}

// buildSuggestionInput extracts the last user message, last assistant message,
// and tool summaries from history to build SuggestionInput.
// Returns nil if there's insufficient history for a meaningful suggestion.
func (s *SessionState) buildSuggestionInput() *activities.SuggestionInput {
	items, err := s.History.GetRawItems()
	if err != nil || len(items) == 0 {
		return nil
	}

	var lastUserMsg, lastAssistantMsg string
	var toolSummaries []string

	// Walk backward through history to find the last messages
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		switch item.Type {
		case models.ItemTypeUserMessage:
			if lastUserMsg == "" {
				lastUserMsg = item.Content
			}
		case models.ItemTypeAssistantMessage:
			if lastAssistantMsg == "" {
				lastAssistantMsg = item.Content
			}
		case models.ItemTypeFunctionCallOutput:
			if item.Output != nil {
				success := item.Output.Success != nil && *item.Output.Success
				toolSummaries = append(toolSummaries, instructions.FormatToolSummary(item.Name, success))
			}
		case models.ItemTypeFunctionCall:
			toolSummaries = append(toolSummaries, item.Name)
		case models.ItemTypeTurnStarted:
			// Don't look past the current turn's start
			if lastUserMsg != "" {
				break
			}
		}
		// Stop once we have both messages
		if lastUserMsg != "" && lastAssistantMsg != "" {
			break
		}
	}

	if lastUserMsg == "" && lastAssistantMsg == "" {
		return nil
	}

	// Pick cheap model based on provider
	suggModel, suggProvider := instructions.SuggestionModelForProvider(s.Config.Model.Provider)

	return &activities.SuggestionInput{
		UserMessage:      lastUserMsg,
		AssistantMessage: lastAssistantMsg,
		ToolSummaries:    toolSummaries,
		ModelConfig: models.ModelConfig{
			Provider:      suggProvider,
			Model:         suggModel,
			Temperature:   0.3,
			MaxTokens:     50,
			ContextWindow: 4096,
		},
	}
}
