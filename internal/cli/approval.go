package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.temporal.io/api/serviceerror"

	"github.com/mfateev/codex-temporal-go/internal/workflow"
)

// HandleApprovalInput parses the user's response to an approval prompt.
// Returns (response, setAutoApprove). Response is nil if input is not recognized.
//
// Supports:
//   - "y"/"yes" — approve all
//   - "n"/"no" — deny all
//   - "a"/"always" — approve all + set auto-approve flag
//   - "1,3" — approve indices 1 and 3, deny the rest
func HandleApprovalInput(line string, pending []workflow.PendingApproval) (*workflow.ApprovalResponse, bool) {
	line = strings.ToLower(strings.TrimSpace(line))

	allCallIDs := make([]string, len(pending))
	for i, ap := range pending {
		allCallIDs[i] = ap.CallID
	}

	switch line {
	case "y", "yes":
		return &workflow.ApprovalResponse{Approved: allCallIDs}, false
	case "n", "no":
		return &workflow.ApprovalResponse{Denied: allCallIDs}, false
	case "a", "always":
		return &workflow.ApprovalResponse{Approved: allCallIDs}, true
	}

	// Try index-based selection
	indices := parseApprovalIndices(line, len(pending))
	if indices == nil {
		return nil, false
	}

	approvedSet := make(map[int]bool, len(indices))
	for _, idx := range indices {
		approvedSet[idx] = true
	}

	var approved, denied []string
	for i, callID := range allCallIDs {
		if approvedSet[i+1] {
			approved = append(approved, callID)
		} else {
			denied = append(denied, callID)
		}
	}

	return &workflow.ApprovalResponse{Approved: approved, Denied: denied}, false
}

// HandleEscalationInput parses the user's response to an escalation prompt.
// Returns nil if the input is not recognized.
func HandleEscalationInput(line string, pending []workflow.EscalationRequest) *workflow.EscalationResponse {
	line = strings.ToLower(strings.TrimSpace(line))

	allCallIDs := make([]string, len(pending))
	for i, esc := range pending {
		allCallIDs[i] = esc.CallID
	}

	switch line {
	case "y", "yes":
		return &workflow.EscalationResponse{Approved: allCallIDs}
	case "n", "no":
		return &workflow.EscalationResponse{Denied: allCallIDs}
	}

	indices := parseApprovalIndices(line, len(pending))
	if indices == nil {
		return nil
	}

	approvedSet := make(map[int]bool, len(indices))
	for _, idx := range indices {
		approvedSet[idx] = true
	}

	var approved, denied []string
	for i, callID := range allCallIDs {
		if approvedSet[i+1] {
			approved = append(approved, callID)
		} else {
			denied = append(denied, callID)
		}
	}

	return &workflow.EscalationResponse{Approved: approved, Denied: denied}
}

// parseApprovalIndices parses a comma-separated list of 1-based indices.
// Returns nil if the input is not valid.
func parseApprovalIndices(input string, maxIndex int) []int {
	parts := strings.Split(input, ",")
	var indices []int
	seen := make(map[int]bool)

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var idx int
		n, err := fmt.Sscanf(part, "%d", &idx)
		if err != nil || n != 1 || idx < 1 || idx > maxIndex {
			return nil
		}
		if !seen[idx] {
			seen[idx] = true
			indices = append(indices, idx)
		}
	}

	if len(indices) == 0 {
		return nil
	}
	return indices
}

// ApprovalSelectionToResponse maps a selector index to an ApprovalResponse.
// Options: 0=approve all, 1=deny all, 2=always approve, 3=select individually (returns nil).
func ApprovalSelectionToResponse(selected int, pending []workflow.PendingApproval) (*workflow.ApprovalResponse, bool) {
	allCallIDs := make([]string, len(pending))
	for i, ap := range pending {
		allCallIDs[i] = ap.CallID
	}

	switch selected {
	case 0: // Yes, allow
		return &workflow.ApprovalResponse{Approved: allCallIDs}, false
	case 1: // No, deny
		return &workflow.ApprovalResponse{Denied: allCallIDs}, false
	case 2: // Always allow
		return &workflow.ApprovalResponse{Approved: allCallIDs}, true
	case 3: // Select individually (multi-tool only) - fall back to textarea
		return nil, false
	default:
		return nil, false
	}
}

// EscalationSelectionToResponse maps a selector index to an EscalationResponse.
// Options: 0=approve (re-run without sandbox), 1=deny.
func EscalationSelectionToResponse(selected int, pending []workflow.EscalationRequest) *workflow.EscalationResponse {
	allCallIDs := make([]string, len(pending))
	for i, esc := range pending {
		allCallIDs[i] = esc.CallID
	}

	switch selected {
	case 0: // Yes, re-run
		return &workflow.EscalationResponse{Approved: allCallIDs}
	case 1: // No, deny
		return &workflow.EscalationResponse{Denied: allCallIDs}
	default:
		return nil
	}
}

// formatApprovalDetail extracts a human-readable detail string from tool arguments.
func formatApprovalDetail(toolName, arguments string) string {
	var args map[string]interface{}
	if json.Unmarshal([]byte(arguments), &args) == nil {
		switch toolName {
		case "shell":
			if cmd, ok := args["command"].(string); ok {
				return "Command: " + cmd
			}
		case "write_file":
			if path, ok := args["file_path"].(string); ok {
				return "Path: " + path
			}
		case "apply_patch":
			if path, ok := args["file_path"].(string); ok {
				return "Path: " + path
			}
		case "read_file":
			if path, ok := args["file_path"].(string); ok {
				return "Path: " + path
			}
		case "list_dir":
			if path, ok := args["dir_path"].(string); ok {
				return "Path: " + path
			}
			if path, ok := args["path"].(string); ok {
				return "Path: " + path
			}
		case "grep_files":
			if pat, ok := args["pattern"].(string); ok {
				detail := "Pattern: " + pat
				if dir, ok := args["path"].(string); ok {
					detail += " in " + dir
				}
				return detail
			}
		}
	}
	display := arguments
	if len(display) > 300 {
		display = display[:300] + "..."
	}
	return "Args: " + display
}

// pollErrorKind classifies errors from workflow queries.
type pollErrorKind int

const (
	pollErrorTransient pollErrorKind = iota
	pollErrorCompleted
	pollErrorFatal
)

// classifyPollError categorizes a poll error using Temporal SDK typed errors.
func classifyPollError(err error) pollErrorKind {
	var notFoundErr *serviceerror.NotFound
	if errors.As(err, &notFoundErr) {
		return pollErrorCompleted
	}

	var notReadyErr *serviceerror.WorkflowNotReady
	if errors.As(err, &notReadyErr) {
		return pollErrorTransient
	}

	var queryFailedErr *serviceerror.QueryFailed
	if errors.As(err, &queryFailedErr) {
		return pollErrorTransient
	}

	if strings.Contains(err.Error(), "workflow execution already completed") {
		return pollErrorCompleted
	}

	return pollErrorFatal
}
