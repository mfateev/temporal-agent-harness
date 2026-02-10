package execpolicy

import (
	"fmt"

	"go.starlark.net/starlark"
)

// ParsePolicy parses a Starlark policy file and returns a Policy.
// The Starlark file may contain calls to the prefix_rule() builtin.
//
// Maps to: codex-rs/execpolicy/src/lib.rs parse_policy
func ParsePolicy(filename, source string) (*Policy, error) {
	policy := NewPolicy()

	// Define the prefix_rule builtin
	prefixRule := starlark.NewBuiltin("prefix_rule", func(
		thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple,
	) (starlark.Value, error) {
		var (
			patternVal     *starlark.List
			decisionStr    string
			justification  string
		)

		if err := starlark.UnpackArgs(fn.Name(), args, kwargs,
			"pattern", &patternVal,
			"decision?", &decisionStr,
			"justification?", &justification,
		); err != nil {
			return nil, err
		}

		// Default decision is "allow"
		if decisionStr == "" {
			decisionStr = "allow"
		}

		decision, err := ParseDecision(decisionStr)
		if err != nil {
			return nil, err
		}

		pattern, err := parsePatternFromStarlark(patternVal)
		if err != nil {
			return nil, err
		}

		if len(pattern) == 0 {
			return nil, fmt.Errorf("prefix_rule pattern must not be empty")
		}

		rule := &PrefixRule{
			Pattern:       pattern,
			Decision:      decision,
			Justification: justification,
		}
		policy.AddRule(rule)

		return starlark.None, nil
	})

	// Set up the Starlark environment with the builtin
	predeclared := starlark.StringDict{
		"prefix_rule": prefixRule,
	}

	thread := &starlark.Thread{Name: filename}

	_, err := starlark.ExecFile(thread, filename, source, predeclared)
	if err != nil {
		return nil, &ParseError{
			File:    filename,
			Message: fmt.Sprintf("starlark parse error: %v", err),
			Cause:   err,
		}
	}

	return policy, nil
}

// parsePatternFromStarlark converts a Starlark list into a PrefixPattern.
// Each element is either a string (PatternSingle) or a list of strings (PatternAlts).
func parsePatternFromStarlark(list *starlark.List) (PrefixPattern, error) {
	pattern := make(PrefixPattern, 0, list.Len())

	iter := list.Iterate()
	defer iter.Done()
	var val starlark.Value
	for iter.Next(&val) {
		switch v := val.(type) {
		case starlark.String:
			s := string(v)
			if s == "" {
				return nil, fmt.Errorf("pattern token must not be empty string")
			}
			pattern = append(pattern, PatternToken{
				Kind:   PatternSingle,
				Single: s,
			})
		case *starlark.List:
			alts, err := starlarkListToStrings(v)
			if err != nil {
				return nil, fmt.Errorf("alternative list: %w", err)
			}
			if len(alts) == 0 {
				return nil, fmt.Errorf("alternative list must not be empty")
			}
			pattern = append(pattern, PatternToken{
				Kind: PatternAlts,
				Alts: alts,
			})
		default:
			return nil, fmt.Errorf("pattern element must be string or list of strings, got %s", val.Type())
		}
	}

	return pattern, nil
}

// starlarkListToStrings converts a Starlark list to a Go string slice.
func starlarkListToStrings(list *starlark.List) ([]string, error) {
	result := make([]string, 0, list.Len())
	iter := list.Iterate()
	defer iter.Done()
	var val starlark.Value
	for iter.Next(&val) {
		s, ok := val.(starlark.String)
		if !ok {
			return nil, fmt.Errorf("expected string, got %s", val.Type())
		}
		str := string(s)
		if str == "" {
			return nil, fmt.Errorf("alternative must not be empty string")
		}
		result = append(result, str)
	}
	return result, nil
}

// ParsePolicyMultiple parses multiple policy sources and merges them into one Policy.
func ParsePolicyMultiple(sources map[string]string) (*Policy, error) {
	merged := NewPolicy()
	for filename, source := range sources {
		p, err := ParsePolicy(filename, source)
		if err != nil {
			return nil, err
		}
		merged.Merge(p)
	}
	return merged, nil
}
