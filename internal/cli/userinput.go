package cli

import (
	"fmt"
	"strings"

	"github.com/mfateev/codex-temporal-go/internal/workflow"
)

// HandleUserInputQuestionInput parses the user's response to a request_user_input prompt.
// For single-question requests, typing a number selects that option and auto-submits.
// For multi-question requests, the same numeric selection applies to each question
// sequentially (future enhancement: sequential prompting).
//
// Returns nil if the input is not recognized (invalid number, out of range, etc.).
func HandleUserInputQuestionInput(line string, req *workflow.PendingUserInputRequest) *workflow.UserInputQuestionResponse {
	line = strings.TrimSpace(line)
	if line == "" || req == nil || len(req.Questions) == 0 {
		return nil
	}

	// Single question: user types a number to select an option, or freeform text
	if len(req.Questions) == 1 {
		q := req.Questions[0]

		// Try parsing as a number (1-based index)
		var idx int
		if n, err := fmt.Sscanf(line, "%d", &idx); err == nil && n == 1 {
			if idx < 1 || idx > len(q.Options) {
				return nil // out of range
			}
			return &workflow.UserInputQuestionResponse{
				Answers: map[string]workflow.UserInputQuestionAnswer{
					q.ID: {Answers: []string{q.Options[idx-1].Label}},
				},
			}
		}

		// Treat as freeform text answer
		return &workflow.UserInputQuestionResponse{
			Answers: map[string]workflow.UserInputQuestionAnswer{
				q.ID: {Answers: []string{line}},
			},
		}
	}

	// Multi-question: parse comma-separated numbers "1,2,3" mapping to Q1→opt1, Q2→opt2, Q3→opt3
	parts := strings.Split(line, ",")
	if len(parts) != len(req.Questions) {
		// Also try freeform: treat entire input as answer for first question
		// For multi-question, require exact match of comma-separated indices
		return nil
	}

	answers := make(map[string]workflow.UserInputQuestionAnswer, len(req.Questions))
	for i, part := range parts {
		part = strings.TrimSpace(part)
		q := req.Questions[i]

		var idx int
		if n, err := fmt.Sscanf(part, "%d", &idx); err == nil && n == 1 {
			if idx < 1 || idx > len(q.Options) {
				return nil // out of range
			}
			answers[q.ID] = workflow.UserInputQuestionAnswer{Answers: []string{q.Options[idx-1].Label}}
		} else {
			// Freeform text for this question
			answers[q.ID] = workflow.UserInputQuestionAnswer{Answers: []string{part}}
		}
	}

	return &workflow.UserInputQuestionResponse{Answers: answers}
}
