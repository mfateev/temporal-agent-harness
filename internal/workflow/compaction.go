// Package workflow contains Temporal workflow definitions.
//
// compaction.go implements context compaction logic for managing conversation
// history when it grows too large for the LLM's context window.
//
// Maps to: codex-rs/core/src/compact.rs
package workflow

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/mfateev/temporal-agent-harness/internal/activities"
	"github.com/mfateev/temporal-agent-harness/internal/models"
)

// performCompaction executes context compaction by calling the ExecuteCompact
// activity. On success, replaces the conversation history with compacted items,
// increments CompactionCount, and resets response chaining state.
//
// Maps to: codex-rs/core/src/compact.rs perform_compaction
func (s *SessionState) performCompaction(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)

	// Set phase to compacting
	s.Phase = PhaseCompacting

	// Get full history for compaction
	historyItems, err := s.History.GetForPrompt()
	if err != nil {
		return err
	}

	// Strip model-switch messages before compaction. The compaction LLM should
	// not see model-switch developer messages (which contain instructions for
	// the *new* model). We re-add the last one after compaction completes.
	var modelSwitchItems []models.ConversationItem
	var filteredItems []models.ConversationItem
	for _, item := range historyItems {
		if item.Type == models.ItemTypeModelSwitch {
			modelSwitchItems = append(modelSwitchItems, item)
		} else {
			filteredItems = append(filteredItems, item)
		}
	}

	// Build compaction activity input
	compactInput := activities.CompactActivityInput{
		Model:        s.Config.Model.Model,
		Input:        filteredItems,
		Instructions: s.Config.BaseInstructions,
	}

	// Configure activity options
	actOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    2,
		},
	}
	compactCtx := workflow.WithActivityOptions(ctx, actOpts)

	// Execute compaction activity
	var compactResult activities.CompactActivityOutput
	err = workflow.ExecuteActivity(compactCtx, "ExecuteCompact", compactInput).Get(ctx, &compactResult)
	if err != nil {
		logger.Warn("Compaction activity failed", "error", err)
		return err
	}

	// Replace history with compacted items
	if err := s.History.ReplaceAll(compactResult.Items); err != nil {
		logger.Error("Failed to replace history after compaction", "error", err)
		return err
	}

	// Re-add the last model-switch message so the new model retains context
	// about the transition for subsequent LLM calls.
	if len(modelSwitchItems) > 0 {
		_ = s.History.AddItem(modelSwitchItems[len(modelSwitchItems)-1])
	}

	// Update compaction tracking state
	s.CompactionCount++
	s.LastResponseID = ""
	s.lastSentHistoryLen = 0
	s.compactedThisTurn = true

	// Track token usage from compaction
	s.TotalTokens += compactResult.TokenUsage.TotalTokens
	s.TotalCachedTokens += compactResult.TokenUsage.CachedTokens

	logger.Info("Context compaction completed",
		"compaction_count", s.CompactionCount,
		"new_history_items", len(compactResult.Items),
		"compaction_tokens", compactResult.TokenUsage.TotalTokens)

	return nil
}
