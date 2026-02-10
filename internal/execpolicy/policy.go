package execpolicy

// Evaluation holds the result of evaluating a command against the policy.
//
// Maps to: codex-rs/execpolicy/src/lib.rs Evaluation
type Evaluation struct {
	// Decision is the aggregate decision (highest of all matching rules).
	Decision Decision
	// MatchedRules are the rules that matched the command.
	MatchedRules []Rule
	// Justification is the justification from the highest-decision matched rule.
	Justification string
	// UsedFallback is true if no rules matched and the fallback was used.
	UsedFallback bool
}

// Policy holds a set of rules indexed by program name for fast lookup.
//
// Maps to: codex-rs/execpolicy/src/lib.rs Policy
type Policy struct {
	// rulesByProgram maps program name → rules. An empty-string key holds
	// rules whose first token is an alternative set (cannot index by single name).
	rulesByProgram map[string][]Rule
}

// NewPolicy creates an empty policy.
func NewPolicy() *Policy {
	return &Policy{
		rulesByProgram: make(map[string][]Rule),
	}
}

// AddRule adds a rule to the policy, indexed by program name.
func (p *Policy) AddRule(r Rule) {
	// Try to index by program name
	if pr, ok := r.(*PrefixRule); ok {
		name := pr.Pattern.ProgramName()
		p.rulesByProgram[name] = append(p.rulesByProgram[name], r)
		return
	}
	// Fallback: index under empty key
	p.rulesByProgram[""] = append(p.rulesByProgram[""], r)
}

// Check evaluates a single command against the policy.
// If no rules match, calls fallback to get a default decision.
//
// Maps to: codex-rs/execpolicy/src/lib.rs Policy::check
func (p *Policy) Check(cmd []string, fallback func([]string) Decision) Evaluation {
	if len(cmd) == 0 {
		d := DecisionPrompt
		if fallback != nil {
			d = fallback(cmd)
		}
		return Evaluation{Decision: d, UsedFallback: true}
	}

	var matched []Rule
	highestDecision := DecisionAllow
	justification := ""

	// Check rules indexed by program name
	programName := cmd[0]
	for _, r := range p.rulesByProgram[programName] {
		if r.Match(cmd) {
			matched = append(matched, r)
			d := r.GetDecision()
			if d > highestDecision {
				highestDecision = d
				justification = r.GetJustification()
			}
		}
	}

	// Check rules with alternative first tokens (indexed under "")
	for _, r := range p.rulesByProgram[""] {
		if r.Match(cmd) {
			matched = append(matched, r)
			d := r.GetDecision()
			if d > highestDecision {
				highestDecision = d
				justification = r.GetJustification()
			}
		}
	}

	if len(matched) == 0 {
		// No rules matched — use fallback
		d := DecisionPrompt
		if fallback != nil {
			d = fallback(cmd)
		}
		return Evaluation{Decision: d, UsedFallback: true}
	}

	return Evaluation{
		Decision:      highestDecision,
		MatchedRules:  matched,
		Justification: justification,
	}
}

// CheckMultiple evaluates multiple commands (e.g., from `bash -c "cmd1 && cmd2"`)
// and returns the aggregate result. The highest decision across all commands wins.
//
// Maps to: codex-rs/execpolicy/src/lib.rs Policy::check_multiple
func (p *Policy) CheckMultiple(cmds [][]string, fallback func([]string) Decision) Evaluation {
	if len(cmds) == 0 {
		d := DecisionPrompt
		if fallback != nil {
			d = fallback(nil)
		}
		return Evaluation{Decision: d, UsedFallback: true}
	}

	aggregate := Evaluation{Decision: DecisionAllow}
	allFallback := true

	for _, cmd := range cmds {
		eval := p.Check(cmd, fallback)
		if !eval.UsedFallback {
			allFallback = false
		}
		if eval.Decision > aggregate.Decision {
			aggregate.Decision = eval.Decision
			aggregate.Justification = eval.Justification
		}
		aggregate.MatchedRules = append(aggregate.MatchedRules, eval.MatchedRules...)
	}

	aggregate.UsedFallback = allFallback
	return aggregate
}

// Merge adds all rules from another policy into this one.
func (p *Policy) Merge(other *Policy) {
	for key, rules := range other.rulesByProgram {
		p.rulesByProgram[key] = append(p.rulesByProgram[key], rules...)
	}
}
