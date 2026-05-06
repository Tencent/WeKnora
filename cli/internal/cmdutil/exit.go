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

// PrintError writes err to w as one human-readable line. The envelope-aware
// JSON formatter is in `internal/format`; this helper is the human path used
// when no command produced its own output.
func PrintError(w io.Writer, err error) {
	if err == nil || errors.Is(err, SilentError) {
		return
	}
	fmt.Fprintln(w, err.Error())
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
