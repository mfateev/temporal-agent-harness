// Package sandbox provides OS-level sandboxing for command execution.
//
// Maps to: codex-rs/core/src/sandbox/
package sandbox

import "fmt"

// SandboxMode controls the level of filesystem restriction.
//
// Maps to: codex-rs/core/src/sandbox/policy.rs SandboxMode
type SandboxMode string

const (
	// ModeFullAccess means no sandboxing (sandbox disabled).
	ModeFullAccess SandboxMode = "full-access"
	// ModeReadOnly means the command can only read the filesystem.
	ModeReadOnly SandboxMode = "read-only"
	// ModeWorkspaceWrite means the command can write only to specified roots.
	ModeWorkspaceWrite SandboxMode = "workspace-write"
)

// ParseSandboxMode parses a string into a SandboxMode.
func ParseSandboxMode(s string) (SandboxMode, error) {
	switch s {
	case "full-access", "full_access":
		return ModeFullAccess, nil
	case "read-only", "read_only":
		return ModeReadOnly, nil
	case "workspace-write", "workspace_write":
		return ModeWorkspaceWrite, nil
	default:
		return "", fmt.Errorf("invalid sandbox mode %q: must be full-access, read-only, or workspace-write", s)
	}
}

// WritableRoot is a directory path that is allowed to be written to in
// workspace-write mode.
type WritableRoot string

// SandboxPolicy configures the sandbox for a command execution.
//
// Maps to: codex-rs/core/src/sandbox/policy.rs SandboxPolicy
type SandboxPolicy struct {
	Mode          SandboxMode   `json:"mode"`
	WritableRoots []WritableRoot `json:"writable_roots,omitempty"`
	NetworkAccess bool          `json:"network_access"`
}

// IsRestricted returns true if the policy restricts execution in any way.
func (p *SandboxPolicy) IsRestricted() bool {
	return p != nil && p.Mode != ModeFullAccess && p.Mode != ""
}

// CommandSpec describes a command to be executed.
type CommandSpec struct {
	Program string   // e.g., "bash"
	Args    []string // e.g., ["-c", "ls -la"]
	Cwd     string   // Working directory
}

// ExecEnv is the transformed execution environment after sandbox wrapping.
type ExecEnv struct {
	Command []string          // Full command to execute (may include sandbox wrapper)
	Cwd     string            // Working directory
	Env     map[string]string // Additional environment variables
}

// SandboxManager is the interface for platform-specific sandbox implementations.
//
// Maps to: codex-rs/core/src/sandbox/ trait
type SandboxManager interface {
	// Transform wraps the command with sandbox restrictions.
	// Returns the transformed exec environment. If the policy is FullAccess,
	// returns the original command unchanged.
	Transform(spec CommandSpec, policy *SandboxPolicy) (*ExecEnv, error)

	// Available returns true if the sandbox implementation is available
	// on the current platform.
	Available() bool
}
