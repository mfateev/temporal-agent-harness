package cli

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/serviceerror"

	"github.com/mfateev/codex-temporal-go/internal/workflow"
)

func TestClassifyPollError_NotFound(t *testing.T) {
	err := serviceerror.NewNotFound("workflow not found")
	assert.Equal(t, pollErrorCompleted, classifyPollError(err))
}

func TestClassifyPollError_WorkflowNotReady(t *testing.T) {
	err := &serviceerror.WorkflowNotReady{Message: "workflow not ready"}
	assert.Equal(t, pollErrorTransient, classifyPollError(err))
}

func TestClassifyPollError_QueryFailed(t *testing.T) {
	err := &serviceerror.QueryFailed{Message: "query rejected"}
	assert.Equal(t, pollErrorTransient, classifyPollError(err))
}

func TestClassifyPollError_AlreadyCompleted(t *testing.T) {
	err := fmt.Errorf("workflow execution already completed")
	assert.Equal(t, pollErrorCompleted, classifyPollError(err))
}

func TestClassifyPollError_UnknownError(t *testing.T) {
	err := fmt.Errorf("some unexpected error")
	assert.Equal(t, pollErrorFatal, classifyPollError(err))
}

func TestClassifyPollError_WrappedNotFound(t *testing.T) {
	inner := serviceerror.NewNotFound("workflow not found")
	err := fmt.Errorf("query failed: %w", inner)
	assert.Equal(t, pollErrorCompleted, classifyPollError(err))
}

// --- Approval input handling tests ---

func TestHandleApprovalInput_Yes(t *testing.T) {
	app := &App{
		pendingApprovals: []workflow.PendingApproval{
			{CallID: "c1", ToolName: "shell"},
			{CallID: "c2", ToolName: "write_file"},
		},
	}
	resp := app.handleApprovalInput("y")
	require.NotNil(t, resp)
	assert.Equal(t, []string{"c1", "c2"}, resp.Approved)
	assert.Nil(t, resp.Denied)
}

func TestHandleApprovalInput_YesFull(t *testing.T) {
	app := &App{
		pendingApprovals: []workflow.PendingApproval{
			{CallID: "c1", ToolName: "shell"},
		},
	}
	resp := app.handleApprovalInput("yes")
	require.NotNil(t, resp)
	assert.Equal(t, []string{"c1"}, resp.Approved)
}

func TestHandleApprovalInput_No(t *testing.T) {
	app := &App{
		pendingApprovals: []workflow.PendingApproval{
			{CallID: "c1", ToolName: "shell"},
		},
	}
	resp := app.handleApprovalInput("n")
	require.NotNil(t, resp)
	assert.Nil(t, resp.Approved)
	assert.Equal(t, []string{"c1"}, resp.Denied)
}

func TestHandleApprovalInput_NoFull(t *testing.T) {
	app := &App{
		pendingApprovals: []workflow.PendingApproval{
			{CallID: "c1", ToolName: "shell"},
		},
	}
	resp := app.handleApprovalInput("no")
	require.NotNil(t, resp)
	assert.Equal(t, []string{"c1"}, resp.Denied)
}

func TestHandleApprovalInput_Always(t *testing.T) {
	app := &App{
		pendingApprovals: []workflow.PendingApproval{
			{CallID: "c1", ToolName: "shell"},
		},
	}
	assert.False(t, app.autoApprove)
	resp := app.handleApprovalInput("a")
	require.NotNil(t, resp)
	assert.Equal(t, []string{"c1"}, resp.Approved)
	assert.True(t, app.autoApprove, "autoApprove should be set after 'always'")
}

func TestHandleApprovalInput_AlwaysFull(t *testing.T) {
	app := &App{
		pendingApprovals: []workflow.PendingApproval{
			{CallID: "c1", ToolName: "shell"},
		},
	}
	resp := app.handleApprovalInput("always")
	require.NotNil(t, resp)
	assert.Equal(t, []string{"c1"}, resp.Approved)
	assert.True(t, app.autoApprove)
}

func TestHandleApprovalInput_Invalid(t *testing.T) {
	app := &App{
		pendingApprovals: []workflow.PendingApproval{
			{CallID: "c1", ToolName: "shell"},
		},
	}
	resp := app.handleApprovalInput("maybe")
	assert.Nil(t, resp)
}

func TestHandleApprovalInput_CaseInsensitive(t *testing.T) {
	app := &App{
		pendingApprovals: []workflow.PendingApproval{
			{CallID: "c1", ToolName: "shell"},
		},
	}
	resp := app.handleApprovalInput("YES")
	require.NotNil(t, resp)
	assert.Equal(t, []string{"c1"}, resp.Approved)
}

func TestHandleApprovalInput_WithWhitespace(t *testing.T) {
	app := &App{
		pendingApprovals: []workflow.PendingApproval{
			{CallID: "c1", ToolName: "shell"},
		},
	}
	resp := app.handleApprovalInput("  y  ")
	require.NotNil(t, resp)
	assert.Equal(t, []string{"c1"}, resp.Approved)
}

