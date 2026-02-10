//go:build linux

package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildBwrapCommand_ReadOnly(t *testing.T) {
	spec := CommandSpec{Program: "bash", Args: []string{"-c", "ls"}, Cwd: "/home/user"}
	policy := &SandboxPolicy{Mode: ModeReadOnly, NetworkAccess: false}

	cmd, env, err := BuildBwrapCommand(spec, policy)
	require.NoError(t, err)

	assert.Equal(t, "bwrap", cmd[0])
	assert.Contains(t, cmd, "--ro-bind")
	assert.Contains(t, cmd, "--unshare-pid")
	assert.Contains(t, cmd, "--chdir")
	assert.Contains(t, cmd, "/home/user")
	// Command should end with the actual command
	assert.Equal(t, "bash", cmd[len(cmd)-3])
	assert.Equal(t, "-c", cmd[len(cmd)-2])
	assert.Equal(t, "ls", cmd[len(cmd)-1])

	assert.Equal(t, "1", env["CODEX_SANDBOX_NETWORK_DISABLED"])
}

func TestBuildBwrapCommand_WorkspaceWrite(t *testing.T) {
	spec := CommandSpec{Program: "bash", Args: []string{"-c", "echo hi"}, Cwd: "/workspace"}
	policy := &SandboxPolicy{
		Mode:          ModeWorkspaceWrite,
		WritableRoots: []WritableRoot{"/workspace", "/tmp/builds"},
		NetworkAccess: true,
	}

	cmd, env, err := BuildBwrapCommand(spec, policy)
	require.NoError(t, err)

	assert.Contains(t, cmd, "--ro-bind")
	// Should have bind mounts for writable roots
	bindCount := 0
	for i, arg := range cmd {
		if arg == "--bind" && i+2 < len(cmd) {
			bindCount++
		}
	}
	assert.Equal(t, 2, bindCount, "should have 2 writable bind mounts")

	// Network should be allowed
	_, hasNetDisabled := env["CODEX_SANDBOX_NETWORK_DISABLED"]
	assert.False(t, hasNetDisabled)
}

func TestBuildBwrapCommand_NoNetworkAccess(t *testing.T) {
	spec := CommandSpec{Program: "curl", Args: []string{"http://example.com"}}
	policy := &SandboxPolicy{Mode: ModeReadOnly, NetworkAccess: false}

	_, env, err := BuildBwrapCommand(spec, policy)
	require.NoError(t, err)
	assert.Equal(t, "1", env["CODEX_SANDBOX_NETWORK_DISABLED"])
}

func TestLinuxSandbox_Transform_FullAccess(t *testing.T) {
	s := &LinuxSandbox{}
	spec := CommandSpec{Program: "bash", Args: []string{"-c", "echo hello"}, Cwd: "/tmp"}
	env, err := s.Transform(spec, &SandboxPolicy{Mode: ModeFullAccess})
	require.NoError(t, err)
	// Should pass through without bwrap wrapping
	assert.Equal(t, []string{"bash", "-c", "echo hello"}, env.Command)
}

func TestLinuxSandbox_Transform_NilPolicy(t *testing.T) {
	s := &LinuxSandbox{}
	spec := CommandSpec{Program: "bash", Args: []string{"-c", "echo hello"}}
	env, err := s.Transform(spec, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"bash", "-c", "echo hello"}, env.Command)
}
