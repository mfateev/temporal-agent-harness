//go:build !linux

package sandbox

// LinuxSandbox is a stub for non-linux platforms.
type LinuxSandbox struct{}

// Available returns false on non-linux platforms.
func (l *LinuxSandbox) Available() bool {
	return false
}

// Transform returns a pass-through on non-linux platforms.
func (l *LinuxSandbox) Transform(spec CommandSpec, policy *SandboxPolicy) (*ExecEnv, error) {
	return &ExecEnv{
		Command: append([]string{spec.Program}, spec.Args...),
		Cwd:     spec.Cwd,
	}, nil
}
