package execpolicy

// PatternTokenKind distinguishes single-value tokens from alternative sets.
//
// Maps to: codex-rs/execpolicy/src/lib.rs PatternToken
type PatternTokenKind int

const (
	// PatternSingle matches exactly one string value.
	PatternSingle PatternTokenKind = iota
	// PatternAlts matches any of a set of alternative strings.
	PatternAlts
)

// PatternToken is a single element in a prefix pattern. It matches either
// exactly one string or any of a set of alternative strings.
//
// Maps to: codex-rs/execpolicy/src/lib.rs PatternToken
type PatternToken struct {
	Kind   PatternTokenKind
	Single string   // used when Kind == PatternSingle
	Alts   []string // used when Kind == PatternAlts
}

// Matches returns true if the token matches the given string.
func (pt *PatternToken) Matches(s string) bool {
	switch pt.Kind {
	case PatternSingle:
		return pt.Single == s
	case PatternAlts:
		for _, alt := range pt.Alts {
			if alt == s {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// PrefixPattern is a sequence of pattern tokens that matches a command prefix.
//
// Maps to: codex-rs/execpolicy/src/lib.rs PrefixPattern
type PrefixPattern []PatternToken

// Matches returns true if the pattern is a prefix of the given command.
// The command must have at least as many tokens as the pattern.
func (pp PrefixPattern) Matches(cmd []string) bool {
	if len(cmd) < len(pp) {
		return false
	}
	for i, token := range pp {
		if !token.Matches(cmd[i]) {
			return false
		}
	}
	return true
}

// ProgramName returns the program name from the first token of the pattern,
// or empty string if the pattern is empty or uses alternatives for the first token.
func (pp PrefixPattern) ProgramName() string {
	if len(pp) == 0 {
		return ""
	}
	if pp[0].Kind == PatternSingle {
		return pp[0].Single
	}
	return ""
}

// PrefixRule matches a command prefix and assigns a decision.
//
// Maps to: codex-rs/execpolicy/src/lib.rs PrefixRule
type PrefixRule struct {
	Pattern       PrefixPattern
	Decision      Decision
	Justification string
}

// Matches returns true if the command matches this rule's pattern.
func (pr *PrefixRule) Matches(cmd []string) bool {
	return pr.Pattern.Matches(cmd)
}

// Rule is the interface for policy rules. Currently only PrefixRule
// is implemented, but this allows future extension.
type Rule interface {
	// Match tests whether the rule applies to the given command.
	Match(cmd []string) bool
	// GetDecision returns the decision if the rule matches.
	GetDecision() Decision
	// GetJustification returns the human-readable reason.
	GetJustification() string
}

// Match implements Rule for PrefixRule.
func (pr *PrefixRule) Match(cmd []string) bool {
	return pr.Matches(cmd)
}

// GetDecision implements Rule for PrefixRule.
func (pr *PrefixRule) GetDecision() Decision {
	return pr.Decision
}

// GetJustification implements Rule for PrefixRule.
func (pr *PrefixRule) GetJustification() string {
	return pr.Justification
}
