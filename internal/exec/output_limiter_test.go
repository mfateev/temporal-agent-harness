package exec

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Maps to: codex-rs/core/src/exec.rs tests (output aggregation)

func TestLimitOutputUnderCap(t *testing.T) {
	data := bytes.Repeat([]byte("a"), 100)
	result, truncated := LimitOutput(data)
	assert.False(t, truncated)
	assert.Equal(t, data, result)
}

func TestLimitOutputOverCap(t *testing.T) {
	data := bytes.Repeat([]byte("a"), ExecOutputMaxBytes+128*1024)
	result, truncated := LimitOutput(data)
	assert.True(t, truncated)
	assert.Equal(t, ExecOutputMaxBytes, len(result))
}

func TestAggregateOutputPrefersStderrOnContention(t *testing.T) {
	stdout := bytes.Repeat([]byte("a"), ExecOutputMaxBytes)
	stderr := bytes.Repeat([]byte("b"), ExecOutputMaxBytes)

	aggregated := AggregateOutput(stdout, stderr)
	stdoutCap := ExecOutputMaxBytes / 3
	stderrCap := ExecOutputMaxBytes - stdoutCap

	assert.Equal(t, ExecOutputMaxBytes, len(aggregated))
	assert.Equal(t, bytes.Repeat([]byte("a"), stdoutCap), aggregated[:stdoutCap])
	assert.Equal(t, bytes.Repeat([]byte("b"), stderrCap), aggregated[stdoutCap:])
}

func TestAggregateOutputRebalancesWhenStderrIsSmall(t *testing.T) {
	stdout := bytes.Repeat([]byte("a"), ExecOutputMaxBytes)
	stderr := []byte("b")

	aggregated := AggregateOutput(stdout, stderr)
	stdoutLen := ExecOutputMaxBytes - 1

	assert.Equal(t, ExecOutputMaxBytes, len(aggregated))
	assert.Equal(t, bytes.Repeat([]byte("a"), stdoutLen), aggregated[:stdoutLen])
	assert.Equal(t, []byte("b"), aggregated[stdoutLen:])
}

func TestAggregateOutputKeepsStdoutThenStderrWhenUnderCap(t *testing.T) {
	stdout := bytes.Repeat([]byte("a"), 4)
	stderr := bytes.Repeat([]byte("b"), 3)

	aggregated := AggregateOutput(stdout, stderr)

	var expected []byte
	expected = append(expected, stdout...)
	expected = append(expected, stderr...)
	assert.Equal(t, expected, aggregated)
}

func TestAggregateOutputFillsRemainingCapacityWithStderr(t *testing.T) {
	stdoutLen := ExecOutputMaxBytes / 10
	stdout := bytes.Repeat([]byte("a"), stdoutLen)
	stderr := bytes.Repeat([]byte("b"), ExecOutputMaxBytes)

	aggregated := AggregateOutput(stdout, stderr)
	stderrCap := ExecOutputMaxBytes - stdoutLen

	assert.Equal(t, ExecOutputMaxBytes, len(aggregated))
	assert.Equal(t, bytes.Repeat([]byte("a"), stdoutLen), aggregated[:stdoutLen])
	assert.Equal(t, bytes.Repeat([]byte("b"), stderrCap), aggregated[stdoutLen:])
}
