package cmdutil

import (
	"errors"
	"fmt"
	"io"

	"github.com/Tencent/WeKnora/cli/internal/output"
)

// globalFormatMode tracks the resolved --format value for the current invocation.
// Set by cmd/root.go in PersistentPreRunE; used by PrintError to choose text vs envelope.
var globalFormatMode string

// SetFormatMode records the resolved --format mode for the current invocation.
// Called by cmd/root.go PersistentPreRunE after FormatOptions.ResolveDefault.
func SetFormatMode(mode string) {
	globalFormatMode = mode
}

// globalProfile tracks the resolved profile name for the current invocation.
// Set by cmd/root.go in PersistentPreRunE via SetProfile; read by Emit and
// init events to populate envelope.profile / NDJSON init.profile.
var globalProfile string

// SetProfile records the resolved profile name for the current invocation.
// Called by cmd/root.go PersistentPreRunE after SetFormatMode.
func SetProfile(name string) { globalProfile = name }

// GetProfile returns the profile name recorded for the current invocation.
// Empty string when nothing is configured (omitempty fields suppress the field).
func GetProfile() string { return globalProfile }

// ExitCode maps an error to the documented CLI exit code.
//   - 0  success
//   - 1  generic / unknown typed error - fallback bucket: resource.already_exists,
//     resource.locked, local.*, mcp.*, operation.failed, server.session_create_failed
//     (workflow-level, see special case below), and any code outside the named
//     buckets below
//   - 2  cobra-parse problem (unrecognised flag, arg-count violation) —
//     typed input.unknown_subcommand from the guard maps to exit 5
//     (input.* bucket); only ungated cobra prose lands here
//   - 3  auth.*
//   - 4  resource.not_found
//   - 5  input.* (other than confirmation_required)
//   - 6  server.rate_limited
//   - 7  server.* (other than rate_limited/session_create_failed) / network.*
//   - 10 input.confirmation_required - high-risk write needs explicit -y
//     (see cli/README.md)
//   - 124 operation.timeout - CLI-level wait/poll exhausted its --timeout window
//     (matches the convention from GNU `timeout`)
//   - 130 SIGINT (handled by Go runtime, not this function)
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var fe *FlagError
	if errors.As(err, &fe) {
		return 2
	}
	if errors.Is(err, SilentError) {
		return 1
	}
	if matchCode(err, CodeInputConfirmationRequired) {
		return 10
	}
	if IsAuthError(err) {
		return 3
	}
	if IsNotFound(err) {
		return 4
	}
	if matchPrefix(err, "input.") {
		return 5
	}
	if matchCode(err, CodeServerRateLimited) {
		return 6
	}
	// server.session_create_failed is a workflow-level failure (the hint
	// asks the caller to pass --session, not to retry with backoff), so it
	// falls through to exit 1 rather than the server.* transient bucket.
	if matchCode(err, CodeSessionCreateFailed) {
		return 1
	}
	if matchPrefix(err, "server.") || matchPrefix(err, "network.") {
		return 7
	}
	if matchCode(err, CodeOperationTimeout) {
		return 124
	}
	return 1
}

// PrintError writes err to w (typically stderr) in dual mode:
//   - text/human: code: msg\nhint: ...\nretry: ...
//   - json/ndjson: {ok:false, error:{...}, _notice?:...}
//
// Mode is read from globalFormatMode (set by root PersistentPreRunE).
func PrintError(w io.Writer, err error) {
	if err == nil || errors.Is(err, SilentError) {
		return
	}
	// Typed *Error with Silent=true suppresses stderr emit while preserving
	// the Code for ExitCode. Used by batch paths that already wrote per-item
	// detail to stdout (cmdutil.RunBatch) — emitting a summary envelope on
	// stderr would duplicate the failure signal.
	if typed := AsError(err); typed != nil && typed.Silent {
		return
	}

	if globalFormatMode == "json" || globalFormatMode == "ndjson" {
		printErrorEnvelope(w, err)
		return
	}
	printErrorProse(w, err)
}

func printErrorProse(w io.Writer, err error) {
	fmt.Fprintln(w, err.Error())
	var typed *Error
	if errors.As(err, &typed) {
		hint := typed.Hint
		if hint == "" {
			hint = defaultHint(typed.Code)
		}
		if hint != "" {
			fmt.Fprintf(w, "hint: %s\n", hint)
		}
		retry := typed.RetryCommand
		if retry == "" {
			retry = defaultRetryCommand(typed.Code)
		}
		if retry != "" {
			fmt.Fprintf(w, "retry: %s\n", retry)
		}
	}
}

