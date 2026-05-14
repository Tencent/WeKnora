// Package cmdutil contains the Factory, Options helpers, error types,
// JSON-flag wiring, and the Exporter abstraction shared by all commands.
package cmdutil

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrorCode is a namespaced stable identifier emitted on stderr in the
// `code: message` failure line. SemVer governance: v0.x maintains the
// registry below; new codes are noted in release notes.
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
	CodeInputInvalidArgument     ErrorCode = "input.invalid_argument"
	CodeInputMissingFlag         ErrorCode = "input.missing_flag"
	// CodeInputConfirmationRequired marks a high-risk write that has no
	// interactive UI (non-TTY or --json) and was invoked without -y/--yes.
	// Mapped to exit code 10 (see cli/AGENTS.md). Agents must surface the
	// error to the user and only retry with -y after explicit human
	// approval; never auto-retry.
	CodeInputConfirmationRequired ErrorCode = "input.confirmation_required"

	// server.* / network.*
	CodeServerError               ErrorCode = "server.error"
	CodeServerTimeout             ErrorCode = "server.timeout"
	CodeServerRateLimited         ErrorCode = "server.rate_limited"
	CodeServerIncompatibleVersion ErrorCode = "server.incompatible_version"
	CodeNetworkError              ErrorCode = "network.error"
	// CodeSessionCreateFailed marks a chat invocation where the auto-created
	// session POST failed. Surfaced as a typed code distinct from generic
	// server.error so agents can retry with their own --session.
	CodeSessionCreateFailed ErrorCode = "server.session_create_failed"

	// local.* — config / file / keychain on the user's machine
	CodeLocalConfigCorrupt   ErrorCode = "local.config_corrupt"
	CodeLocalKeychainDenied  ErrorCode = "local.keychain_denied"
	CodeLocalFileIO          ErrorCode = "local.file_io"
	CodeLocalUnimplemented   ErrorCode = "local.unimplemented"
	CodeLocalContextNotFound ErrorCode = "local.context_not_found"
	// v0.2 KB-resolution chain (spec §1.3) and project-link (spec §2.4) codes.
	CodeKBIDRequired       ErrorCode = "local.kb_id_required"
	CodeKBNotFound         ErrorCode = "local.kb_not_found"
	CodeProjectLinkCorrupt ErrorCode = "local.project_link_corrupt"
	// CodeUserAborted marks a user-cancelled destructive operation (declined a
	// confirm prompt). Distinct from SilentError so the stderr line still
	// carries a stable code; distinct from input.* because the user supplied
	// valid args and simply chose not to proceed.
	CodeUserAborted ErrorCode = "local.user_aborted"
	// CodeUploadFileNotFound marks a `weknora doc upload` invocation pointing at
	// a path that does not exist. Distinct from CodeLocalFileIO (permission /
	// disk-fault) so the hint can name the actual culprit.
	CodeUploadFileNotFound ErrorCode = "local.upload_file_not_found"
	// CodeSSEStreamAborted marks a streaming RAG response that began producing
	// data and then dropped before the SDK observed a Done event. Distinct
	// from network.error (pre-stream transport failure) so users see the
	// stream specifically aborted, not a connection that never opened.
	CodeSSEStreamAborted ErrorCode = "local.sse_stream_aborted"

	// mcp.*
	CodeMCPReadonlyMode   ErrorCode = "mcp.readonly_mode"
	CodeMCPToolNotAllowed ErrorCode = "mcp.tool_not_allowed"
	CodeMCPSchemaUnknown  ErrorCode = "mcp.schema_unknown_command"
)

// Error is the typed error implementations carry through the call stack.
// RunE returns a *Error and the root command renders it on stderr in
// `code: message[: cause]\nhint: ...` form. Exit code is derived by
// ExitCode().
type Error struct {
	Code       ErrorCode
	Message    string
	Hint       string
	Cause      error
	Retryable  bool
	HTTPStatus int
	// Silent suppresses PrintError's stderr output while preserving the
	// typed Code for ExitCode. Set by commands that already wrote their
	// own output (e.g. bulk operations reporting partial-success data on
	// stdout) but still need to surface a non-zero exit code.
	Silent bool
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

// WrapHTTP wraps a transport / response error with the typed code derived
// from its HTTP shape (404 → resource.not_found, 401 → auth.unauthenticated,
// non-HTTP → network.error, …). Shortcut for the universal pattern
// `Wrapf(ClassifyHTTPError(err), err, format, args...)` used by every SDK
// call site — single source for the wrap-and-classify policy.
//
// Use this for any error returned from a wire call. Stays paired with
// ClassifyHTTPErrorOutputs() in the acceptance/contract test, which
// enumerates the codes this helper can yield.
func WrapHTTP(cause error, format string, args ...any) *Error {
	return Wrapf(ClassifyHTTPError(cause), cause, format, args...)
}

// FlagError signals user-visible flag/argument problems; the root command
// prints help on top of the message and exits 2.
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

// Typed predicates — use these instead of comparing ErrorCode strings.
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

// ClassifyHTTPStatus maps an HTTP status code to the canonical ErrorCode.
// Single source of truth so error codes stay aligned whether the failure
// was detected by the SDK (string-formatted error) or by the CLI directly
// (e.g. raw passthrough reading resp.StatusCode).
func ClassifyHTTPStatus(status int) ErrorCode {
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

// ClassifyHTTPError maps an SDK HTTP error to the canonical ErrorCode by
// parsing the "HTTP error <status>: ..." message format the SDK currently
// emits (client.parseResponse). Until the SDK exposes a typed APIError this
// is the lowest-friction way to surface 401/404/429/etc. as the right
// typed code instead of every server-side problem collapsing to
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
	return ClassifyHTTPStatus(status)
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
		CodeInputInvalidArgument, CodeInputMissingFlag, CodeInputConfirmationRequired,
		// server / network
		CodeServerError, CodeServerTimeout, CodeServerRateLimited,
		CodeServerIncompatibleVersion, CodeNetworkError,
		// local
		CodeLocalConfigCorrupt, CodeLocalKeychainDenied, CodeLocalFileIO,
		CodeLocalUnimplemented, CodeLocalContextNotFound,
		CodeKBIDRequired, CodeKBNotFound,
		CodeProjectLinkCorrupt,
		CodeUserAborted, CodeUploadFileNotFound,
		CodeSSEStreamAborted, CodeSessionCreateFailed,
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
