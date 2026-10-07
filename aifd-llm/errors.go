package llm

import (
	"context"
	"errors"
	"fmt"
)

// ErrorKind classifies stable error behavior across providers.
type ErrorKind string

const (
	KindInvalidRequest ErrorKind = "invalid_request"
	KindUnauthorized   ErrorKind = "unauthorized"
	KindRateLimited    ErrorKind = "rate_limited"
	KindProvider       ErrorKind = "provider"
	KindUnsupported    ErrorKind = "unsupported"
	KindContext        ErrorKind = "context"
)

// Error is the stable error wrapper returned by aifd-llm adapters.
type Error struct {
	Kind       ErrorKind
	Provider   string
	StatusCode int
	Code       string
	Message    string
	Err        error
}

// Error returns a compact provider-safe error string.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	prefix := string(e.Kind)
	if e.Provider != "" {
		prefix = e.Provider + ":" + prefix
	}
	if e.StatusCode > 0 {
		prefix = fmt.Sprintf("%s:%d", prefix, e.StatusCode)
	}
	if e.Code != "" {
		prefix = prefix + ":" + e.Code
	}
	if e.Message != "" {
		return prefix + ": " + e.Message
	}
	if e.Err != nil {
		return prefix + ": " + e.Err.Error()
	}
	return prefix
}

// Unwrap returns the underlying error.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// NewError creates a classified llm error.
func NewError(kind ErrorKind, provider string, message string) *Error {
	return &Error{Kind: kind, Provider: provider, Message: message}
}

// WrapError wraps an underlying error with llm classification.
func WrapError(kind ErrorKind, provider string, message string, err error) *Error {
	return &Error{Kind: kind, Provider: provider, Message: message, Err: err}
}

// Unsupported returns a standardized unsupported-capability error.
func Unsupported(provider string, capability string) *Error {
	return NewError(KindUnsupported, provider, capability+" is unsupported")
}

// ProviderHTTPError creates a classified provider HTTP error.
func ProviderHTTPError(provider string, status int, code string, message string) *Error {
	kind := KindProvider
	switch status {
	case 400, 404, 409, 422:
		kind = KindInvalidRequest
	case 401, 403:
		kind = KindUnauthorized
	case 429:
		kind = KindRateLimited
	}
	return &Error{
		Kind:       kind,
		Provider:   provider,
		StatusCode: status,
		Code:       code,
		Message:    message,
	}
}

// ContextError maps context cancellation and deadline errors.
func ContextError(provider string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return WrapError(KindContext, provider, "llm request context ended", err)
	}
	return err
}

// IsKind reports whether err contains an llm Error of the requested kind.
func IsKind(err error, kind ErrorKind) bool {
	var llmErr *Error
	if !errors.As(err, &llmErr) {
		return false
	}
	return llmErr.Kind == kind
}
