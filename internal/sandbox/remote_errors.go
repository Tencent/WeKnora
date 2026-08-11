// Package sandbox: provider-neutral error contract for RemoteSandboxClient.
//
// Adapters normalize provider-native errors (HTTP status codes, SDK error
// types, gRPC codes) into RemoteError with a stable Kind so SessionBoundManager
// can make lifecycle decisions without knowing which backend is in use.
//
// Rule of thumb for adapters:
//
//   - NotFound / Terminal   → binding may be replaced
//   - Timeout / Unavailable / Capacity / Conflict / Authentication → PRESERVE binding
//   - InvalidRequest / Unsupported → surface to caller; do not touch binding
//   - Internal → last resort; preserve binding (default-preserve on ambiguity)
//
// The original cause is always retained via errors.Unwrap so callers can
// still inspect provider-specific detail when logging.
package sandbox

import (
	"errors"
	"fmt"
	"net/http"
)

// RemoteErrorKind is the stable, provider-neutral classification of a
// RemoteSandboxClient failure.
type RemoteErrorKind string

const (
	// RemoteErrorKindNotFound: sandbox / file / path does not exist.
	RemoteErrorKindNotFound RemoteErrorKind = "not_found"

	// RemoteErrorKindTerminal: sandbox is permanently gone (killed,
	// crashed, terminated). Semantically equivalent to NotFound for
	// lifecycle purposes but distinct for observability.
	RemoteErrorKindTerminal RemoteErrorKind = "terminal"

	// RemoteErrorKindAuthentication: bad credentials, expired token, etc.
	RemoteErrorKindAuthentication RemoteErrorKind = "authentication"

	// RemoteErrorKindInvalidRequest: caller-side error (bad template ID,
	// bad path, oversized payload). Not retryable.
	RemoteErrorKindInvalidRequest RemoteErrorKind = "invalid_request"

	// RemoteErrorKindUnsupported: provider does not implement the requested
	// capability (e.g. metadata, "never" timeout).
	RemoteErrorKindUnsupported RemoteErrorKind = "unsupported"

	// RemoteErrorKindConflict: concurrent modification (e.g. sandbox state
	// changed under us, template being built).
	RemoteErrorKindConflict RemoteErrorKind = "conflict"

	// RemoteErrorKindCapacity: quota / rate-limit / out-of-capacity.
	RemoteErrorKindCapacity RemoteErrorKind = "capacity"

	// RemoteErrorKindTimeout: request exceeded its deadline before the
	// provider responded. Distinct from execution timeout, which is a
	// normal RemoteExecResult with Killed=true.
	RemoteErrorKindTimeout RemoteErrorKind = "timeout"

	// RemoteErrorKindUnavailable: transient provider outage (5xx, network
	// error, control-plane unreachable).
	RemoteErrorKindUnavailable RemoteErrorKind = "unavailable"

	// RemoteErrorKindInternal: catch-all for unclassified failures.
	// Adapters SHOULD narrow this to a more specific kind when possible.
	RemoteErrorKindInternal RemoteErrorKind = "internal"
)

// RemoteError is the wire-agnostic error type returned by every
// RemoteSandboxClient method.
type RemoteError struct {
	// Kind classifies the failure for coordinator decisions.
	Kind RemoteErrorKind

	// Provider identifies which backend produced the error, for logging.
	Provider RemoteProvider

	// Op names the RemoteSandboxClient operation (e.g. "Create", "Exec").
	Op string

	// Message is a human-readable summary. Provider-specific status codes
	// or SDK error strings belong here.
	Message string

	// StatusCode is the provider's HTTP status when the failure came from an
	// HTTP response, or 0 when it did not. Kind deliberately collapses statuses
	// that call for the same lifecycle decision, so diagnostics that must tell
	// those statuses apart read this instead of re-parsing Message.
	StatusCode int

	// Cause is the original provider-side error, retained for
	// errors.Unwrap so callers can still errors.Is / errors.As it.
	Cause error
}

// Error implements the error interface.
func (e *RemoteError) Error() string {
	if e == nil {
		return "<nil remote error>"
	}
	prov := string(e.Provider)
	if prov == "" {
		prov = "remote"
	}
	if e.Message == "" && e.Cause != nil {
		return fmt.Sprintf("%s %s: %s: %v", prov, e.Op, e.Kind, e.Cause)
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s %s: %s: %s: %v", prov, e.Op, e.Kind, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s %s: %s: %s", prov, e.Op, e.Kind, e.Message)
}

// Unwrap exposes the wrapped provider-native error for errors.Is / errors.As.
func (e *RemoteError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// NewRemoteError builds a RemoteError. Convenience for adapters.
func NewRemoteError(provider RemoteProvider, op string, kind RemoteErrorKind, message string, cause error) *RemoteError {
	return &RemoteError{
		Kind:     kind,
		Provider: provider,
		Op:       op,
		Message:  message,
		Cause:    cause,
	}
}

// remoteKind extracts the Kind from err, or "" when err is not a
// *RemoteError. Wraps errors.As so callers can pass any error value.
func remoteKind(err error) RemoteErrorKind {
	if err == nil {
		return ""
	}
	var re *RemoteError
	if errors.As(err, &re) {
		return re.Kind
	}
	return ""
}

// IsRemoteNotFound reports whether err classifies as sandbox / path not found.
// Both NotFound and Terminal are treated as "gone" for coordinator decisions.
func IsRemoteNotFound(err error) bool {
	switch remoteKind(err) {
	case RemoteErrorKindNotFound, RemoteErrorKindTerminal:
		return true
	default:
		return false
	}
}

// IsRemoteInvalidRequest reports whether err classifies as a caller-side
// error (bad template, oversized payload, malformed path, etc.).
func IsRemoteInvalidRequest(err error) bool {
	return remoteKind(err) == RemoteErrorKindInvalidRequest
}

// CanReplaceRemoteBinding reports whether the error proves that the bound
// remote sandbox is permanently gone. This is intentionally allow-list based:
// unknown and newly introduced errors preserve bindings by default.
func CanReplaceRemoteBinding(err error) bool {
	switch remoteKind(err) {
	case RemoteErrorKindNotFound, RemoteErrorKindTerminal:
		return true
	default:
		return false
	}
}

// httpErrorKind maps an HTTP status code to a RemoteErrorKind. It is shared
// by all remote sandbox adapter backends (Cube, E2B).
func httpErrorKind(op string, status int) RemoteErrorKind {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return RemoteErrorKindInvalidRequest
	case http.StatusUnauthorized, http.StatusForbidden:
		return RemoteErrorKindAuthentication
	case http.StatusNotFound:
		if op == "Create" {
			return RemoteErrorKindInvalidRequest
		}
		return RemoteErrorKindNotFound
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return RemoteErrorKindTimeout
	case http.StatusConflict:
		return RemoteErrorKindConflict
	case http.StatusGone:
		return RemoteErrorKindTerminal
	case http.StatusTooManyRequests, http.StatusInsufficientStorage:
		return RemoteErrorKindCapacity
	default:
		if status >= 500 {
			return RemoteErrorKindUnavailable
		}
		return RemoteErrorKindInternal
	}
}
