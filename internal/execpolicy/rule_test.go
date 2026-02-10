package execpolicy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPatternToken_Matches_Single(t *testing.T) {
	token := PatternToken{Kind: PatternSingle, Single: "git"}
	assert.True(t, token.Matches("git"))
	assert.False(t, token.Matches("hg"))
	assert.False(t, token.Matches(""))
}

func TestPatternToken_Matches_Alts(t *testing.T) {
	token := PatternToken{Kind: PatternAlts, Alts: []string{"install", "ci", "add"}}
	assert.True(t, token.Matches("install"))
	assert.True(t, token.Matches("ci"))
	assert.True(t, token.Matches("add"))
	assert.False(t, token.Matches("remove"))
	assert.False(t, token.Matches(""))
}

func TestPrefixPattern_Matches(t *testing.T) {
	// Pattern: ["git", "push"]
	pattern := PrefixPattern{
		{Kind: PatternSingle, Single: "git"},
		{Kind: PatternSingle, Single: "push"},
	}

	assert.True(t, pattern.Matches([]string{"git", "push"}))
	assert.True(t, pattern.Matches([]string{"git", "push", "origin", "main"}))
	assert.False(t, pattern.Matches([]string{"git"}))       // too short
	assert.False(t, pattern.Matches([]string{"git", "pull"})) // wrong second token
	assert.False(t, pattern.Matches([]string{}))
}

func TestPrefixPattern_Matches_WithAlts(t *testing.T) {
	// Pattern: ["npm", ["install", "ci"]]
	pattern := PrefixPattern{
		{Kind: PatternSingle, Single: "npm"},
		{Kind: PatternAlts, Alts: []string{"install", "ci"}},
	}

	assert.True(t, pattern.Matches([]string{"npm", "install"}))
	assert.True(t, pattern.Matches([]string{"npm", "ci"}))
	assert.True(t, pattern.Matches([]string{"npm", "install", "--save"}))
	assert.False(t, pattern.Matches([]string{"npm", "run"}))
	assert.False(t, pattern.Matches([]string{"npm"}))
}

func TestPrefixPattern_ProgramName(t *testing.T) {
	pattern := PrefixPattern{
		{Kind: PatternSingle, Single: "git"},
		{Kind: PatternSingle, Single: "push"},
	}
	assert.Equal(t, "git", pattern.ProgramName())

	// Pattern with alts as first token
	altPattern := PrefixPattern{
		{Kind: PatternAlts, Alts: []string{"npm", "yarn"}},
	}
	assert.Equal(t, "", altPattern.ProgramName())

	// Empty pattern
	assert.Equal(t, "", PrefixPattern{}.ProgramName())
}

func TestPrefixRule_Matches(t *testing.T) {
	rule := &PrefixRule{
		Pattern: PrefixPattern{
			{Kind: PatternSingle, Single: "echo"},
		},
		Decision: DecisionAllow,
	}

	assert.True(t, rule.Matches([]string{"echo", "hello"}))
	assert.True(t, rule.Matches([]string{"echo"}))
	assert.False(t, rule.Matches([]string{"rm", "-rf"}))
}

func TestPrefixRule_RuleInterface(t *testing.T) {
	rule := &PrefixRule{
		Pattern: PrefixPattern{
			{Kind: PatternSingle, Single: "rm"},
		},
		Decision:      DecisionForbidden,
		Justification: "dangerous command",
	}

	var r Rule = rule
	assert.True(t, r.Match([]string{"rm", "-rf", "/"}))
	assert.Equal(t, DecisionForbidden, r.GetDecision())
	assert.Equal(t, "dangerous command", r.GetJustification())
}
