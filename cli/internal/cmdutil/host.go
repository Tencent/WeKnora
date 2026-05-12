package cmdutil

import (
	"fmt"
	"net/url"
	"strings"
)

// NormalizeHost validates and canonicalizes a `--host` value. Trailing
// slashes are trimmed. Every failure path returns CodeInputInvalidArgument —
// a present-but-empty flag is treated as a bad value, not as a missing flag
// (cobra's required-flag layer is what catches the absent case).
//
// Rules:
//   - non-empty
//   - scheme is http or https
//   - URL parses
//   - u.Host is non-empty (rejects "http://" which url.Parse accepts)
func NormalizeHost(host string) (string, error) {
	host = strings.TrimRight(strings.TrimSpace(host), "/")
	if host == "" {
		return "", NewError(CodeInputInvalidArgument, "--host must not be empty")
	}
	u, err := url.Parse(host)
	if err != nil {
		return "", NewError(CodeInputInvalidArgument, fmt.Sprintf("--host %q is not a valid URL: %v", host, err))
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", &Error{
			Code:    CodeInputInvalidArgument,
			Message: fmt.Sprintf("--host scheme must be http or https, got %q", u.Scheme),
			Hint:    "example: https://kb.example.com",
		}
	}
	if u.Host == "" {
		return "", NewError(CodeInputInvalidArgument, fmt.Sprintf("--host %q is missing the host portion", host))
	}
	return host, nil
}
