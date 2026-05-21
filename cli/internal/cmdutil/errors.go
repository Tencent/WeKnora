// Package cmdutil contains the Factory, Options helpers, error types,
// JSON-flag wiring, and the Exporter abstraction shared by all commands.
package cmdutil

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/cli/internal/output"
)

// ErrorCode is a namespaced stable identifier emitted on stderr in the
// `code: message` failure line. SemVer governance: v0.x maintains the
// registry below; new codes are noted in release notes.
type ErrorCode string

const (
	// auth.* - authentication / permission
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

	// input.* - flag and argument validation
	CodeInputInvalidArgument ErrorCode = "input.invalid_argument"
	CodeInputMissingFlag     ErrorCode = "input.missing_flag"
	// CodeInputConfirmationRequired marks a high-risk write that has no
	// interactive UI (non-TTY or JSON-output mode) and was invoked without
	// -y/--yes. Mapped to exit code 10 (see cli/README.md). Agents must
	// surface the error to the user and only retry with -y after explicit
	// human approval; never auto-retry.
	CodeInputConfirmationRequired ErrorCode = "input.confirmation_required"
	// CodeInputUnknownSubcommand marks an invocation that reached a parent
	// command path but the first positional argument did not match any
	// registered subcommand. Detail includes an `available` list so agents
	// can surface valid choices without re-invoking; retry with `<path> --help`.
	CodeInputUnknownSubcommand ErrorCode = "input.unknown_subcommand"

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

	// operation.* - CLI-level wait/poll results
	// CodeOperationTimeout marks a CLI-level wait/poll operation that exhausted
	// its --timeout window. Distinct from CodeServerTimeout (HTTP 504). Mapped
	// to exit 124 (matches the convention from GNU `timeout`).
	CodeOperationTimeout ErrorCode = "operation.timeout"
	// CodeOperationFailed marks a CLI-level wait/poll operation where one or
	// more targets reached a terminal failure (e.g. doc wait found a doc with
	// parse_status=failed). Distinct from server.* / network.* because the
	// failure is the target's own terminal state, not a transient transport
	// issue. Maps to exit 1 via the fall-through bucket.
	CodeOperationFailed ErrorCode = "operation.failed"
	// CodeOperationCancelled marks a long-running command interrupted by a
	// caught signal (SIGINT / SIGTERM after main.go's signal.NotifyContext
	// fires). Distinct from CodeUserAborted (declined confirm prompt) — the
	// hints differ. main.go overrides the exit code to 130 for cancelled
	// contexts so the user-visible exit follows Unix signal convention.
	CodeOperationCancelled ErrorCode = "operation.cancelled"

	// local.* - config / file / keychain on the user's machine
	CodeLocalConfigCorrupt   ErrorCode = "local.config_corrupt"
	CodeLocalKeychainDenied  ErrorCode = "local.keychain_denied"
	CodeLocalFileIO          ErrorCode = "local.file_io"
	CodeLocalUnimplemented   ErrorCode = "local.unimplemented"
	CodeLocalProfileNotFound ErrorCode = "local.profile_not_found"
	// KB-resolution chain and project-link codes.
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
	Code    ErrorCode
	Message string
	Hint    string
	Cause   error
	// Silent suppresses PrintError's stderr output while preserving the
	// typed Code for ExitCode. Set by commands that already wrote their
	// own output (e.g. bulk operations reporting partial-success data on
	// stdout) but still need to surface a non-zero exit code.
	Silent            bool
	RetryCommand      string // Directly-executable argv, distinct from prose Hint
	RetryAfterSeconds int    // HTTP Retry-After header semantics (transport-level retry hint)
	Detail            any    // Structured detail for envelope.error.detail (e.g. unknown-subcommand available[])
	Risk              *RiskInfo
}