func printErrorEnvelope(w io.Writer, err error) {
	_ = output.WriteErrorEnvelope(w, ErrorToDetail(err), false)
}

// defaultHint returns a canonical actionable hint for known error codes
// when the call site didn't set one. `auth.unauthenticated` always points
// at `weknora auth login` - covers the broad surface (auth status / kb
// list / kb view / search) without per-command hint plumbing.
//
// Empty string for codes without a stable canonical hint.
func defaultHint(code ErrorCode) string {
	switch code {
	case CodeAuthUnauthenticated, CodeAuthBadCredential:
		return "run `weknora auth login`"
	case CodeAuthTokenExpired:
		return "your session expired; run `weknora auth login` to re-authenticate"
	case CodeAuthForbidden:
		return "active profile lacks permission for this resource"
	case CodeAuthCrossTenantBlocked, CodeAuthTenantMismatch:
		return "verify tenant profile with `weknora auth status`"
	case CodeNetworkError:
		return "check base URL reachability with `weknora doctor`"
	case CodeServerIncompatibleVersion:
		return "run `weknora doctor` to see version skew details"
	case CodeServerRateLimited:
		return "rate-limited; retry after a few seconds"
	case CodeServerTimeout:
		return "request timed out; retry, or run `weknora doctor` to check connectivity"
	case CodeResourceNotFound:
		return "verify the resource ID and try again"
	case CodeInputInvalidArgument, CodeInputMissingFlag:
		return "see `weknora <command> --help` for valid usage"
	case CodeInputConfirmationRequired:
		return "high-risk write - re-run with -y/--yes after the user explicitly approves"
	case CodeLocalKeychainDenied:
		return "verify keyring access; falls back to file storage"
	case CodeLocalConfigCorrupt:
		return "remove ~/.config/weknora/config.yaml and re-run `weknora auth login`"
	case CodeLocalFileIO:
		return "check file permissions under $XDG_CONFIG_HOME/weknora/"
	case CodeKBIDRequired:
		return "run `weknora link` to bind this directory to a knowledge base, or pass --kb"
	case CodeKBNotFound:
		return "list available with `weknora kb list`"
	case CodeProjectLinkCorrupt:
		return "remove .weknora/project.yaml and run `weknora link` again"
	case CodeUserAborted:
		return "no action taken; pass -y/--yes to skip the confirmation prompt"
	case CodeUploadFileNotFound:
		return "verify the path is correct and readable"
	case CodeSSEStreamAborted:
		return "the streaming answer was cut off mid-flight; retry, or pass --format json to buffer the full response"
	case CodeSessionCreateFailed:
		return "could not create a chat session; pass --session to reuse an existing session"
	case CodeOperationTimeout:
		return "wait timed out; raise --timeout or check the underlying job"
	case CodeOperationCancelled:
		return "operation cancelled by signal (Ctrl-C / SIGTERM)"
	}
	return ""
}

// defaultRetryCommand returns canonical retry argv for known codes.
// Empty string for codes without a stable canonical retry.
// Symmetric counterpart to defaultHint.
func defaultRetryCommand(code ErrorCode) string {
	switch code {
	case CodeAuthUnauthenticated, CodeAuthBadCredential, CodeAuthTokenExpired:
		return "weknora auth login"
	case CodeKBIDRequired:
		return "weknora link"
	case CodeNetworkError, CodeServerTimeout:
		return "weknora doctor"
	case CodeProjectLinkCorrupt:
		return "weknora link" // 重新绑定
	case CodeLocalConfigCorrupt:
		// 删 config + 重 login 是两步；prose hint 已说明，retry argv 留空
		return ""
	}
	return ""
}

// DefaultHint and DefaultRetryCommand are exported wrappers so that
// cross-package callers (MCP handlers, batch envelope helpers) can
// resolve hint/retry without duplicating the typed-code → string table.
// Avoids drift between cmdutil and copies elsewhere.
func DefaultHint(code ErrorCode) string         { return defaultHint(code) }
func DefaultRetryCommand(code ErrorCode) string { return defaultRetryCommand(code) }
