package models

import "fmt"

// ErrorType categorizes errors for appropriate handling
//
// Maps to: codex-rs/core/src/function_tool.rs error categorization
type ErrorType int

const (
	ErrorTypeTransient        ErrorType = iota // Network, timeout → Temporal retries
	ErrorTypeContextOverflow                   // Context window exceeded → ContinueAsNew
	ErrorTypeAPILimit                          // Rate limit → surface to user
	ErrorTypeToolFailure                       // Individual tool failed → continue workflow
	ErrorTypeFatal                             // Unrecoverable → stop workflow
)

// String returns the string representation of ErrorType
func (e ErrorType) String() string {
	switch e {
	case ErrorTypeTransient:
		return "Transient"
	case ErrorTypeContextOverflow:
		return "ContextOverflow"
	case ErrorTypeAPILimit:
		return "APILimit"
	case ErrorTypeToolFailure:
		return "ToolFailure"
	case ErrorTypeFatal:
		return "Fatal"
	default:
		return "Unknown"
	}
}

// ActivityError represents an error from a Temporal activity with categorization
//
// Maps to: codex-rs/core/src/function_tool.rs error handling
type ActivityError struct {
	Type      ErrorType              `json:"type"`
	Retryable bool                   `json:"retryable"`
	Message   string                 `json:"message"`
	Details   map[string]interface{} `json:"details,omitempty"`
}

// Error implements the error interface
func (e *ActivityError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Type, e.Message)
}

// NewTransientError creates a retryable transient error
func NewTransientError(message string) *ActivityError {
	return &ActivityError{
		Type:      ErrorTypeTransient,
		Retryable: true,
		Message:   message,
	}
}

// NewContextOverflowError creates a context overflow error
func NewContextOverflowError(message string) *ActivityError {
	return &ActivityError{
		Type:      ErrorTypeContextOverflow,
		Retryable: false,
		Message:   message,
	}
}

// NewAPILimitError creates an API rate limit error
func NewAPILimitError(message string) *ActivityError {
	return &ActivityError{
		Type:      ErrorTypeAPILimit,
		Retryable: true,
		Message:   message,
	}
}

// NewToolFailureError creates a tool failure error
func NewToolFailureError(message string) *ActivityError {
	return &ActivityError{
		Type:      ErrorTypeToolFailure,
		Retryable: false,
		Message:   message,
	}
}

// NewFatalError creates a fatal error
func NewFatalError(message string) *ActivityError {
	return &ActivityError{
		Type:      ErrorTypeFatal,
		Retryable: false,
		Message:   message,
	}
}
