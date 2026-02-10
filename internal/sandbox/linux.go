//go:build linux

package sandbox

import (
	"fmt"
	"os/exec"
)

// LinuxSandbox uses bubblewrap (bwrap) for filesystem sandboxing on Linux.
//
// Maps to: codex-rs/core/src/sandbox/linux.rs
type LinuxSandbox struct{}

// Available returns true if bwrap is available on the system.
func (l *LinuxSandbox) Available() bool {
	_, err := exec.LookPath("bwrap")
	return err == nil
}

// Transform wraps the command with bwrap for filesystem isolation.
func (l *LinuxSandbox) Transform(spec CommandSpec, policy *SandboxPolicy) (*ExecEnv, error) {
	if policy == nil || !policy.IsRestricted() {
		return &ExecEnv{
			Command: append([]string{spec.Program}, spec.Args...),
			Cwd:     spec.Cwd,
		}, nil
	}

	cmd, env, err := buildBwrapCommand(spec, policy)
	if err != nil {
		return nil, err
	}

	return &ExecEnv{
		Command: cmd,
		Cwd:     spec.Cwd,
		Env:     env,
	}, nil
}

// buildBwrapCommand constructs the bwrap command for the given policy.
func buildBwrapCommand(spec CommandSpec, policy *SandboxPolicy) ([]string, map[string]string, error) {
	cmd := []string{"bwrap"}

	switch policy.Mode {
	case ModeReadOnly:
		// Read-only bind of root filesystem
		cmd = append(cmd, "--ro-bind", "/", "/")
		// Writable tmpfs for /tmp and /dev/shm
		cmd = append(cmd, "--tmpfs", "/tmp")
		cmd = append(cmd, "--dev", "/dev")
		cmd = append(cmd, "--proc", "/proc")

	case ModeWorkspaceWrite:
		// Read-only root
		cmd = append(cmd, "--ro-bind", "/", "/")
		cmd = append(cmd, "--tmpfs", "/tmp")
		cmd = append(cmd, "--dev", "/dev")
		cmd = append(cmd, "--proc", "/proc")
		// Writable bind mounts for specified roots
		for _, root := range policy.WritableRoots {
			path := string(root)
			cmd = append(cmd, "--bind", path, path)
		}

	default:
		return nil, nil, fmt.Errorf("unsupported sandbox mode: %s", policy.Mode)
	}

	// PID isolation
	cmd = append(cmd, "--unshare-pid")

	// Set working directory if specified
	if spec.Cwd != "" {
		cmd = append(cmd, "--chdir", spec.Cwd)
	}

	// Add the actual command
	cmd = append(cmd, "--")
	cmd = append(cmd, spec.Program)
	cmd = append(cmd, spec.Args...)

	// Environment variables for network policy
	env := make(map[string]string)
	if !policy.NetworkAccess {
		env["CODEX_SANDBOX_NETWORK_DISABLED"] = "1"
	}

	return cmd, env, nil
}

// BuildBwrapCommand is exported for testing.
func BuildBwrapCommand(spec CommandSpec, policy *SandboxPolicy) ([]string, map[string]string, error) {
	return buildBwrapCommand(spec, policy)
}
