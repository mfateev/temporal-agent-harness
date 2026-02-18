// Package workflow contains Temporal workflow definitions.
//
// turn.go implements the single-turn agentic loop (LLM + tool execution).
// The main function runAgenticTurn delegates to focused sub-methods.
//
// Maps to: codex-rs/core/src/codex.rs run_sampling_request
package workflow

import (
	"errors"
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/mfateev/temporal-agent-harness/internal/activities"
	"github.com/mfateev/temporal-agent-harness/internal/models"
)

// runAgenticTurn runs a single agentic turn (LLM + tool loop).
// Returns (needsContinueAsNew, error).
//
// Maps to: codex-rs/core/src/codex.rs run_sampling_request
func (s *SessionState) runAgenticTurn(ctx workflow.Context) (bool, error) {
	logger := workflow.GetLogger(ctx)
	s.compactedThisTurn = false
	gate := NewApprovalGate(s.Config.ApprovalMode, s.ExecPolicyRules)
	executor := NewToolExecutor(s.ToolSpecs, s.Config.Cwd, s.Config.SessionTaskQueue)

	for s.IterationCount < s.MaxIterations {
		if s.Interrupted {
			logger.Info("Turn interrupted")
			return false, nil
		}
		logger.Info("Starting iteration", "iteration", s.IterationCount, "turn_id", s.CurrentTurnID)

		s.maybeCompactBeforeLLM(ctx)

		llmResult, err := s.callLLM(ctx)
		if err != nil {
			retry, handleErr := s.handleLLMError(ctx, err)
			if handleErr != nil {
				return false, handleErr
			}
			if retry {
				continue
			}
			return false, nil
		}
		if s.Interrupted {
			logger.Info("Turn interrupted after LLM call")
			return false, nil
		}

		s.recordLLMResponse(ctx, llmResult)

		calls := extractFunctionCalls(llmResult.Items)
		calls, hadIntercepted, err := s.dispatchInterceptedCalls(ctx, calls)
		if err != nil {
			return false, err
		}
		if hadIntercepted && len(calls) == 0 {
			if s.Interrupted || s.ShutdownRequested {
				return false, nil
			}
			s.IterationCount++
			continue
		}

		if len(calls) > 0 {
			if s.detectRepeatedToolCalls(calls) {
				logger.Warn("Detected repeated identical tool calls", "repeat_count", s.repeatCount)
				_ = s.History.AddItem(models.ConversationItem{
					Type:    models.ItemTypeAssistantMessage,
					Content: "[Turn ended: detected repeated identical tool calls. Please try a different approach.]",
				})
				return false, nil
			}
			allDenied, execErr := s.approveAndExecuteTools(ctx, gate, executor, calls)
			if execErr != nil {
				return false, execErr
			}
			if allDenied {
				return false, nil
			}
			if s.Interrupted {
				logger.Info("Turn interrupted after tool execution")
				return false, nil
			}
			s.IterationCount++
			continue
		}

		// No tool calls — check finish reason
		if llmResult.FinishReason == models.FinishReasonStop {
			logger.Info("Turn completed", "iterations", s.IterationCount, "turn_id", s.CurrentTurnID)
			return false, nil
		}
		s.IterationCount++
		return false, nil
	}

	// Max iterations reached
	logger.Warn("Max iterations per turn reached", "iterations", s.IterationCount)
	_ = s.History.AddItem(models.ConversationItem{
		Type:    models.ItemTypeAssistantMessage,
		Content: fmt.Sprintf("[Turn ended: reached maximum of %d iterations without completing. The task may need to be broken into smaller steps.]", s.MaxIterations),
	})
	return false, nil
}

// effectiveAutoCompactLimit returns the auto-compact token limit, clamped to
// 90% of the context window. This prevents the configured limit from exceeding
// the model's actual context capacity (important after a model switch to a
// smaller context window).
func (s *SessionState) effectiveAutoCompactLimit() int {
	configured := s.Config.AutoCompactTokenLimit
	if configured <= 0 {
		return 0
	}
	contextLimit := s.Config.Model.ContextWindow * 9 / 10
	if contextLimit > 0 && contextLimit < configured {
		return contextLimit
	}
	return configured
}

