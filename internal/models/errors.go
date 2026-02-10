package models

import (
	"fmt"

	"go.temporal.io/sdk/temporal"
)

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

// LLM error type strings for temporal.ApplicationError.Type().
// Used across the activity boundary so the workflow can classify errors
// without parsing messages.
const (
	// LLMErrTypeContextOverflow indicates the context window was exceeded.
	// Non-retryable: the same input will fail again without compaction.
	LLMErrTypeContextOverflow = "LLMContextOverflow"

	// LLMErrTypeAPILimit indicates an API rate limit was hit.
	// Retryable after a delay.
	LLMErrTypeAPILimit = "LLMAPILimit"

	// LLMErrTypeFatal indicates an unrecoverable LLM error.
	// Non-retryable.
	LLMErrTypeFatal = "LLMFatal"
)

// WrapActivityError converts an ActivityError into a temporal.ApplicationError
// suitable for returning from a Temporal activity. This ensures the error type
// survives serialization across the activity boundary.
func WrapActivityError(ae *ActivityError) error {
	switch ae.Type {
	case ErrorTypeContextOverflow:
		return temporal.NewNonRetryableApplicationError(ae.Message, LLMErrTypeContextOverflow, nil)
	case ErrorTypeAPILimit:
		return temporal.NewApplicationErrorWithCause(ae.Message, LLMErrTypeAPILimit, nil)
	case ErrorTypeFatal:
		return temporal.NewNonRetryableApplicationError(ae.Message, LLMErrTypeFatal, nil)
	default:
		return temporal.NewApplicationErrorWithCause(ae.Message, ae.Type.String(), nil)
	}
}

// Tool error type strings for temporal.ApplicationError.Type().
// Use these constants to match errors on the workflow side via appErr.Type().
// Never parse error messages — use appErr.Details() for structured data.
const (
	// ToolErrTypeNotFound indicates the requested tool is not registered.
	// Non-retryable: the tool won't appear on retry.
	ToolErrTypeNotFound = "ToolNotFound"

	// ToolErrTypeValidation indicates invalid or missing arguments.
	// Non-retryable: the same bad input will be sent on retry.
	ToolErrTypeValidation = "ToolValidation"

	// ToolErrTypeTransient indicates a temporary infrastructure issue
	// (e.g., resource temporarily unavailable). Retryable.
	ToolErrTypeTransient = "ToolTransient"
)

// ToolErrorDetails carries structured context in ApplicationError.Details().
// Extract on the workflow side via: appErr.Details(&details)
type ToolErrorDetails struct {
	ToolName string `json:"tool_name"`
	Reason   string `json:"reason"` // Human-readable reason for LLM context
}

// NewToolNotFoundError creates a non-retryable ApplicationError for missing tools.
func NewToolNotFoundError(toolName string) error {
	return temporal.NewNonRetryableApplicationError(
		"tool not found",
		ToolErrTypeNotFound,
		nil,
		ToolErrorDetails{ToolName: toolName, Reason: fmt.Sprintf("tool %q is not registered", toolName)},
	)
}

// NewToolValidationError creates a non-retryable ApplicationError for invalid arguments.
func NewToolValidationError(toolName string, cause error) error {
	return temporal.NewNonRetryableApplicationError(
		"tool validation failed",
		ToolErrTypeValidation,
		cause,
		ToolErrorDetails{ToolName: toolName, Reason: cause.Error()},
	)
}

// NewToolTransientError creates a retryable ApplicationError for temporary failures.
func NewToolTransientError(toolName string, cause error) error {
	return temporal.NewApplicationErrorWithCause(
		"tool transient failure",
		ToolErrTypeTransient,
		cause,
		ToolErrorDetails{ToolName: toolName, Reason: cause.Error()},
	)
}
