package cmdutil

import (
	"errors"
	"fmt"
	"io"
)

// ExitCode maps an error to the documented CLI exit code.
//   - 0  success
//   - 1  generic / unknown typed error - fallback for: resource.already_exists,
//        resource.locked, local.* (config_corrupt / keychain_denied / file_io /
//        context_not_found / kb_id_required / kb_not_found / projectlink_corrupt /
//        user_aborted / upload_file_not_found), mcp.*, server.session_create_failed,
//        sse.stream_aborted, and any code outside the named buckets below
//   - 2  flag / argument problem (cobra parse / unknown subcommand)
//   - 3  auth.*
//   - 4  resource.not_found
//   - 5  input.* (other than confirmation_required)
//   - 6  server.rate_limited
//   - 7  server.* (other than rate_limited/session_create_failed) / network.*
//   - 10 input.confirmation_required - high-risk write needs explicit -y
//        (see cli/README.md)
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
	if matchPrefix(err, "server.") || matchPrefix(err, "network.") {
		return 7
	}
	return 1
}

// PrintError writes err to w (typically stderr) as `code: message\nhint:
// ...`. Typed *Error values surface their Hint as a second line so users
// see the actionable next-step. Falls through to defaultHint when the
// caller didn't set one.
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
		return "active context lacks permission for this resource"
	case CodeAuthCrossTenantBlocked, CodeAuthTenantMismatch:
		return "verify tenant context with `weknora auth status`"
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
		return "the streaming answer was cut off mid-flight; retry, or pass --no-stream to buffer the full response"
	case CodeSessionCreateFailed:
		return "could not create a chat session; pass --session to reuse an existing session"
	}
	return ""
}
