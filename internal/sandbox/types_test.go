package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSandboxMode(t *testing.T) {
	tests := []struct {
		input   string
		want    SandboxMode
		wantErr bool
	}{
		{"full-access", ModeFullAccess, false},
		{"full_access", ModeFullAccess, false},
		{"read-only", ModeReadOnly, false},
		{"read_only", ModeReadOnly, false},
		{"workspace-write", ModeWorkspaceWrite, false},
		{"workspace_write", ModeWorkspaceWrite, false},
		{"invalid", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseSandboxMode(tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestSandboxPolicy_IsRestricted(t *testing.T) {
	assert.False(t, (*SandboxPolicy)(nil).IsRestricted())
	assert.False(t, (&SandboxPolicy{Mode: ModeFullAccess}).IsRestricted())
	assert.False(t, (&SandboxPolicy{Mode: ""}).IsRestricted())
	assert.True(t, (&SandboxPolicy{Mode: ModeReadOnly}).IsRestricted())
	assert.True(t, (&SandboxPolicy{Mode: ModeWorkspaceWrite}).IsRestricted())
}

func TestNoopSandbox_Transform(t *testing.T) {
	noop := &NoopSandbox{}
	assert.True(t, noop.Available())

	spec := CommandSpec{Program: "bash", Args: []string{"-c", "echo hello"}, Cwd: "/tmp"}
	env, err := noop.Transform(spec, &SandboxPolicy{Mode: ModeReadOnly})
	require.NoError(t, err)
	assert.Equal(t, []string{"bash", "-c", "echo hello"}, env.Command)
	assert.Equal(t, "/tmp", env.Cwd)
}

func TestNewSandboxManager_ReturnsNonNil(t *testing.T) {
	mgr := NewSandboxManager()
	assert.NotNil(t, mgr)
	assert.True(t, mgr.Available())
}

func TestNewNoopSandboxManager(t *testing.T) {
	mgr := NewNoopSandboxManager()
	assert.NotNil(t, mgr)
	assert.True(t, mgr.Available())
}
