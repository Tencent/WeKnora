package cmdutil

import (
	"fmt"
	"strings"
)

// ValidateProfileName enforces the profile-name allowlist: letters, digits,
// dash, underscore, dot. The `.` exception lets email / DNS-like names
// through; `.` / `..` and path separators are structurally rejected so a
// hand-edited config.yaml can't claim a profile whose name walks out of the
// keyring namespace.
//
// Crucially this also rejects shell metacharacters (space, `;`, `&`, `|`,
// `$`, quotes, backticks, etc.), so the profile name remains safe to
// interpolate into envelope.error.retry_command — an agent that exec's
// `sh -c <retry_command>` cannot be tricked via a maliciously-named profile.
func ValidateProfileName(name string) error {
	if name == "" {
		return &Error{
			Code:    CodeInputInvalidArgument,
			Message: "profile name must not be empty",
		}
	}
	if name == "." || name == ".." || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return &Error{
			Code:    CodeInputInvalidArgument,
			Message: fmt.Sprintf("profile name %q is reserved or path-like", name),
			Hint:    "use letters, digits, dashes, underscores, or dots",
		}
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-' || r == '_' || r == '.':
			continue
		default:
			return &Error{
				Code:    CodeInputInvalidArgument,
				Message: fmt.Sprintf("profile name %q contains invalid character %q", name, r),
				Hint:    "use letters, digits, dashes, underscores, or dots",
			}
		}
	}
	return nil
}