func TestFormatApprovalDetail_Shell(t *testing.T) {
	detail := formatApprovalDetail("shell", `{"command": "rm -rf /tmp"}`)
	assert.Equal(t, "Command: rm -rf /tmp", detail)
}

func TestFormatApprovalDetail_WriteFile(t *testing.T) {
	detail := formatApprovalDetail("write_file", `{"file_path": "/home/user/test.txt", "content": "hello"}`)
	assert.Equal(t, "Path: /home/user/test.txt", detail)
}

func TestFormatApprovalDetail_ApplyPatch(t *testing.T) {
	detail := formatApprovalDetail("apply_patch", `{"file_path": "/home/user/test.txt"}`)
	assert.Equal(t, "Path: /home/user/test.txt", detail)
}

func TestFormatApprovalDetail_UnknownTool(t *testing.T) {
	detail := formatApprovalDetail("custom_tool", `{"foo": "bar"}`)
	assert.Equal(t, `Args: {"foo": "bar"}`, detail)
}

func TestFormatApprovalDetail_BadJSON(t *testing.T) {
	detail := formatApprovalDetail("shell", `{bad json`)
	assert.Contains(t, detail, "Args:")
}

func TestFormatApprovalDetail_LongArgs(t *testing.T) {
	longArg := ""
	for i := 0; i < 400; i++ {
		longArg += "x"
	}
	detail := formatApprovalDetail("custom_tool", longArg)
	assert.Contains(t, detail, "...")
	assert.LessOrEqual(t, len(detail), 310) // "Args: " + 300 + "..."
}

// --- Index-based approval tests ---

func TestHandleApprovalInput_IndexSingle(t *testing.T) {
	app := &App{
		pendingApprovals: []workflow.PendingApproval{
			{CallID: "c1", ToolName: "shell"},
			{CallID: "c2", ToolName: "write_file"},
			{CallID: "c3", ToolName: "apply_patch"},
		},
	}
	resp := app.handleApprovalInput("2")
	require.NotNil(t, resp)
	assert.Equal(t, []string{"c2"}, resp.Approved)
	assert.Equal(t, []string{"c1", "c3"}, resp.Denied)
}

func TestHandleApprovalInput_IndexMultiple(t *testing.T) {
	app := &App{
		pendingApprovals: []workflow.PendingApproval{
			{CallID: "c1", ToolName: "shell"},
			{CallID: "c2", ToolName: "write_file"},
			{CallID: "c3", ToolName: "apply_patch"},
		},
	}
	resp := app.handleApprovalInput("1,3")
	require.NotNil(t, resp)
	assert.Equal(t, []string{"c1", "c3"}, resp.Approved)
	assert.Equal(t, []string{"c2"}, resp.Denied)
}

func TestHandleApprovalInput_IndexWithSpaces(t *testing.T) {
	app := &App{
		pendingApprovals: []workflow.PendingApproval{
			{CallID: "c1", ToolName: "shell"},
			{CallID: "c2", ToolName: "write_file"},
		},
	}
	resp := app.handleApprovalInput("1, 2")
	require.NotNil(t, resp)
	assert.Equal(t, []string{"c1", "c2"}, resp.Approved)
	assert.Empty(t, resp.Denied)
}

func TestHandleApprovalInput_IndexOutOfRange(t *testing.T) {
	app := &App{
		pendingApprovals: []workflow.PendingApproval{
			{CallID: "c1", ToolName: "shell"},
		},
	}
	resp := app.handleApprovalInput("5")
	assert.Nil(t, resp)
}

func TestHandleApprovalInput_IndexZero(t *testing.T) {
	app := &App{
		pendingApprovals: []workflow.PendingApproval{
			{CallID: "c1", ToolName: "shell"},
		},
	}
	resp := app.handleApprovalInput("0")
	assert.Nil(t, resp)
}

func TestParseApprovalIndices_Valid(t *testing.T) {
	assert.Equal(t, []int{1, 3}, parseApprovalIndices("1,3", 3))
	assert.Equal(t, []int{2}, parseApprovalIndices("2", 3))
	assert.Equal(t, []int{1, 2, 3}, parseApprovalIndices("1,2,3", 3))
}

func TestParseApprovalIndices_WithSpaces(t *testing.T) {
	assert.Equal(t, []int{1, 2}, parseApprovalIndices("1, 2", 3))
}

func TestParseApprovalIndices_Dedup(t *testing.T) {
	indices := parseApprovalIndices("1,1,2", 3)
	assert.Equal(t, []int{1, 2}, indices)
}

func TestParseApprovalIndices_Invalid(t *testing.T) {
	assert.Nil(t, parseApprovalIndices("abc", 3))
	assert.Nil(t, parseApprovalIndices("0", 3))
	assert.Nil(t, parseApprovalIndices("4", 3))
	assert.Nil(t, parseApprovalIndices("", 3))
	assert.Nil(t, parseApprovalIndices("-1", 3))
}
