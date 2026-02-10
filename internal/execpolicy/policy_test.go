package execpolicy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPolicy_Check_MatchesSingleRule(t *testing.T) {
	p := NewPolicy()
	p.AddRule(&PrefixRule{
		Pattern:  PrefixPattern{{Kind: PatternSingle, Single: "echo"}},
		Decision: DecisionAllow,
	})

	eval := p.Check([]string{"echo", "hello"}, nil)
	assert.Equal(t, DecisionAllow, eval.Decision)
	assert.Len(t, eval.MatchedRules, 1)
	assert.False(t, eval.UsedFallback)
}

func TestPolicy_Check_NoMatch_UsesFallback(t *testing.T) {
	p := NewPolicy()
	p.AddRule(&PrefixRule{
		Pattern:  PrefixPattern{{Kind: PatternSingle, Single: "echo"}},
		Decision: DecisionAllow,
	})

	eval := p.Check([]string{"rm", "-rf"}, func(cmd []string) Decision {
		return DecisionPrompt
	})
	assert.Equal(t, DecisionPrompt, eval.Decision)
	assert.Len(t, eval.MatchedRules, 0)
	assert.True(t, eval.UsedFallback)
}

func TestPolicy_Check_NoMatch_NilFallback_DefaultsPrompt(t *testing.T) {
	p := NewPolicy()

	eval := p.Check([]string{"unknown"}, nil)
	assert.Equal(t, DecisionPrompt, eval.Decision)
	assert.True(t, eval.UsedFallback)
}

func TestPolicy_Check_HighestDecisionWins(t *testing.T) {
	p := NewPolicy()
	// Rule 1: git → allow
	p.AddRule(&PrefixRule{
		Pattern:  PrefixPattern{{Kind: PatternSingle, Single: "git"}},
		Decision: DecisionAllow,
	})
	// Rule 2: git reset → forbidden
	p.AddRule(&PrefixRule{
		Pattern: PrefixPattern{
			{Kind: PatternSingle, Single: "git"},
			{Kind: PatternSingle, Single: "reset"},
		},
		Decision:      DecisionForbidden,
		Justification: "destructive",
	})

	eval := p.Check([]string{"git", "reset", "--hard"}, nil)
	assert.Equal(t, DecisionForbidden, eval.Decision)
	assert.Len(t, eval.MatchedRules, 2)
	assert.Equal(t, "destructive", eval.Justification)
	assert.False(t, eval.UsedFallback)
}

func TestPolicy_Check_MultipleRules_AllMatch(t *testing.T) {
	p := NewPolicy()
	p.AddRule(&PrefixRule{
		Pattern:  PrefixPattern{{Kind: PatternSingle, Single: "npm"}},
		Decision: DecisionPrompt,
	})
	p.AddRule(&PrefixRule{
		Pattern: PrefixPattern{
			{Kind: PatternSingle, Single: "npm"},
			{Kind: PatternSingle, Single: "install"},
		},
		Decision: DecisionAllow,
	})

	// "npm install" matches both rules; highest (Prompt) wins
	eval := p.Check([]string{"npm", "install", "express"}, nil)
	assert.Equal(t, DecisionPrompt, eval.Decision)
	assert.Len(t, eval.MatchedRules, 2)
}

func TestPolicy_Check_EmptyCommand(t *testing.T) {
	p := NewPolicy()
	eval := p.Check([]string{}, func(cmd []string) Decision { return DecisionForbidden })
	assert.Equal(t, DecisionForbidden, eval.Decision)
	assert.True(t, eval.UsedFallback)
}

func TestPolicy_CheckMultiple_AggregatesHighest(t *testing.T) {
	p := NewPolicy()
	p.AddRule(&PrefixRule{
		Pattern:  PrefixPattern{{Kind: PatternSingle, Single: "echo"}},
		Decision: DecisionAllow,
	})
	p.AddRule(&PrefixRule{
		Pattern:  PrefixPattern{{Kind: PatternSingle, Single: "rm"}},
		Decision: DecisionForbidden,
	})

	cmds := [][]string{
		{"echo", "hello"},
		{"rm", "-rf", "/"},
	}
	eval := p.CheckMultiple(cmds, nil)
	assert.Equal(t, DecisionForbidden, eval.Decision)
	assert.Len(t, eval.MatchedRules, 2)
}

func TestPolicy_CheckMultiple_EmptyCommands(t *testing.T) {
	p := NewPolicy()
	eval := p.CheckMultiple(nil, func(cmd []string) Decision { return DecisionAllow })
	assert.Equal(t, DecisionAllow, eval.Decision)
	assert.True(t, eval.UsedFallback)
}

func TestPolicy_CheckMultiple_AllFallback(t *testing.T) {
	p := NewPolicy()
	cmds := [][]string{{"unknown1"}, {"unknown2"}}
	eval := p.CheckMultiple(cmds, func(cmd []string) Decision { return DecisionPrompt })
	assert.Equal(t, DecisionPrompt, eval.Decision)
	assert.True(t, eval.UsedFallback)
}

func TestPolicy_Merge(t *testing.T) {
	p1 := NewPolicy()
	p1.AddRule(&PrefixRule{
		Pattern:  PrefixPattern{{Kind: PatternSingle, Single: "echo"}},
		Decision: DecisionAllow,
	})

	p2 := NewPolicy()
	p2.AddRule(&PrefixRule{
		Pattern:  PrefixPattern{{Kind: PatternSingle, Single: "rm"}},
		Decision: DecisionForbidden,
	})

	p1.Merge(p2)

	// p1 should now have rules from both
	eval := p1.Check([]string{"rm", "-rf"}, nil)
	assert.Equal(t, DecisionForbidden, eval.Decision)

	eval = p1.Check([]string{"echo", "test"}, nil)
	assert.Equal(t, DecisionAllow, eval.Decision)
}
