package execpolicy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePolicy_SimpleAllowRule(t *testing.T) {
	source := `prefix_rule(pattern=["echo"], decision="allow")`
	p, err := ParsePolicy("test.rules", source)
	require.NoError(t, err)

	eval := p.Check([]string{"echo", "hello"}, nil)
	assert.Equal(t, DecisionAllow, eval.Decision)
	assert.Len(t, eval.MatchedRules, 1)
}

func TestParsePolicy_ForbiddenRule(t *testing.T) {
	source := `prefix_rule(pattern=["git", "reset"], decision="forbidden")`
	p, err := ParsePolicy("test.rules", source)
	require.NoError(t, err)

	eval := p.Check([]string{"git", "reset", "--hard"}, nil)
	assert.Equal(t, DecisionForbidden, eval.Decision)
}

func TestParsePolicy_PromptRule(t *testing.T) {
	source := `prefix_rule(pattern=["git", "push"], decision="prompt")`
	p, err := ParsePolicy("test.rules", source)
	require.NoError(t, err)

	eval := p.Check([]string{"git", "push", "origin", "main"}, nil)
	assert.Equal(t, DecisionPrompt, eval.Decision)
}

func TestParsePolicy_DefaultDecisionIsAllow(t *testing.T) {
	source := `prefix_rule(pattern=["echo"])`
	p, err := ParsePolicy("test.rules", source)
	require.NoError(t, err)

	eval := p.Check([]string{"echo"}, nil)
	assert.Equal(t, DecisionAllow, eval.Decision)
}

func TestParsePolicy_AlternativePattern(t *testing.T) {
	source := `prefix_rule(pattern=["npm", ["install", "ci"]], decision="prompt")`
	p, err := ParsePolicy("test.rules", source)
	require.NoError(t, err)

	eval := p.Check([]string{"npm", "install"}, nil)
	assert.Equal(t, DecisionPrompt, eval.Decision)

	eval = p.Check([]string{"npm", "ci"}, nil)
	assert.Equal(t, DecisionPrompt, eval.Decision)

	eval = p.Check([]string{"npm", "run"}, nil)
	assert.True(t, eval.UsedFallback)
}

func TestParsePolicy_WithJustification(t *testing.T) {
	source := `prefix_rule(pattern=["rm"], decision="forbidden", justification="never delete files")`
	p, err := ParsePolicy("test.rules", source)
	require.NoError(t, err)

	eval := p.Check([]string{"rm", "-rf"}, nil)
	assert.Equal(t, DecisionForbidden, eval.Decision)
	assert.Equal(t, "never delete files", eval.Justification)
}

func TestParsePolicy_MultipleRules(t *testing.T) {
	source := `
prefix_rule(pattern=["echo"], decision="allow")
prefix_rule(pattern=["git", "reset"], decision="forbidden")
prefix_rule(pattern=["npm", ["install", "ci"]], decision="prompt")
`
	p, err := ParsePolicy("test.rules", source)
	require.NoError(t, err)

	eval := p.Check([]string{"echo", "hello"}, nil)
	assert.Equal(t, DecisionAllow, eval.Decision)

	eval = p.Check([]string{"git", "reset"}, nil)
	assert.Equal(t, DecisionForbidden, eval.Decision)

	eval = p.Check([]string{"npm", "install"}, nil)
	assert.Equal(t, DecisionPrompt, eval.Decision)
}

func TestParsePolicy_InvalidDecision(t *testing.T) {
	source := `prefix_rule(pattern=["echo"], decision="invalid")`
	_, err := ParsePolicy("test.rules", source)
	require.Error(t, err)
}

func TestParsePolicy_EmptyPattern(t *testing.T) {
	source := `prefix_rule(pattern=[])`
	_, err := ParsePolicy("test.rules", source)
	require.Error(t, err)
}

func TestParsePolicy_InvalidPatternType(t *testing.T) {
	source := `prefix_rule(pattern=[123])`
	_, err := ParsePolicy("test.rules", source)
	require.Error(t, err)
}

func TestParsePolicy_EmptyAlternative(t *testing.T) {
	source := `prefix_rule(pattern=["git", []])`
	_, err := ParsePolicy("test.rules", source)
	require.Error(t, err)
}

func TestParsePolicy_SyntaxError(t *testing.T) {
	source := `this is not valid starlark {{{`
	_, err := ParsePolicy("test.rules", source)
	require.Error(t, err)
	assert.IsType(t, &ParseError{}, err)
}

func TestParsePolicy_EmptySource(t *testing.T) {
	source := ``
	p, err := ParsePolicy("test.rules", source)
	require.NoError(t, err)

	// No rules, should fallback
	eval := p.Check([]string{"anything"}, nil)
	assert.True(t, eval.UsedFallback)
}

func TestParsePolicy_CommentsOnly(t *testing.T) {
	source := `
# This is a comment
# Another comment
`
	p, err := ParsePolicy("test.rules", source)
	require.NoError(t, err)

	eval := p.Check([]string{"anything"}, nil)
	assert.True(t, eval.UsedFallback)
}

func TestParsePolicyMultiple(t *testing.T) {
	sources := map[string]string{
		"a.rules": `prefix_rule(pattern=["echo"], decision="allow")`,
		"b.rules": `prefix_rule(pattern=["rm"], decision="forbidden")`,
	}

	p, err := ParsePolicyMultiple(sources)
	require.NoError(t, err)

	eval := p.Check([]string{"echo"}, nil)
	assert.Equal(t, DecisionAllow, eval.Decision)

	eval = p.Check([]string{"rm"}, nil)
	assert.Equal(t, DecisionForbidden, eval.Decision)
}