// maybeCompactBeforeLLM performs proactive compaction if history exceeds the
// effective token limit. Also handles model-switch awareness: injects a
// developer message about the switch and triggers compaction if needed.
func (s *SessionState) maybeCompactBeforeLLM(ctx workflow.Context) {
	if s.compactedThisTurn {
		return
	}

	limit := s.effectiveAutoCompactLimit()
	logger := workflow.GetLogger(ctx)

	if s.modelSwitched {
		// Consume the flag so it fires only once.
		s.modelSwitched = false

		// Inject a developer message so the new model knows about the switch.
		switchMsg := fmt.Sprintf("<model_switch>\nThe user switched from model %q to %q "+
			"(context window: %d tokens). Continue the conversation seamlessly.\n</model_switch>",
			s.PreviousModel, s.Config.Model.Model, s.Config.Model.ContextWindow)
		_ = s.History.AddItem(models.ConversationItem{
			Type:    models.ItemTypeModelSwitch,
			Content: switchMsg,
		})
		// Reset incremental sends since we modified the history.
		s.lastSentHistoryLen = 0

		// Check if compaction is needed after model switch.
		if limit > 0 {
			estimated, _ := s.History.EstimateTokenCount()
			if estimated >= limit {
				logger.Info("Model-switch compaction triggered",
					"estimated_tokens", estimated,
					"limit", limit,
					"previous_model", s.PreviousModel,
					"new_model", s.Config.Model.Model)
				if err := s.performCompaction(ctx); err != nil {
					logger.Warn("Model-switch compaction failed, continuing without", "error", err)
				}
			}
		}
		return
	}

	// Standard proactive compaction check.
	if limit > 0 {
		estimated, _ := s.History.EstimateTokenCount()
		if estimated >= limit {
			logger.Info("Proactive compaction triggered",
				"estimated_tokens", estimated,
				"limit", limit)
			if err := s.performCompaction(ctx); err != nil {
				logger.Warn("Proactive compaction failed, continuing without", "error", err)
			}
		}
	}
}

// callLLM prepares incremental history and executes the LLM activity.
// Returns the LLM output or an error for handleLLMError to classify.
func (s *SessionState) callLLM(ctx workflow.Context) (*activities.LLMActivityOutput, error) {
	historyItems, err := s.History.GetForPrompt()
	if err != nil {
		return nil, fmt.Errorf("failed to get history: %w", err)
	}

	var inputItems []models.ConversationItem
	var previousResponseID string
	if s.LastResponseID != "" && s.lastSentHistoryLen > 0 && s.lastSentHistoryLen <= len(historyItems) {
		inputItems = historyItems[s.lastSentHistoryLen:]
		previousResponseID = s.LastResponseID
	} else {
		inputItems = historyItems
		previousResponseID = ""
	}

	llmActivityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	}
	llmCtx := workflow.WithActivityOptions(ctx, llmActivityOptions)

	s.Phase = PhaseLLMCalling
	s.ToolsInFlight = nil

	llmInput := activities.LLMActivityInput{
		History:               inputItems,
		ModelConfig:           s.Config.Model,
		ToolSpecs:             s.ToolSpecs,
		BaseInstructions:      s.Config.BaseInstructions,
		DeveloperInstructions: s.Config.DeveloperInstructions,
		UserInstructions:      s.Config.UserInstructions,
		PreviousResponseID:    previousResponseID,
	}

	var llmResult activities.LLMActivityOutput
	err = workflow.ExecuteActivity(llmCtx, "ExecuteLLMCall", llmInput).Get(ctx, &llmResult)
	if err != nil {
		return nil, err
	}
	return &llmResult, nil
}

// handleLLMError classifies and handles LLM errors: context overflow -> compact+retry,
// rate limit -> sleep+retry, fatal -> end turn. Returns (continueLoop, error).
func (s *SessionState) handleLLMError(ctx workflow.Context, err error) (bool, error) {
	logger := workflow.GetLogger(ctx)

	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) {
		switch appErr.Type() {
		case models.LLMErrTypeContextOverflow:
			logger.Warn("Context overflow, attempting compaction")
			if compactErr := s.performCompaction(ctx); compactErr != nil {
				logger.Warn("Compaction failed, falling back to destructive drop", "error", compactErr)
				turnCount, _ := s.History.GetTurnCount()
				keepTurns := turnCount / 2
				if keepTurns < 2 {
					keepTurns = 2
				}
				s.History.DropOldestUserTurns(keepTurns)
			}
			s.LastResponseID = ""
			s.lastSentHistoryLen = 0
			return true, nil // retry

		case models.LLMErrTypeAPILimit:
			logger.Warn("API rate limit, sleeping for 1 minute")
			workflow.Sleep(ctx, time.Minute)
			return true, nil // retry

		case models.LLMErrTypeFatal:
			logger.Error("Fatal LLM error, ending turn", "error", err)
			_ = s.History.AddItem(models.ConversationItem{
				Type:    models.ItemTypeAssistantMessage,
				Content: fmt.Sprintf("[Error: %s]", appErr.Message()),
				TurnID:  s.CurrentTurnID,
			})
			return false, nil // end turn
		}
	}

	// General activity error (timeout, unknown, etc.)
	logger.Error("LLM activity failed, ending turn", "error", err)
	_ = s.History.AddItem(models.ConversationItem{
		Type:    models.ItemTypeAssistantMessage,
		Content: fmt.Sprintf("[Error: LLM call failed: %v]", err),
		TurnID:  s.CurrentTurnID,
	})
	return false, nil // end turn
}

