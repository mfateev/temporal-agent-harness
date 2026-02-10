package sandbox

// NoopSandbox is a no-op sandbox that passes through commands unchanged.
// Used when sandbox mode is full-access or when no sandbox is available.
type NoopSandbox struct{}

// Transform returns the command unchanged.
func (n *NoopSandbox) Transform(spec CommandSpec, policy *SandboxPolicy) (*ExecEnv, error) {
	return &ExecEnv{
		Command: append([]string{spec.Program}, spec.Args...),
		Cwd:     spec.Cwd,
		Env:     nil,
	}, nil
}

// Available always returns true (no-op is always available).
func (n *NoopSandbox) Available() bool {
	return true
}
