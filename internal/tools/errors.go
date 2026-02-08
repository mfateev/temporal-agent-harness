// Package tools error types for distinguishing retryable vs non-retryable errors.
package tools

import (
	"errors"
	"fmt"
)

// TransientError indicates a temporary failure that should be retried.
// Examples: network timeout, RPC 500/503, temporary resource unavailability.
//
// Temporal will retry activities that return this error type.
type TransientError struct {
	Cause error
}

func (e *TransientError) Error() string {
	return fmt.Sprintf("transient error: %v", e.Cause)
}

func (e *TransientError) Unwrap() error {
	return e.Cause
}

// NewTransientError wraps an error as transient (retryable).
func NewTransientError(cause error) *TransientError {
	return &TransientError{Cause: cause}
}

// ValidationError indicates invalid input that won't succeed on retry.
// Examples: missing required argument, invalid argument type, malformed input.
//
// Temporal will NOT retry activities that return this error type.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error: %s", e.Message)
}

// NewValidationError creates a validation error (non-retryable).
func NewValidationError(message string) *ValidationError {
	return &ValidationError{Message: message}
}

// NewValidationErrorf creates a validation error with formatting.
func NewValidationErrorf(format string, args ...interface{}) *ValidationError {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}

// IsTransientError checks if an error is transient (retryable).
func IsTransientError(err error) bool {
	var transientErr *TransientError
	return errors.As(err, &transientErr)
}

// IsValidationError checks if an error is a validation error (non-retryable).
func IsValidationError(err error) bool {
	var validationErr *ValidationError
	return errors.As(err, &validationErr)
}