// recordLLMResponse adds response items to history, tracks tokens, and updates
// the response ID for incremental sends.
func (s *SessionState) recordLLMResponse(ctx workflow.Context, result *activities.LLMActivityOutput) {
	logger := workflow.GetLogger(ctx)

	s.TotalTokens += result.TokenUsage.TotalTokens
	s.TotalCachedTokens += result.TokenUsage.CachedTokens
	logger.Info("LLM call completed",
		"tokens", result.TokenUsage.TotalTokens,
		"cached_tokens", result.TokenUsage.CachedTokens,
		"cache_creation_tokens", result.TokenUsage.CacheCreationTokens,
		"finish_reason", result.FinishReason,
		"items", len(result.Items))

	for _, item := range result.Items {
		_ = s.History.AddItem(item)
	}
	if result.ResponseID != "" {
		s.LastResponseID = result.ResponseID
		allItems, _ := s.History.GetForPrompt()
		s.lastSentHistoryLen = len(allItems)
	}
}

// dispatchInterceptedCalls processes workflow-handled tool calls (request_user_input
// and collab tools), returning the remaining normal calls and whether any were intercepted.
func (s *SessionState) dispatchInterceptedCalls(ctx workflow.Context, calls []models.ConversationItem) (remaining []models.ConversationItem, hadIntercepted bool, err error) {
	if len(calls) == 0 {
		return calls, false, nil
	}

	var normalCalls []models.ConversationItem
	for _, fc := range calls {
		if fc.Name == "request_user_input" {
			hadIntercepted = true
			outputItem, callErr := s.handleRequestUserInput(ctx, fc)
			if callErr != nil {
				return nil, hadIntercepted, callErr
			}
			if addErr := s.History.AddItem(outputItem); addErr != nil {
				return nil, hadIntercepted, fmt.Errorf("failed to add user input response: %w", addErr)
			}
		} else if fc.Name == "update_plan" {
			hadIntercepted = true
			outputItem, callErr := s.handleUpdatePlan(ctx, fc)
			if callErr != nil {
				return nil, hadIntercepted, callErr
			}
			if addErr := s.History.AddItem(outputItem); addErr != nil {
				return nil, hadIntercepted, fmt.Errorf("failed to add update_plan response: %w", addErr)
			}
		} else if isCollabToolCall(fc.Name) {
			hadIntercepted = true
			outputItem, callErr := s.handleCollabToolCall(ctx, fc)
			if callErr != nil {
				return nil, hadIntercepted, callErr
			}
			if addErr := s.History.AddItem(outputItem); addErr != nil {
				return nil, hadIntercepted, fmt.Errorf("failed to add collab tool response: %w", addErr)
			}
		} else {
			normalCalls = append(normalCalls, fc)
		}
	}
	return normalCalls, hadIntercepted, nil
}

