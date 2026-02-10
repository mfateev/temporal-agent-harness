//go:build darwin

package sandbox

import (
	"fmt"
	"os/exec"
	"strings"
)

// SeatbeltSandbox uses macOS Seatbelt (sandbox-exec) for sandboxing.
//
// Maps to: codex-rs/core/src/sandbox/seatbelt.rs
type SeatbeltSandbox struct{}

// Available returns true if sandbox-exec is available on the system.
func (s *SeatbeltSandbox) Available() bool {
	_, err := exec.LookPath("/usr/bin/sandbox-exec")
	return err == nil
}

// Transform wraps the command with sandbox-exec and an SBPL policy.
func (s *SeatbeltSandbox) Transform(spec CommandSpec, policy *SandboxPolicy) (*ExecEnv, error) {
	if policy == nil || !policy.IsRestricted() {
		// No restrictions — pass through
		return &ExecEnv{
			Command: append([]string{spec.Program}, spec.Args...),
			Cwd:     spec.Cwd,
		}, nil
	}

	sbpl := generateSBPL(policy)

	// Build sandbox-exec command
	cmd := []string{"/usr/bin/sandbox-exec", "-p", sbpl, "--", spec.Program}
	cmd = append(cmd, spec.Args...)

	return &ExecEnv{
		Command: cmd,
		Cwd:     spec.Cwd,
	}, nil
}

// generateSBPL generates a Seatbelt Profile Language policy string.
//
// Maps to: codex-rs/core/src/sandbox/seatbelt.rs generate_sbpl
func generateSBPL(policy *SandboxPolicy) string {
	var sb strings.Builder
	sb.WriteString("(version 1)\n")

	switch policy.Mode {
	case ModeReadOnly:
		// Deny all by default, allow read
		sb.WriteString("(deny default)\n")
		sb.WriteString("(allow process-exec)\n")
		sb.WriteString("(allow process-fork)\n")
		sb.WriteString("(allow sysctl-read)\n")
		sb.WriteString("(allow file-read*)\n")
		sb.WriteString("(allow mach-lookup)\n")
		// Allow writing to temp dirs for process operation
		sb.WriteString("(allow file-write* (subpath \"/private/tmp\"))\n")
		sb.WriteString("(allow file-write* (subpath \"/tmp\"))\n")
		sb.WriteString("(allow file-write* (subpath \"/dev\"))\n")

	case ModeWorkspaceWrite:
		// Deny all by default, allow read + write to specified roots
		sb.WriteString("(deny default)\n")
		sb.WriteString("(allow process-exec)\n")
		sb.WriteString("(allow process-fork)\n")
		sb.WriteString("(allow sysctl-read)\n")
		sb.WriteString("(allow file-read*)\n")
		sb.WriteString("(allow mach-lookup)\n")
		// Allow writing to temp dirs
		sb.WriteString("(allow file-write* (subpath \"/private/tmp\"))\n")
		sb.WriteString("(allow file-write* (subpath \"/tmp\"))\n")
		sb.WriteString("(allow file-write* (subpath \"/dev\"))\n")
		// Allow writing to specified roots
		for _, root := range policy.WritableRoots {
			sb.WriteString(fmt.Sprintf("(allow file-write* (subpath %q))\n", string(root)))
		}
	}

	if !policy.NetworkAccess {
		sb.WriteString("(deny network*)\n")
	} else {
		sb.WriteString("(allow network*)\n")
	}

	return sb.String()
}

// GenerateSBPL is exported for testing.
func GenerateSBPL(policy *SandboxPolicy) string {
	return generateSBPL(policy)
}
