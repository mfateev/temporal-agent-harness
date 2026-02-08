// Package exec provides command execution utilities including output limiting.
//
// Corresponds to: codex-rs/core/src/exec.rs (output capping)
package exec

// ExecOutputMaxBytes is the hard cap on bytes retained from exec stdout/stderr/aggregated output.
// A single runaway command cannot OOM the process by dumping huge amounts of data.
//
// Maps to: codex-rs/core/src/exec.rs EXEC_OUTPUT_MAX_BYTES
const ExecOutputMaxBytes = 1024 * 1024 // 1 MiB

// LimitOutput truncates output to ExecOutputMaxBytes.
// Returns the (possibly truncated) result and whether truncation occurred.
func LimitOutput(output []byte) (result []byte, truncated bool) {
	if len(output) <= ExecOutputMaxBytes {
		return output, false
	}
	return output[:ExecOutputMaxBytes], true
}

// AggregateOutput combines stdout and stderr, capped at ExecOutputMaxBytes.
// On contention: 1/3 stdout, 2/3 stderr, rebalance unused capacity.
//
// Maps to: codex-rs/core/src/exec.rs aggregate_output
func AggregateOutput(stdout, stderr []byte) []byte {
	totalLen := len(stdout) + len(stderr)
	maxBytes := ExecOutputMaxBytes

	if totalLen <= maxBytes {
		result := make([]byte, 0, totalLen)
		result = append(result, stdout...)
		result = append(result, stderr...)
		return result
	}

	// Under contention, reserve 1/3 for stdout and 2/3 for stderr;
	// rebalance unused capacity.
	wantStdout := len(stdout)
	if wantStdout > maxBytes/3 {
		wantStdout = maxBytes / 3
	}
	wantStderr := len(stderr)

	stderrTake := wantStderr
	if remaining := maxBytes - wantStdout; stderrTake > remaining {
		stderrTake = remaining
	}

	// Rebalance: give unused stderr capacity back to stdout
	remaining := maxBytes - wantStdout - stderrTake
	extraStdout := len(stdout) - wantStdout
	if extraStdout < 0 {
		extraStdout = 0
	}
	if remaining > extraStdout {
		remaining = extraStdout
	}
	stdoutTake := wantStdout + remaining

	result := make([]byte, 0, stdoutTake+stderrTake)
	result = append(result, stdout[:stdoutTake]...)
	result = append(result, stderr[:stderrTake]...)
	return result
}
