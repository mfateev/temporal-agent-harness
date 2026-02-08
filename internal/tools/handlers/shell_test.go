package handlers

import (
	"context"
	"testing"

	"github.com/mfateev/codex-temporal-go/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShellTool_IsMutating_SafeCommand(t *testing.T) {
	tool := NewShellTool()
	invocation := &tools.ToolInvocation{
		Arguments: map[string]interface{}{"command": "ls -la"},
	}
	assert.False(t, tool.IsMutating(invocation), "ls should be classified as non-mutating")
}

func TestShellTool_IsMutating_UnsafeCommand(t *testing.T) {
	tool := NewShellTool()
	invocation := &tools.ToolInvocation{
		Arguments: map[string]interface{}{"command": "rm -rf /tmp/test"},
	}
	assert.True(t, tool.IsMutating(invocation), "rm should be classified as mutating")
}

func TestShellTool_IsMutating_MissingCommand(t *testing.T) {
	tool := NewShellTool()
	invocation := &tools.ToolInvocation{
		Arguments: map[string]interface{}{},
	}
	assert.True(t, tool.IsMutating(invocation), "missing command should be classified as mutating")
}

func TestShellTool_IsMutating_GitStatus(t *testing.T) {
	tool := NewShellTool()
	invocation := &tools.ToolInvocation{
		Arguments: map[string]interface{}{"command": "git status"},
	}
	assert.False(t, tool.IsMutating(invocation), "git status should be classified as non-mutating")
}

func TestShellTool_IsMutating_GitPushForce(t *testing.T) {
	tool := NewShellTool()
	invocation := &tools.ToolInvocation{
		Arguments: map[string]interface{}{"command": "git push --force"},
	}
	assert.True(t, tool.IsMutating(invocation), "git push --force should be classified as mutating")
}

func TestShellTool_Handle_Success(t *testing.T) {
	tool := NewShellTool()
	invocation := &tools.ToolInvocation{
		Arguments: map[string]interface{}{"command": "echo hello"},
	}
	output, err := tool.Handle(context.Background(), invocation)
	require.NoError(t, err)
	require.NotNil(t, output)
	assert.Equal(t, "hello\n", output.Content)
	require.NotNil(t, output.Success)
	assert.True(t, *output.Success)
}

func TestShellTool_Handle_Failure(t *testing.T) {
	tool := NewShellTool()
	invocation := &tools.ToolInvocation{
		Arguments: map[string]interface{}{"command": "exit 1"},
	}
	output, err := tool.Handle(context.Background(), invocation)
	require.NoError(t, err) // Non-zero exit is not a Go error
	require.NotNil(t, output)
	require.NotNil(t, output.Success)
	assert.False(t, *output.Success)
}

func TestShellTool_Handle_StderrCaptured(t *testing.T) {
	tool := NewShellTool()
	invocation := &tools.ToolInvocation{
		Arguments: map[string]interface{}{"command": "echo out && echo err >&2"},
	}
	output, err := tool.Handle(context.Background(), invocation)
	require.NoError(t, err)
	require.NotNil(t, output)
	// AggregateOutput concatenates stdout then stderr when under cap
	assert.Contains(t, output.Content, "out")
	assert.Contains(t, output.Content, "err")
}

func TestShellTool_Handle_MissingCommand(t *testing.T) {
	tool := NewShellTool()
	invocation := &tools.ToolInvocation{
		Arguments: map[string]interface{}{},
	}
	_, err := tool.Handle(context.Background(), invocation)
	require.Error(t, err)
	assert.True(t, tools.IsValidationError(err))
}

func TestShellTool_Handle_EmptyCommand(t *testing.T) {
	tool := NewShellTool()
	invocation := &tools.ToolInvocation{
		Arguments: map[string]interface{}{"command": ""},
	}
	_, err := tool.Handle(context.Background(), invocation)
	require.Error(t, err)
	assert.True(t, tools.IsValidationError(err))
}
