// Package cmdutil contains the Factory, Options helpers, error types,
// JSON-flag wiring, and the Exporter abstraction shared by all commands.
package cmdutil

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrorCode is a namespaced stable identifier carried in the failure envelope.
// SemVer governance: v0.x maintains the registry below; new codes are noted
// in release notes. v0.9 introduces a CI compat test (see ADR-6b).
type ErrorCode string

const (
	// auth.* — authentication / permission
	CodeAuthUnauthenticated    ErrorCode = "auth.unauthenticated"
	CodeAuthTokenExpired       ErrorCode = "auth.token_expired"
	CodeAuthBadCredential      ErrorCode = "auth.bad_credential"
	CodeAuthForbidden          ErrorCode = "auth.forbidden"
	CodeAuthCrossTenantBlocked ErrorCode = "auth.cross_tenant_blocked"
	CodeAuthTenantMismatch     ErrorCode = "auth.tenant_mismatch"

	// resource.*
	CodeResourceNotFound      ErrorCode = "resource.not_found"
	CodeResourceAlreadyExists ErrorCode = "resource.already_exists"
	CodeResourceLocked        ErrorCode = "resource.locked"

	// input.* — flag and argument validation
	CodeInputInvalidArgument ErrorCode = "input.invalid_argument"
	CodeInputMissingFlag     ErrorCode = "input.missing_flag"

	// server.* / network.*
	CodeServerError               ErrorCode = "server.error"
	CodeServerTimeout             ErrorCode = "server.timeout"
	CodeServerRateLimited         ErrorCode = "server.rate_limited"
	CodeServerIncompatibleVersion ErrorCode = "server.incompatible_version"
	CodeNetworkError              ErrorCode = "network.error"

	// local.* — config / file / keychain on the user's machine
	CodeLocalConfigCorrupt   ErrorCode = "local.config_corrupt"
	CodeLocalKeychainDenied  ErrorCode = "local.keychain_denied"
	CodeLocalFileIO          ErrorCode = "local.file_io"
	CodeLocalUnimplemented   ErrorCode = "local.unimplemented"
	CodeLocalContextNotFound ErrorCode = "local.context_not_found"

	// mcp.*
	CodeMCPReadonlyMode   ErrorCode = "mcp.readonly_mode"
	CodeMCPToolNotAllowed ErrorCode = "mcp.tool_not_allowed"
	CodeMCPSchemaUnknown  ErrorCode = "mcp.schema_unknown_command"
)

// Error is the typed error implementations carry through the call stack.
// RunE returns a *Error and the root command formats it into the envelope.
type Error struct {
	Code       ErrorCode
	Message    string
	Hint       string
	Cause      error
	Retryable  bool
	HTTPStatus int
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

// NewError constructs a typed error.
func NewError(code ErrorCode, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Wrapf wraps cause with a typed code and Sprintf-style message.
func Wrapf(code ErrorCode, cause error, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Cause: cause}
}

// FlagError signals user-visible flag/argument problems; the root command
// prints help on top of the message and exits 2 (gh-style).
type FlagError struct{ err error }

func (e *FlagError) Error() string { return e.err.Error() }
func (e *FlagError) Unwrap() error { return e.err }

// NewFlagError wraps err as a FlagError.
func NewFlagError(err error) error { return &FlagError{err: err} }

// SilentError skips printing to stderr; useful when a command has already
// emitted a fully-formatted message and exits non-zero.
var SilentError = errors.New("silent error (handled)")

// CancelError marks a user-cancelled operation (Ctrl-C / "no" at confirm).
var CancelError = errors.New("operation cancelled")

// Typed predicates — use these instead of comparing ErrorCode strings (Stripe pattern).
// They walk the error chain so wrapped errors still match.

// IsAuthError matches any auth.* code.
func IsAuthError(err error) bool { return matchPrefix(err, "auth.") }

// IsNotFound matches resource.not_found.
func IsNotFound(err error) bool { return matchCode(err, CodeResourceNotFound) }

// IsTransient matches network.* and server.timeout / rate_limited (worth retrying).
func IsTransient(err error) bool {
	return matchPrefix(err, "network.") ||
		matchCode(err, CodeServerTimeout) ||
		matchCode(err, CodeServerRateLimited)
}

// IsAuthExpired matches auth.token_expired.
func IsAuthExpired(err error) bool { return matchCode(err, CodeAuthTokenExpired) }

// matchCode returns true if err (or anything it wraps) is a *Error with code == c.
// errors.As walks the wrap chain itself; the explicit unwrap loop is unnecessary.
func matchCode(err error, c ErrorCode) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == c
}

