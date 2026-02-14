// Package workflow contains Temporal workflow definitions.
//
// handlers.go registers all Temporal query and update handlers on the workflow.
package workflow

import (
	"fmt"

	"go.temporal.io/sdk/workflow"

	"github.com/mfateev/temporal-agent-harness/internal/models"
	"github.com/mfateev/temporal-agent-harness/internal/version"
)

// registerHandlers registers query and update handlers on the workflow.
func (s *SessionState) registerHandlers(ctx workflow.Context) {
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
			Phase:                   s.Phase,
			CurrentTurnID:           s.CurrentTurnID,
			ToolsInFlight:           s.ToolsInFlight,
			PendingApprovals:        s.PendingApprovals,
			PendingEscalations:      s.PendingEscalations,
			PendingUserInputRequest: s.PendingUserInputReq,
			IterationCount:          s.IterationCount,
			TotalTokens:             s.TotalTokens,
			TotalCachedTokens:       s.TotalCachedTokens,
			TurnCount:               turnCount,
			WorkerVersion:           version.GitCommit,
			Suggestion:              s.Suggestion,
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

			s.CurrentTurnID = turnID
			s.PendingUserInput = true

			return UserInputAccepted{TurnID: turnID}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, input UserInput) error {
				if input.Content == "" {
					return fmt.Errorf("content must not be empty")
				}
				if s.ShutdownRequested {
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
			s.Interrupted = true

			// Add TurnComplete marker for interrupted turn
			if s.CurrentTurnID != "" {
				_ = s.History.AddItem(models.ConversationItem{
					Type:    models.ItemTypeTurnComplete,
					TurnID:  s.CurrentTurnID,
					Content: "interrupted",
				})
			}

			return InterruptResponse{Acknowledged: true}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, req InterruptRequest) error {
				if s.ShutdownRequested {
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
			s.ShutdownRequested = true
			s.Interrupted = true // Also interrupt current turn
			return ShutdownResponse{Acknowledged: true}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, req ShutdownRequest) error {
				if s.ShutdownRequested {
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
			s.Config.Model.Provider = req.Provider
			s.Config.Model.Model = req.Model
			// Reset response chaining when switching models.
			s.LastResponseID = ""
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
				if s.ShutdownRequested {
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
			s.ApprovalResponse = &resp
			s.ApprovalReceived = true
			// Clear PendingApprovals immediately so the query handler reflects
			// the response before the main coroutine wakes from Await.
			// Without this, the CLI may poll and re-enter the approval prompt.
			s.PendingApprovals = nil
			return ApprovalResponseAck{}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, resp ApprovalResponse) error {
				if s.Phase != PhaseApprovalPending {
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
			s.EscalationResponse = &resp
			s.EscalationReceived = true
			s.PendingEscalations = nil
			return EscalationResponseAck{}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, resp EscalationResponse) error {
				if s.Phase != PhaseEscalationPending {
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
			s.CompactRequested = true
			return CompactResponse{Acknowledged: true}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, req CompactRequest) error {
				if s.ShutdownRequested {
					return fmt.Errorf("session is shutting down")
				}
				if s.Phase == PhaseCompacting {
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
			s.UserInputQResponse = &resp
			s.UserInputQReceived = true
			s.PendingUserInputReq = nil
			return UserInputQuestionResponseAck{}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context, resp UserInputQuestionResponse) error {
				if s.Phase != PhaseUserInputPending {
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
				if s.ShutdownRequested {
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
				s.Interrupted = true
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

			s.CurrentTurnID = turnID
			s.PendingUserInput = true
		}
	})

	// agent_shutdown — requests this child workflow to shut down.
	agentShutdownCh := workflow.GetSignalChannel(ctx, SignalAgentShutdown)
	workflow.Go(ctx, func(gCtx workflow.Context) {
		var ignored interface{}
		if !agentShutdownCh.Receive(gCtx, &ignored) {
			return
		}
		s.ShutdownRequested = true
		s.Interrupted = true
	})
}