// approveAndExecuteTools runs the full pipeline: classify -> filter forbidden ->
// wait for approval -> execute -> escalate -> record results.
// Returns (allDenied, error). allDenied=true means all tools were denied by user.
func (s *SessionState) approveAndExecuteTools(
	ctx workflow.Context,
	gate *ApprovalGate,
	executor *ToolExecutor,
	functionCalls []models.ConversationItem,
) (bool, error) {
	logger := workflow.GetLogger(ctx)

	// Classify which tools need approval
	needsApproval, forbiddenResults := gate.Classify(functionCalls)

	// Record forbidden results and filter them out
	functionCalls = s.recordForbiddenAndFilter(functionCalls, forbiddenResults)
	if len(functionCalls) == 0 {
		return false, nil // all forbidden — iteration continues
	}

	// Wait for approval if needed
	if len(needsApproval) > 0 {
		var err error
		functionCalls, err = s.waitForApprovalAndFilter(ctx, functionCalls, gate, needsApproval)
		if err != nil {
			return false, err
		}
		if len(functionCalls) == 0 {
			return true, nil // all denied by user — end turn
		}
	}

	// Execute tools
	s.Phase = PhaseToolExecuting
	toolNames := make([]string, len(functionCalls))
	for i, fc := range functionCalls {
		toolNames[i] = fc.Name
	}
	s.ToolsInFlight = toolNames
	logger.Info("Executing tools", "count", len(functionCalls))

	toolResults, err := executor.ExecuteParallel(ctx, functionCalls)
	if err != nil {
		_ = s.History.AddItem(models.ConversationItem{
			Type:    models.ItemTypeAssistantMessage,
			Content: fmt.Sprintf("[Error: tool execution failed: %v]", err),
			TurnID:  s.CurrentTurnID,
		})
		return false, nil
	}

	s.ToolsInFlight = nil

	// On-failure mode escalation
	if s.Config.ApprovalMode == models.ApprovalOnFailure {
		toolResults, err = s.handleOnFailureEscalation(ctx, functionCalls, toolResults)
		if err != nil {
			return false, err
		}
	}

	// Record results
	s.recordToolResults(functionCalls, toolResults)
	return false, nil
}

// recordForbiddenAndFilter adds forbidden results to history and removes those
// tool calls from the list. Returns the remaining allowed calls.
func (s *SessionState) recordForbiddenAndFilter(
	calls []models.ConversationItem,
	forbidden []models.ConversationItem,
) []models.ConversationItem {
	for _, fr := range forbidden {
		_ = s.History.AddItem(fr)
	}

	if len(forbidden) == 0 {
		return calls
	}

	forbiddenIDs := make(map[string]bool, len(forbidden))
	for _, fr := range forbidden {
		forbiddenIDs[fr.CallID] = true
	}

	var remaining []models.ConversationItem
	for _, fc := range calls {
		if !forbiddenIDs[fc.CallID] {
			remaining = append(remaining, fc)
		}
	}
	return remaining
}

// waitForApprovalAndFilter sets the pending state, awaits the user's response,
// applies the approval decision, and returns the remaining approved calls.
func (s *SessionState) waitForApprovalAndFilter(
	ctx workflow.Context,
	calls []models.ConversationItem,
	gate *ApprovalGate,
	needsApproval []PendingApproval,
) ([]models.ConversationItem, error) {
	logger := workflow.GetLogger(ctx)

	s.Phase = PhaseApprovalPending
	s.PendingApprovals = needsApproval
	s.ApprovalReceived = false
	s.ApprovalResponse = nil

	logger.Info("Waiting for tool approval", "count", len(needsApproval))

	err := workflow.Await(ctx, func() bool {
		return s.ApprovalReceived || s.Interrupted || s.ShutdownRequested
	})
	if err != nil {
		return nil, fmt.Errorf("approval await failed: %w", err)
	}

	s.PendingApprovals = nil

	if s.Interrupted || s.ShutdownRequested {
		logger.Info("Approval wait interrupted")
		return nil, nil
	}

	// Apply decision
	approved, deniedResults := gate.ApplyDecision(calls, s.ApprovalResponse)

	for _, dr := range deniedResults {
		_ = s.History.AddItem(dr)
	}

	return approved, nil
}

// recordToolResults tracks which tools were executed and adds their outputs to history.
func (s *SessionState) recordToolResults(calls []models.ConversationItem, results []activities.ToolActivityOutput) {
	for _, fc := range calls {
		s.ToolCallsExecuted = append(s.ToolCallsExecuted, fc.Name)
	}

	for _, result := range results {
		item := models.ConversationItem{
			Type:   models.ItemTypeFunctionCallOutput,
			CallID: result.CallID,
			Output: &models.FunctionCallOutputPayload{
				Content: result.Content,
				Success: result.Success,
			},
		}
		_ = s.History.AddItem(item)
	}
}

// detectRepeatedToolCalls checks whether the current batch of tool calls is
// identical to the previous batch. Returns true if the same batch has been
// seen maxRepeatToolCalls times consecutively, indicating a tight loop.
func (s *SessionState) detectRepeatedToolCalls(calls []models.ConversationItem) bool {
	key := toolCallsKey(calls)
	if key == s.lastToolKey {
		s.repeatCount++
	} else {
		s.lastToolKey = key
		s.repeatCount = 1
	}
	return s.repeatCount >= maxRepeatToolCalls
}
