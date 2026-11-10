package sandbox

import "strings"

// ShellQuote renders s as one literal word for /bin/sh.
//
// Single quotes are the only construct that keeps every metacharacter inert:
// inside double quotes `$`, backtick and `\` still expand, so a path chosen by
// an uploaded archive would execute as a command. The embedded-quote escape is
// the usual '\” dance. Non-ASCII bytes are passed through unchanged, so a CJK
// file name still names the same file inside the sandbox.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	// Only bare tokens (alnum, dash, underscore, slash, dot, comma, colon,
	// equals, plus) can be passed unquoted; everything else gets single
	// quotes.
	if isShellSafe(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func isShellSafe(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '/' || r == '.' || r == ',' ||
			r == ':' || r == '=' || r == '+':
		default:
			return false
		}
	}
	return true
}
