package cmdutil

import (
	"fmt"
	"net/url"
	"strings"
)

// NormalizeHost validates and canonicalizes a `--host` value. Trailing
// slashes are trimmed. Every failure path returns CodeInputInvalidArgument -
// a present-but-empty flag is treated as a bad value, not as a missing flag
// (cobra's required-flag layer is what catches the absent case).
//
// Rules:
//   - non-empty
//   - scheme is http or https
//   - URL parses
//   - u.Host is non-empty (rejects "http://" which url.Parse accepts)
func NormalizeHost(host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", NewError(CodeInputInvalidArgument, "--host must not be empty")
	}
	// Validate the value as typed, then trim. Trimming first turns "https://"
	// into "https:", so the rejection quotes a string the user never passed.
	if err := ValidateHTTPURL("--host", host); err != nil {
		return "", err
	}
	return strings.TrimRight(host, "/"), nil
}

// ValidateHTTPURL applies the same http/https URL rules NormalizeHost uses,
// but parameterized by flagLabel so error messages name the caller's flag
// (e.g. "--host", "--from-url"). Returns CodeInputInvalidArgument on any
// failure. Empty `value` is not pre-checked here - callers that distinguish
// "missing" from "malformed" should test for empty before calling.
func ValidateHTTPURL(flagLabel, value string) error {
	u, err := url.Parse(value)
	if err != nil {
		return NewError(CodeInputInvalidArgument, fmt.Sprintf("%s %q is not a valid URL: %v", flagLabel, value, err))
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		// Report the value the user typed, not the parsed scheme: a scheme-less
		// value ("kb.example.com") parses to Scheme == "", and `got ""` reads as
		// "you passed nothing", which sends the caller hunting for a different
		// problem than the missing prefix.
		return &Error{
			Code:    CodeInputInvalidArgument,
			Message: fmt.Sprintf("%s %q must start with http:// or https://", flagLabel, value),
			Hint:    "example: https://example.com",
		}
	}
	if u.Host == "" {
		return NewError(CodeInputInvalidArgument, fmt.Sprintf("%s %q is missing the host portion", flagLabel, value))
	}
	return nil
}
