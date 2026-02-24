package workflow

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mfateev/temporal-agent-harness/internal/tools"
)

func TestResolveRetryPolicy_NonRetryable(t *testing.T) {
	specs := map[string]tools.ToolSpec{
		"shell_command": {
			Name:        "shell_command",
			RetryPolicy: tools.RetryNone,
		},
	}

	policy := resolveRetryPolicy(specs, "shell_command")
	assert.Equal(t, int32(1), policy.MaximumAttempts, "NonRetryable tools should get MaximumAttempts=1")
}

func TestResolveRetryPolicy_Retryable(t *testing.T) {
	specs := map[string]tools.ToolSpec{
		"read_file": {
			Name:        "read_file",
			RetryPolicy: tools.RetryDefault,
		},
	}

	policy := resolveRetryPolicy(specs, "read_file")
	assert.Equal(t, int32(3), policy.MaximumAttempts)
	assert.Equal(t, time.Second, policy.InitialInterval)
	assert.Equal(t, 2.0, policy.BackoffCoefficient)
	assert.Equal(t, time.Minute, policy.MaximumInterval)
}

func TestResolveRetryPolicy_CustomMaxAttempts(t *testing.T) {
	specs := map[string]tools.ToolSpec{
		"custom_tool": {
			Name:        "custom_tool",
			RetryPolicy: &tools.ToolRetryPolicy{MaxAttempts: 5},
		},
	}

	policy := resolveRetryPolicy(specs, "custom_tool")
	assert.Equal(t, int32(5), policy.MaximumAttempts)
}

func TestResolveRetryPolicy_NilPolicy_UsesDefault(t *testing.T) {
	specs := map[string]tools.ToolSpec{
		"mcp__echo__echo": {
			Name: "mcp__echo__echo",
			// RetryPolicy is nil — should use default
		},
	}

	policy := resolveRetryPolicy(specs, "mcp__echo__echo")
	assert.Equal(t, int32(3), policy.MaximumAttempts, "nil RetryPolicy should fall back to default 3 attempts")
	assert.Equal(t, time.Second, policy.InitialInterval)
}

func TestResolveRetryPolicy_UnknownTool_UsesDefault(t *testing.T) {
	specs := map[string]tools.ToolSpec{}

	policy := resolveRetryPolicy(specs, "unknown_tool")
	assert.Equal(t, int32(3), policy.MaximumAttempts, "Unknown tools should get default 3 attempts")
}

func TestResolveRetryPolicy_AllBuiltinTools(t *testing.T) {
	// Verify each built-in tool has the expected retry behavior.
	nonRetryable := map[string]bool{
		"shell":         true,
		"shell_command": true,
		"write_file":    true,
		"apply_patch":   true,
		"exec_command":  true,
		"write_stdin":   true,
	}
	retryable := map[string]bool{
		"read_file":  true,
		"list_dir":   true,
		"grep_files": true,
	}

	allSpecs := tools.BuildSpecs([]string{
		"shell", "shell_command", "read_file", "write_file",
		"list_dir", "grep_files", "apply_patch", "exec_command", "write_stdin",
	})
	specByName := make(map[string]tools.ToolSpec, len(allSpecs))
	for _, s := range allSpecs {
		specByName[s.Name] = s
	}

	for name := range nonRetryable {
		policy := resolveRetryPolicy(specByName, name)
		assert.Equal(t, int32(1), policy.MaximumAttempts,
			"%s should be non-retryable (MaxAttempts=1)", name)
	}

	for name := range retryable {
		policy := resolveRetryPolicy(specByName, name)
		assert.Equal(t, int32(3), policy.MaximumAttempts,
			"%s should be retryable (MaxAttempts=3)", name)
	}
}
