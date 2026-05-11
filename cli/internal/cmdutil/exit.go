package cmdutil

import (
	"errors"
	"fmt"
	"io"

	"github.com/Tencent/WeKnora/cli/internal/format"
)

// ExitCode maps an error to the documented CLI exit code (spec §2.4).
// Mirrors gh / Stripe convention:
//   - 0 success
//   - 1 generic / unknown
//   - 2 flag / argument problem
//   - 3 auth.*
//   - 4 resource.not_found
//   - 5 input.*
//   - 6 server.rate_limited
//   - 7 server.* (other) / network.*
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
	if matchPrefix(err, "server.") || matchPrefix(err, "network.") {
		return 7
	}
	return 1
}

// PrintError writes err to w in human-readable form. The envelope-aware
// JSON formatter is in `internal/format`; this helper is the human path used
// when no command produced its own output.
//
// Typed *Error values surface their Hint as a second line so users see the
// actionable next-step (matches envelope.error.hint visibility in --json).
// Falls through to defaultHint when caller didn't set one.
func PrintError(w io.Writer, err error) {
	if err == nil || errors.Is(err, SilentError) {
		return
	}
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
	}
}

// PrintErrorEnvelope writes err as a JSON envelope on w. Used in agent mode /
// --json / --format=json output so failures stay machine-parseable.
func PrintErrorEnvelope(w io.Writer, err error) {
	if err == nil || errors.Is(err, SilentError) {
		return
	}
	_ = format.WriteEnvelope(w, format.Failure(ToErrorBody(err)))
}

// ToErrorBody projects err into the canonical envelope ErrorBody. Exposed so
// other emit paths (planned: MCP) reuse the same projection rather than
// reimplementing the typed-error → wire-shape mapping.
func ToErrorBody(err error) *format.ErrorBody {
	if err == nil {
		return nil
	}
	body := &format.ErrorBody{Message: err.Error()}
	var typed *Error
	if errors.As(err, &typed) {
		body.Code = string(typed.Code)
		body.Message = typed.Message
		body.Hint = typed.Hint
		if body.Hint == "" {
			body.Hint = defaultHint(typed.Code)
		}
		body.Retryable = typed.Retryable
		// Surface the wrapped cause so agents see the actual server / SDK
		// error string, not just the wrap message ("hybrid search"). Stripe's
		// envelope does the same — the human's printed line and the JSON
		// envelope both end with the underlying problem.
		if typed.Cause != nil {
			body.Message = typed.Message + ": " + typed.Cause.Error()
		}
		return body
	}
	var fe *FlagError
	if errors.As(err, &fe) {
		body.Code = string(CodeInputInvalidArgument)
		return body
	}
	// Unclassified error; consumers see the message but no stable code.
	body.Code = string(CodeServerError)
	return body
}

// defaultHint returns a canonical actionable hint for known error codes when
// the call site didn't set one. Spec §1.4 zero-config matrix mandates
// `auth.unauthenticated` envelopes carry "run weknora auth login" — this
// fallback covers the broad surface (whoami / auth status / kb list / kb get
// / search) without per-command hint plumbing.
//
// Empty string for codes without a stable canonical hint.
func defaultHint(code ErrorCode) string {
	switch code {
	case CodeAuthUnauthenticated, CodeAuthBadCredential:
		return "run `weknora auth login`"
	case CodeAuthTokenExpired:
		return "your session expired; run `weknora auth login` to re-authenticate"
	case CodeAuthForbidden:
		return "active context lacks permission for this resource"
	case CodeAuthCrossTenantBlocked, CodeAuthTenantMismatch:
		return "verify tenant context with `weknora whoami`"
	case CodeNetworkError:
		return "check base URL reachability with `weknora doctor`"
	case CodeServerIncompatibleVersion:
		return "run `weknora doctor` to see version skew details"
	case CodeServerRateLimited:
		return "rate-limited; retry after a few seconds"
	case CodeServerTimeout:
		return "request timed out; retry, or run `weknora doctor` to check connectivity"
	case CodeResourceNotFound:
		return "verify the resource ID; list available with `weknora kb list`"
	case CodeInputInvalidArgument, CodeInputMissingFlag:
		return "see `weknora <command> --help` for valid usage"
	case CodeLocalKeychainDenied:
		return "verify keyring access; falls back to file storage"
	case CodeLocalConfigCorrupt:
		return "remove ~/.config/weknora/config.yaml and re-run `weknora auth login`"
	case CodeLocalFileIO:
		return "check file permissions under $XDG_CONFIG_HOME/weknora/"
	}
	return ""
}
