package cmdutil

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// rfc3339Example is the canonical hint shown when a time-flag value isn't RFC3339.
const rfc3339Example = "2006-01-02T15:04:05Z"

// ParseTimeFlag parses an RFC3339 time-flag value. An empty value is treated as
// unset and returns (nil, nil) — callers use nil to mean "no bound". A
// malformed value returns a CodeInputInvalidArgument error naming the label;
// on success it returns the parsed time. label is used verbatim in the message
// (e.g. "--start-time" for a CLI flag, "start_time" for an MCP tool field).
//
// One place owns the RFC3339 time-flag shape — the parse, error code, and
// wording consistency sibling of ValidateEnum.
func ParseTimeFlag(label, value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, &Error{
			Code: CodeInputInvalidArgument,
			Message: fmt.Sprintf("%s must be RFC3339 (e.g. %s), got %q",
				label, rfc3339Example, value),
		}
	}
	return &t, nil
}

// ValidateEnum checks a closed-set flag value, case-insensitively. An empty
// value is treated as unset and returns "" with no error (callers use the empty
// case to mean "no filter" / "server default"). A non-empty value that matches
// no allowed entry returns CodeInputInvalidArgument naming the flag and the
// valid set; on a match it returns the canonical spelling from allowed (so the
// server, which may be case-sensitive, receives a normalized value).
//
// This is the one place the "validate a flag against a closed enum" shape
// lives — keeping the membership test, error code, and message wording
// consistent across every such flag instead of hand-rolling each.
func ValidateEnum(flagName, value string, allowed []string) (string, error) {
	if value == "" {
		return "", nil
	}
	for _, a := range allowed {
		if strings.EqualFold(value, a) {
			return a, nil
		}
	}
	return "", &Error{
		Code: CodeInputInvalidArgument,
		Message: fmt.Sprintf("--%s must be one of: %s - got %q",
			flagName, strings.Join(allowed, " | "), value),
	}
}

// EnumStrings converts a slice of a ~string enum type (e.g. the SDK's
// AllModelTypes / AllMessageSearchModes enumerators) to plain strings for
// ValidateEnum and flag-help wording. Sourcing the closed set from the SDK
// this way keeps the CLI from drifting from the server vocabulary.
func EnumStrings[T ~string](xs []T) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = string(x)
	}
	return out
}

// OpenInput returns a reader for path. If path == "-", returns stdin
// (iostreams.IO.In). Otherwise opens the file. Caller is responsible
// for closing if needed — for typical "-input <file>"/"--input -" CLI
// patterns the file is fully read before the command exits and OS
// reclaims the FD, so closing is cosmetic.
func OpenInput(path string) (io.Reader, error) {
	if path == "-" {
		return iostreams.IO.In, nil
	}
	return os.Open(path)
}
