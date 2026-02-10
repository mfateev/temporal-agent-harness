//go:build !darwin

package sandbox

// SeatbeltSandbox is a stub for non-darwin platforms.
type SeatbeltSandbox struct{}

// Available returns false on non-darwin platforms.
func (s *SeatbeltSandbox) Available() bool {
	return false
}

// Transform returns an error on non-darwin platforms.
func (s *SeatbeltSandbox) Transform(spec CommandSpec, policy *SandboxPolicy) (*ExecEnv, error) {
	// Fall through to no-op
	return &ExecEnv{
		Command: append([]string{spec.Program}, spec.Args...),
		Cwd:     spec.Cwd,
	}, nil
}
