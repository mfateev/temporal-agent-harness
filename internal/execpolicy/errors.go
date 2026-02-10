package execpolicy

import "fmt"

// ParseError represents an error parsing an exec policy file.
type ParseError struct {
	File    string
	Line    int
	Message string
	Cause   error
}

func (e *ParseError) Error() string {
	if e.File != "" {
		return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Message)
	}
	return e.Message
}

func (e *ParseError) Unwrap() error {
	return e.Cause
}

// RuleError represents an invalid rule definition.
type RuleError struct {
	Message string
}

func (e *RuleError) Error() string {
	return fmt.Sprintf("rule error: %s", e.Message)
}
