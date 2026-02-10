// Package execpolicy provides a Starlark-based exec policy engine for
// classifying shell commands as allow, prompt, or forbidden.
//
// Corresponds to: codex-rs/execpolicy/src/
package execpolicy

import (
	"fmt"
	"strings"
)

// Decision represents the outcome of evaluating a command against the policy.
// Decisions are ordered: Allow < Prompt < Forbidden. When aggregating multiple
// rule matches, the highest decision wins.
//
// Maps to: codex-rs/execpolicy/src/lib.rs Decision
type Decision int

const (
	// DecisionAllow means the command is safe and can be auto-executed.
	DecisionAllow Decision = iota
	// DecisionPrompt means the user should be asked before executing.
	DecisionPrompt
	// DecisionForbidden means the command must not be executed.
	DecisionForbidden
)

// String returns the string representation of a Decision.
func (d Decision) String() string {
	switch d {
	case DecisionAllow:
		return "allow"
	case DecisionPrompt:
		return "prompt"
	case DecisionForbidden:
		return "forbidden"
	default:
		return fmt.Sprintf("Decision(%d)", int(d))
	}
}

// ParseDecision parses a string into a Decision.
// Accepted values: "allow", "prompt", "forbidden" (case-insensitive).
//
// Maps to: codex-rs/execpolicy/src/lib.rs Decision::from_str
func ParseDecision(s string) (Decision, error) {
	switch strings.ToLower(s) {
	case "allow":
		return DecisionAllow, nil
	case "prompt":
		return DecisionPrompt, nil
	case "forbidden":
		return DecisionForbidden, nil
	default:
		return DecisionAllow, fmt.Errorf("invalid decision %q: must be allow, prompt, or forbidden", s)
	}
}

// Max returns the higher of two decisions (used for aggregation).
func (d Decision) Max(other Decision) Decision {
	if other > d {
		return other
	}
	return d
}
