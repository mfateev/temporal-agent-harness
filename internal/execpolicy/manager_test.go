package execpolicy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mfateev/codex-temporal-go/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadExecPolicy_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	m, err := LoadExecPolicy(dir)
	require.NoError(t, err)

	// No rules — uses fallback
	req := m.EvaluateCommand([]string{"echo", "hi"}, "never")
	assert.Equal(t, tools.ApprovalSkip, req)
}

func TestLoadExecPolicy_NoRulesDir(t *testing.T) {
	dir := t.TempDir()
	m, err := LoadExecPolicy(dir)
	require.NoError(t, err)
	assert.NotNil(t, m)
}

func TestLoadExecPolicy_WithRules(t *testing.T) {
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, "rules")
	require.NoError(t, os.MkdirAll(rulesDir, 0o755))

	rules := `
prefix_rule(pattern=["echo"], decision="allow")
prefix_rule(pattern=["rm"], decision="forbidden")
`
	require.NoError(t, os.WriteFile(filepath.Join(rulesDir, "default.rules"), []byte(rules), 0o644))

	m, err := LoadExecPolicy(dir)
	require.NoError(t, err)

	req := m.EvaluateCommand([]string{"echo", "hello"}, "unless-trusted")
	assert.Equal(t, tools.ApprovalSkip, req)

	req = m.EvaluateCommand([]string{"rm", "-rf"}, "unless-trusted")
	assert.Equal(t, tools.ApprovalForbidden, req)
}

func TestLoadExecPolicy_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, "rules")
	require.NoError(t, os.MkdirAll(rulesDir, 0o755))

	require.NoError(t, os.WriteFile(
		filepath.Join(rulesDir, "a.rules"),
		[]byte(`prefix_rule(pattern=["echo"], decision="allow")`),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(rulesDir, "b.rules"),
		[]byte(`prefix_rule(pattern=["rm"], decision="forbidden")`),
		0o644,
	))

	m, err := LoadExecPolicy(dir)
	require.NoError(t, err)

	req := m.EvaluateCommand([]string{"echo"}, "unless-trusted")
	assert.Equal(t, tools.ApprovalSkip, req)

	req = m.EvaluateCommand([]string{"rm"}, "unless-trusted")
	assert.Equal(t, tools.ApprovalForbidden, req)
}

func TestLoadExecPolicyFromSource(t *testing.T) {
	source := `prefix_rule(pattern=["git", "push"], decision="prompt")`
	m, err := LoadExecPolicyFromSource(source)
	require.NoError(t, err)

	req := m.EvaluateCommand([]string{"git", "push"}, "never")
	assert.Equal(t, tools.ApprovalNeeded, req)
}

func TestLoadExecPolicyFromSource_Empty(t *testing.T) {
	m, err := LoadExecPolicyFromSource("")
	require.NoError(t, err)

	req := m.EvaluateCommand([]string{"anything"}, "never")
	assert.Equal(t, tools.ApprovalSkip, req)
}

func TestEvaluateCommand_UnlessTrusted_SafeCommand(t *testing.T) {
	m := NewExecPolicyManager(NewPolicy())

	// "ls" is a known safe command in command_safety
	req := m.EvaluateCommand([]string{"bash", "-c", "ls"}, "unless-trusted")
	assert.Equal(t, tools.ApprovalSkip, req)
}

func TestEvaluateCommand_UnlessTrusted_UnsafeCommand(t *testing.T) {
	m := NewExecPolicyManager(NewPolicy())

	// "curl" is not in the safe list
	req := m.EvaluateCommand([]string{"bash", "-c", "curl http://example.com"}, "unless-trusted")
	assert.Equal(t, tools.ApprovalNeeded, req)
}

func TestEvaluateCommand_NeverMode(t *testing.T) {
	m := NewExecPolicyManager(NewPolicy())

	// "never" mode auto-approves everything
	req := m.EvaluateCommand([]string{"bash", "-c", "rm -rf /"}, "never")
	assert.Equal(t, tools.ApprovalSkip, req)
}

func TestEvaluateCommand_OnFailureMode(t *testing.T) {
	m := NewExecPolicyManager(NewPolicy())

	// "on-failure" mode auto-approves (runs in sandbox)
	req := m.EvaluateCommand([]string{"bash", "-c", "curl http://example.com"}, "on-failure")
	assert.Equal(t, tools.ApprovalSkip, req)
}

func TestEvaluateCommand_RuleOverridesFallback(t *testing.T) {
	p := NewPolicy()
	p.AddRule(&PrefixRule{
		Pattern:  PrefixPattern{{Kind: PatternSingle, Single: "rm"}},
		Decision: DecisionForbidden,
	})
	m := NewExecPolicyManager(p)

	// Even in "never" mode, explicit rule takes precedence
	req := m.EvaluateCommand([]string{"bash", "-c", "rm -rf /"}, "never")
	assert.Equal(t, tools.ApprovalForbidden, req)
}

func TestEvaluateShellCommand(t *testing.T) {
	p := NewPolicy()
	p.AddRule(&PrefixRule{
		Pattern:  PrefixPattern{{Kind: PatternSingle, Single: "echo"}},
		Decision: DecisionAllow,
	})
	m := NewExecPolicyManager(p)

	req := m.EvaluateShellCommand("echo hello", "unless-trusted")
	assert.Equal(t, tools.ApprovalSkip, req)
}

func TestGetEvaluation(t *testing.T) {
	p := NewPolicy()
	p.AddRule(&PrefixRule{
		Pattern:       PrefixPattern{{Kind: PatternSingle, Single: "rm"}},
		Decision:      DecisionForbidden,
		Justification: "deleting files is dangerous",
	})
	m := NewExecPolicyManager(p)

	eval := m.GetEvaluation([]string{"bash", "-c", "rm -rf /"}, "unless-trusted")
	assert.Equal(t, DecisionForbidden, eval.Decision)
	assert.Equal(t, "deleting files is dangerous", eval.Justification)
}

func TestAppendAndReload(t *testing.T) {
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, "rules")
	require.NoError(t, os.MkdirAll(rulesDir, 0o755))

	m, err := LoadExecPolicy(dir)
	require.NoError(t, err)

	// Initially no rules — unknown command uses fallback
	req := m.EvaluateCommand([]string{"my-tool"}, "unless-trusted")
	assert.Equal(t, tools.ApprovalNeeded, req)

	// Append an allow rule
	err = m.AppendAndReload(dir, []string{"my-tool"})
	require.NoError(t, err)

	// Now the rule matches
	req = m.EvaluateCommand([]string{"my-tool"}, "unless-trusted")
	assert.Equal(t, tools.ApprovalSkip, req)
}
