// Package errs holds error types shared across the agent and its adapters.
//
// Ported from the Sleepy pipeline and simplified: the original re-implemented
// errors.As by hand, the standard library already does that.
package errs

import (
	"errors"
	"fmt"
)

// TransientError is a retryable failure from an external service, for example
// an HTTP 429 or 503. Retry and backoff logic keys off this type.
type TransientError struct {
	StatusCode int    // HTTP status code, or 0 when the failure was not HTTP
	Cause      error  // underlying error
	Provider   string // which adapter failed, for logs and traces
}

func (e *TransientError) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("transient error from %s (HTTP %d): %v", e.Provider, e.StatusCode, e.Cause)
	}
	return fmt.Sprintf("transient error from %s: %v", e.Provider, e.Cause)
}

// Unwrap lets errors.Is and errors.As see the underlying cause.
func (e *TransientError) Unwrap() error { return e.Cause }

// NewTransient wraps cause as a retryable failure from provider.
func NewTransient(provider string, statusCode int, cause error) *TransientError {
	return &TransientError{StatusCode: statusCode, Cause: cause, Provider: provider}
}

// IsTransient reports whether err, or anything it wraps, is a TransientError.
func IsTransient(err error) bool {
	var te *TransientError
	return errors.As(err, &te)
}
