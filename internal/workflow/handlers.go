// Package workflow contains Temporal workflow definitions.
//
// handlers.go registers all Temporal query and update handlers on the workflow.
// Handlers delegate coordination state to LoopControl and agent state to
// SessionState. No handler mutates LoopControl fields directly; they call
// typed methods (DeliverApproval, SetPendingUserInput, etc.).
package workflow

import (
	"fmt"

	"go.temporal.io/sdk/workflow"

	"github.com/mfateev/temporal-agent-harness/internal/models"
	"github.com/mfateev/temporal-agent-harness/internal/version"
)

// registerHandlers registers query and update handlers on the workflow.
func (s *SessionState) registerHandlers(ctx workflow.Context, ctrl *LoopControl) {
	logger := workflow.GetLogger(ctx)

	// Query: get_conversation_items
	// Maps to: Codex ContextManager::raw_items()
	err := workflow.SetQueryHandler(ctx, QueryGetConversationItems, func() ([]models.ConversationItem, error) {
		return s.History.GetRawItems()
	})
	if err != nil {
		logger.Error("Failed to register get_conversation_items query handler", "error", err)
	}

	// Query: get_turn_status
	// Returns current turn phase and stats for CLI polling.
	err = workflow.SetQueryHandler(ctx, QueryGetTurnStatus, func() (TurnStatus, error) {
		turnCount, _ := s.History.GetTurnCount()
		status := TurnStatus{
			Phase:                   ctrl.Phase(),
			CurrentTurnID:           ctrl.CurrentTurnID(),
			ToolsInFlight:           ctrl.ToolsInFlight(),
			PendingApprovals:        ctrl.PendingApprovals(),
			PendingEscalations:      ctrl.PendingEscalations(),
			PendingUserInputRequest: ctrl.PendingUserInputReq(),
			IterationCount:          s.IterationCount,
			TotalTokens:             s.TotalTokens,
			TotalCachedTokens:       s.TotalCachedTokens,
			TurnCount:               turnCount,
			WorkerVersion:           version.GitCommit,
			Suggestion:              ctrl.Suggestion(),
			Plan:                    s.Plan,
		}
		// Populate child agent summaries from AgentControl
		if s.AgentCtl != nil {
			for _, info := range s.AgentCtl.Agents {
				status.ChildAgents = append(status.ChildAgents, ChildAgentSummary{
					AgentID:    info.AgentID,
					WorkflowID: info.WorkflowID,
					Role:       info.Role,
					Status:     info.Status,
				})
			}
		}
		return status, nil
	})
	if err != nil {
		logger.Error("Failed to register get_turn_status query handler", "error", err)
	}

	// Update: user_input
	// Maps to: Codex Op::UserInput / turn/start
	err = workflow.SetUpdateHandlerWithOptions(
		ctx,
		UpdateUserInput,
		func(ctx workflow.Context, input UserInput) (UserInputAccepted, error) {
			turnID := generateTurnID(ctx)

			// Add TurnStarted marker
			if err := s.History.AddItem(models.ConversationItem{
				Type:   models.ItemTypeTurnStarted,
				TurnID: turnID,
			}); err != nil {
				return UserInputAccepted{}, fmt.Errorf("failed to add turn started: %w", err)
			}

			// Add user message
			if err := s.History.AddItem(models.ConversationItem{
				Type:    models.ItemTypeUserMessage,
				Content: input.Content,
				TurnID:  turnID,
			}); err != nil {
				return UserInputAccepted{}, fmt.Errorf("failed to add user message: %w", err)
			}

			ctrl.SetPendingUserInput(turnID)

			return UserInputAccepted{TurnID: turnID}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, input UserInput) error {
				if input.Content == "" {
					return fmt.Errorf("content must not be empty")
				}
				if ctrl.IsShutdown() {
					return fmt.Errorf("session is shutting down")
				}
				return nil
			},
		},
	)
	if err != nil {
		logger.Error("Failed to register user_input update handler", "error", err)
	}

	// Update: interrupt
	// Maps to: Codex Op::Interrupt
	err = workflow.SetUpdateHandlerWithOptions(
		ctx,
		UpdateInterrupt,
		func(ctx workflow.Context, req InterruptRequest) (InterruptResponse, error) {
			ctrl.SetInterrupted()

			// Add TurnComplete marker for interrupted turn
			if ctrl.CurrentTurnID() != "" {
				_ = s.History.AddItem(models.ConversationItem{
					Type:    models.ItemTypeTurnComplete,
					TurnID:  ctrl.CurrentTurnID(),
					Content: "interrupted",
				})
			}

			return InterruptResponse{Acknowledged: true}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, req InterruptRequest) error {
				if ctrl.IsShutdown() {
					return fmt.Errorf("session is shutting down")
				}
				return nil
			},
		},
	)
	if err != nil {
		logger.Error("Failed to register interrupt update handler", "error", err)
	}

	// Update: shutdown
	// Maps to: Codex Op::Shutdown
	err = workflow.SetUpdateHandlerWithOptions(
		ctx,
		UpdateShutdown,
		func(ctx workflow.Context, req ShutdownRequest) (ShutdownResponse, error) {
			ctrl.SetShutdown()
			return ShutdownResponse{Acknowledged: true}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, req ShutdownRequest) error {
				if ctrl.IsShutdown() {
					return fmt.Errorf("session is already shutting down")
				}
				return nil
			},
		},
	)
	if err != nil {
		logger.Error("Failed to register shutdown update handler", "error", err)
	}

	// Update: update_model
	// Allows the CLI to change the model used for subsequent LLM calls.
	err = workflow.SetUpdateHandlerWithOptions(
		ctx,
		UpdateModel,
		func(ctx workflow.Context, req UpdateModelRequest) (UpdateModelResponse, error) {
			// Save previous model info before overwriting.
			s.PreviousModel = s.Config.Model.Model
			s.PreviousContextWindow = s.Config.Model.ContextWindow

			// Apply new provider/model.
			s.Config.Model.Provider = req.Provider
			s.Config.Model.Model = req.Model

			// Re-resolve the model profile so ContextWindow, Temperature,
			// MaxTokens reflect the new model's defaults from the registry.
			s.resolveProfile()

			// If the caller supplied an explicit context window, override the profile.
			if req.ContextWindow > 0 {
				s.Config.Model.ContextWindow = req.ContextWindow
			}

			// Reset response chaining and incremental history tracking.
			s.LastResponseID = ""
			s.lastSentHistoryLen = 0

			// Flag for maybeCompactBeforeLLM to inject a model-switch message
			// and trigger proactive compaction if needed.
			s.modelSwitched = true

			return UpdateModelResponse{Acknowledged: true}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, req UpdateModelRequest) error {
				if req.Provider == "" {
					return fmt.Errorf("provider must not be empty")
				}
				if req.Model == "" {
					return fmt.Errorf("model must not be empty")
				}
				if ctrl.IsShutdown() {
					return fmt.Errorf("session is shutting down")
				}
				return nil
			},
		},
	)
	if err != nil {
		logger.Error("Failed to register update_model update handler", "error", err)
	}

	// Update: approval_response
	// Maps to: Codex approval flow (user approves/denies tool calls)
	err = workflow.SetUpdateHandlerWithOptions(
		ctx,
		UpdateApprovalResponse,
		func(ctx workflow.Context, resp ApprovalResponse) (ApprovalResponseAck, error) {
			ctrl.DeliverApproval(resp)
			return ApprovalResponseAck{}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, resp ApprovalResponse) error {
				if ctrl.Phase() != PhaseApprovalPending {
					return fmt.Errorf("no approval pending")
				}
				return nil
			},
		},
	)
	if err != nil {
		logger.Error("Failed to register approval_response update handler", "error", err)
	}

	// Update: escalation_response
	// Maps to: Codex on-failure escalation flow
	err = workflow.SetUpdateHandlerWithOptions(
		ctx,
		UpdateEscalationResponse,
		func(ctx workflow.Context, resp EscalationResponse) (EscalationResponseAck, error) {
			ctrl.DeliverEscalation(resp)
			return EscalationResponseAck{}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, resp EscalationResponse) error {
				if ctrl.Phase() != PhaseEscalationPending {
					return fmt.Errorf("no escalation pending")
				}
				return nil
			},
		},
	)
	if err != nil {
		logger.Error("Failed to register escalation_response update handler", "error", err)
	}

	// Update: compact
	// Triggers manual context compaction from the CLI /compact command.
	err = workflow.SetUpdateHandlerWithOptions(
		ctx,
		UpdateCompact,
		func(ctx workflow.Context, req CompactRequest) (CompactResponse, error) {
			ctrl.SetCompactRequested()
			return CompactResponse{Acknowledged: true}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, req CompactRequest) error {
				if ctrl.IsShutdown() {
					return fmt.Errorf("session is shutting down")
				}
				if ctrl.Phase() == PhaseCompacting {
					return fmt.Errorf("compaction already in progress")
				}
				return nil
			},
		},
	)
	if err != nil {
		logger.Error("Failed to register compact update handler", "error", err)
	}

	// Update: user_input_question_response
	// Maps to: Codex request_user_input flow (user answers multi-choice questions)
	err = workflow.SetUpdateHandlerWithOptions(
		ctx,
		UpdateUserInputQuestionResponse,
		func(ctx workflow.Context, resp UserInputQuestionResponse) (UserInputQuestionResponseAck, error) {
			ctrl.DeliverUserInputQ(resp)
			return UserInputQuestionResponseAck{}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, resp UserInputQuestionResponse) error {
				if ctrl.Phase() != PhaseUserInputPending {
					return fmt.Errorf("no user input question pending")
				}
				return nil
			},
		},
	)
	if err != nil {
		logger.Error("Failed to register user_input_question_response update handler", "error", err)
	}

	// Update: plan_request
	// Spawns a planner child workflow directly (no LLM round-trip) and returns
	// its workflow ID so the CLI can communicate with it.
	err = workflow.SetUpdateHandlerWithOptions(
		ctx,
		UpdatePlanRequest,
		func(ctx workflow.Context, req PlanRequest) (PlanRequestAccepted, error) {
			childDepth := s.AgentCtl.ParentDepth + 1
			if childDepth > MaxThreadSpawnDepth {
				return PlanRequestAccepted{}, fmt.Errorf("cannot spawn planner: maximum nesting depth (%d) exceeded", MaxThreadSpawnDepth)
			}

			agentID := nextAgentID(ctx)

			// Build planner child workflow input
			childInput := buildAgentSpawnConfig(s.Config, AgentRolePlanner, req.Message, childDepth)

			// Register agent info
			info := &AgentInfo{
				AgentID:     agentID,
				Role:        AgentRolePlanner,
				Status:      AgentStatusPendingInit,
				TaskMessage: req.Message,
			}
			s.AgentCtl.Agents[agentID] = info

			// Start child workflow
			childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
				WorkflowID: s.ConversationID + "/" + agentID,
			})

			future := workflow.ExecuteChildWorkflow(childCtx, "AgenticWorkflow", childInput)

			// Get the child workflow execution info
			var childExec workflow.Execution
			if err := future.GetChildWorkflowExecution().Get(ctx, &childExec); err != nil {
				info.Status = AgentStatusErrored
				return PlanRequestAccepted{}, fmt.Errorf("failed to start planner workflow: %w", err)
			}

			info.WorkflowID = childExec.ID
			info.RunID = childExec.RunID
			info.Status = AgentStatusRunning

			// Store future and start watcher
			s.AgentCtl.childFutures[agentID] = future
			s.startChildCompletionWatcher(ctx, agentID, future)

			logger.Info("Spawned planner agent",
				"agent_id", agentID,
				"child_workflow_id", childExec.ID)

			return PlanRequestAccepted{
				AgentID:    agentID,
				WorkflowID: childExec.ID,
			}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, req PlanRequest) error {
				if req.Message == "" {
					return fmt.Errorf("message must not be empty")
				}
				if ctrl.IsShutdown() {
					return fmt.Errorf("session is shutting down")
				}
				return nil
			},
		},
	)
	if err != nil {
		logger.Error("Failed to register plan_request update handler", "error", err)
	}

	// Signal channels for child workflow mode (subagent).
	// These are drained in goroutines so signals are processed asynchronously.
	// Maps to: codex-rs/core/src/agent/control.rs agent signal handling

	// agent_input — delivers a message from parent to child workflow.
	agentInputCh := workflow.GetSignalChannel(ctx, SignalAgentInput)
	workflow.Go(ctx, func(gCtx workflow.Context) {
		for {
			var signal AgentInputSignal
			if !agentInputCh.Receive(gCtx, &signal) {
				return // channel closed
			}
			if signal.Interrupt {
				ctrl.SetInterrupted()
			}

			turnID := generateTurnID(gCtx)
			_ = s.History.AddItem(models.ConversationItem{
				Type:   models.ItemTypeTurnStarted,
				TurnID: turnID,
			})
			_ = s.History.AddItem(models.ConversationItem{
				Type:    models.ItemTypeUserMessage,
				Content: signal.Content,
				TurnID:  turnID,
			})

			ctrl.SetPendingUserInput(turnID)
		}
	})

	// agent_shutdown — requests this child workflow to shut down.
	agentShutdownCh := workflow.GetSignalChannel(ctx, SignalAgentShutdown)
	workflow.Go(ctx, func(gCtx workflow.Context) {
		var ignored interface{}
		if !agentShutdownCh.Receive(gCtx, &ignored) {
			return
		}
		ctrl.SetShutdown()
	})
}