// matchPrefix returns true if err (or anything it wraps) is a *Error whose code
// has the given namespace prefix (e.g. "auth.").
func matchPrefix(err error, prefix string) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return strings.HasPrefix(string(e.Code), prefix)
}

// ClassifyHTTPError maps an SDK HTTP error to the canonical ErrorCode by
// parsing the "HTTP error <status>: ..." message format the SDK currently
// emits (client.parseResponse). Until the SDK exposes a typed APIError this
// is the lowest-friction way to surface 401/404/429/etc. as the right
// envelope code instead of every server-side problem collapsing to
// server.error.
//
// Returns CodeNetworkError when err is not an HTTP error (transport / DNS),
// and CodeServerError when the status can't be parsed.
func ClassifyHTTPError(err error) ErrorCode {
	if err == nil {
		return ""
	}
	msg := err.Error()
	rest, ok := strings.CutPrefix(msg, "HTTP error ")
	if !ok {
		return CodeNetworkError
	}
	end := strings.IndexByte(rest, ':')
	if end <= 0 {
		return CodeServerError
	}
	status, perr := strconv.Atoi(rest[:end])
	if perr != nil {
		return CodeServerError
	}
	switch {
	case status == 401:
		return CodeAuthUnauthenticated
	case status == 403:
		return CodeAuthForbidden
	case status == 404:
		return CodeResourceNotFound
	case status == 409:
		return CodeResourceAlreadyExists
	case status == 429:
		return CodeServerRateLimited
	case status >= 500:
		return CodeServerError
	case status >= 400:
		return CodeInputInvalidArgument
	}
	return CodeServerError
}

// AllCodes returns the registered error code set.
// Used by acceptance/contract/errorcodes_test.go to validate that every code
// referenced in cli/cmd/ is present here. Update this list whenever a new
// ErrorCode constant is added above.
func AllCodes() []ErrorCode {
	return []ErrorCode{
		// auth
		CodeAuthUnauthenticated, CodeAuthTokenExpired, CodeAuthBadCredential,
		CodeAuthForbidden, CodeAuthCrossTenantBlocked, CodeAuthTenantMismatch,
		// resource
		CodeResourceNotFound, CodeResourceAlreadyExists, CodeResourceLocked,
		// input
		CodeInputInvalidArgument, CodeInputMissingFlag,
		// server / network
		CodeServerError, CodeServerTimeout, CodeServerRateLimited,
		CodeServerIncompatibleVersion, CodeNetworkError,
		// local
		CodeLocalConfigCorrupt, CodeLocalKeychainDenied, CodeLocalFileIO,
		CodeLocalUnimplemented, CodeLocalContextNotFound,
		// mcp
		CodeMCPReadonlyMode, CodeMCPToolNotAllowed, CodeMCPSchemaUnknown,
	}
}

// ClassifyHTTPErrorOutputs returns every code that ClassifyHTTPError can return.
// Bridges the AST-friendly literal model with the dynamic switch inside
// ClassifyHTTPError. errorcodes_test.go uses this to seed the "referenced codes"
// set without trying to AST-introspect a function-call expression.
//
// IMPORTANT: keep in sync with the switch in ClassifyHTTPError.
func ClassifyHTTPErrorOutputs() []ErrorCode {
	return []ErrorCode{
		CodeAuthUnauthenticated,   // 401
		CodeAuthForbidden,         // 403
		CodeResourceNotFound,      // 404
		CodeResourceAlreadyExists, // 409
		CodeServerRateLimited,     // 429
		CodeServerError,           // 5xx / parse-failure / default
		CodeInputInvalidArgument,  // 4xx (else)
		CodeNetworkError,          // 非 HTTP error
	}
}
