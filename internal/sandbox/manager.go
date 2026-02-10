package sandbox

import "runtime"

// NewSandboxManager creates the appropriate sandbox manager for the current platform.
// Falls back to NoopSandbox if no platform-specific sandbox is available.
func NewSandboxManager() SandboxManager {
	switch runtime.GOOS {
	case "darwin":
		s := &SeatbeltSandbox{}
		if s.Available() {
			return s
		}
	case "linux":
		s := &LinuxSandbox{}
		if s.Available() {
			return s
		}
	}
	return &NoopSandbox{}
}

// NewNoopSandboxManager always returns a no-op sandbox (for testing or full-access mode).
func NewNoopSandboxManager() SandboxManager {
	return &NoopSandbox{}
}