// RiskInfo tags an error with destructive-write metadata that surfaces
// in the wire envelope's error.risk field.
type RiskInfo struct {
	Level  string
	Action string
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

// WithHint sets a prose-style actionable hint.
func (e *Error) WithHint(hint string) *Error {
	e.Hint = hint
	return e
}

// WithRetryCommand sets the directly-executable retry argv string.
// Agent 端不用 regex 从 prose hint 提 argv。
// Empty string for codes without a canonical retry command.
func (e *Error) WithRetryCommand(cmd string) *Error {
	e.RetryCommand = cmd
	return e
}

// WithRetryAfter sets the retry_after_seconds hint (from HTTP Retry-After header).
func (e *Error) WithRetryAfter(s int) *Error {
	e.RetryAfterSeconds = s
	return e
}

// WithDetail attaches structured error.detail (e.g. unknown-subcommand available[]).
func (e *Error) WithDetail(d any) *Error {
	e.Detail = d
	return e
}

// WithRisk tags a high-risk write (destructive deletes etc.) for the agent protocol.
func (e *Error) WithRisk(level, action string) *Error {
	e.Risk = &RiskInfo{Level: level, Action: action}
	return e
}

// AsError unwraps to *Error if the chain contains one. Returns nil if not found.
func AsError(err error) *Error {
	var typed *Error
	if errors.As(err, &typed) {
		return typed
	}
	return nil
}

// ErrorToDetail converts a typed cmdutil.Error (or fallback) into
// output.ErrDetail for embedding in success-envelope batch items or
// MCP CallToolResult StructuredContent. Hint / RetryCommand fall back
// to defaultHint / defaultRetryCommand when typed value is empty.
// Returns nil when err is nil.
func ErrorToDetail(err error) *output.ErrDetail {
	if err == nil {
		return nil
	}
	if typed := AsError(err); typed != nil {
		hint := typed.Hint
		if hint == "" {
			hint = defaultHint(typed.Code)
		}
		retry := typed.RetryCommand
		if retry == "" {
			retry = defaultRetryCommand(typed.Code)
		}
		// Build message without the code prefix — the envelope's separate
		// "type" field already carries the code, so repeating it in "message"
		// causes agents that render "{type}: {message}" to produce a doubled
		// prefix (e.g. "resource.not_found: resource.not_found: ...").
		msg := typed.Message
		if typed.Cause != nil {
			msg = fmt.Sprintf("%s: %v", typed.Message, typed.Cause)
		}
		detail := &output.ErrDetail{
			Type:              string(typed.Code),
			Message:           msg,
			Hint:              hint,
			RetryCommand:      retry,
			RetryAfterSeconds: typed.RetryAfterSeconds,
			Detail:            typed.Detail,
		}
		if typed.Risk != nil {
			detail.Risk = &output.RiskDetail{Level: typed.Risk.Level, Action: typed.Risk.Action}
		}
		return detail
	}
	// Cobra parse / arg-count errors flow through cmdutil.NewFlagError —
	// surface them as input.invalid_argument so the wire envelope carries a
	// useful typed code instead of the unclassified "internal.error" bucket.
	// ExitCode separately maps FlagError → 2.
	var fe *FlagError
	if errors.As(err, &fe) {
		return &output.ErrDetail{
			Type:    string(CodeInputInvalidArgument),
			Message: err.Error(),
			Hint:    defaultHint(CodeInputInvalidArgument),
		}
	}
	return &output.ErrDetail{Type: "internal.error", Message: err.Error()}
}

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
// call site - single source for the wrap-and-classify policy.
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

// Typed predicates - use these instead of comparing ErrorCode strings.
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

// serverNotFoundRE matches the WeKnora server's structured error-envelope body
// for the typed "not found" code (1003 = ErrNotFound). Server's 1007 is the
// generic ErrInternalServer bucket — including it would mis-classify every
// validation / DB failure (e.g. SQLSTATE 22001 "value too long") as
// resource.not_found, sending agents down the wrong recovery path.
// Matching the structured "code":1003 anchor avoids the free-substring false
// positive (e.g. a stack trace containing "config file not found").
var serverNotFoundRE = regexp.MustCompile(`"code":1003\b`)

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
	base := ClassifyHTTPStatus(status)
	// Server-side 500-misclassification rescue: some servers return HTTP 500
	// for logical "not found" cases (e.g. code 1007 "knowledge base not found",
	// code 1003 "Knowledge not found") instead of 404. Match the server's known
	// error-code envelope precisely to avoid false-positive rescues on generic
	// 500 bodies that happen to contain "not found" (e.g. "config file not found
	// in stack trace"). The free-substring match would over-match.
	body := rest[end+1:]
	if base == CodeServerError && serverNotFoundRE.MatchString(body) {
		return CodeResourceNotFound
	}
	return base
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
		CodeInputUnknownSubcommand,
		// server / network
		CodeServerError, CodeServerTimeout, CodeServerRateLimited,
		CodeServerIncompatibleVersion, CodeNetworkError,
		// local
		CodeLocalConfigCorrupt, CodeLocalKeychainDenied, CodeLocalFileIO,
		CodeLocalUnimplemented, CodeLocalProfileNotFound,
		CodeKBIDRequired, CodeKBNotFound,
		CodeProjectLinkCorrupt,
		CodeUserAborted, CodeUploadFileNotFound,
		CodeSSEStreamAborted, CodeSessionCreateFailed,
		// operation
		CodeOperationTimeout, CodeOperationFailed, CodeOperationCancelled,
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
		CodeNetworkError,          // non-HTTP error
	}
}

// IsCancelled reports whether err is a context cancellation, either via
// the context itself or via wrapped CancelError / context.Canceled /
// context.DeadlineExceeded. Used by streaming commands to distinguish
// SIGINT-driven shutdown from real errors.
func IsCancelled(ctx context.Context, err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	if ctx.Err() == context.Canceled {
		return true
	}
	return false
}
