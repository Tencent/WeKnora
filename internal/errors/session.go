package errors

import "errors"

var (
	// ErrSessionNotFound session not found error
	ErrSessionNotFound = errors.New("session not found")
	// ErrSessionExpired session expired error
	ErrSessionExpired = errors.New("session expired")
	// ErrSessionLimitExceeded session limit exceeded error
	ErrSessionLimitExceeded = errors.New("session limit exceeded")
	// ErrInvalidSessionID invalid session ID error
	ErrInvalidSessionID = errors.New("invalid session id")
	// ErrInvalidTenantID invalid tenant ID error
	ErrInvalidTenantID = errors.New("invalid tenant id")
)
