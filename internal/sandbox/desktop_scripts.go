// Package sandbox provides the shell fed to the desktop sandbox over Exec.
//
// These live as real .sh files rather than Go string literals so they keep
// shell tooling (shellcheck, `sh -n`, an editor that knows the language) and
// so a diff reads as shell instead of as an escaped blob. They are sent as
// the command body rather than invoked by path on purpose: unlike
// DesktopStartScript they are not baked into the image, so they also reach
// sandboxes booted from an older desktop template.
package sandbox

import (
	"regexp"
	"strings"

	_ "embed"
)

//go:embed desktopscripts/ensure.sh
var desktopEnsureScript string

//go:embed desktopscripts/reset-listeners.sh
var desktopResetListenersScript string

// DesktopEnsureUnsupportedExit is ensure.sh when DesktopStartScript is missing.
// POSIX sh also exits 2 on syntax errors; pair with the stderr marker.
const DesktopEnsureUnsupportedExit = 2

// DesktopEnsureUnsupportedMarker is written to stderr immediately before
// that exit 2. Go must see both or the failure is a broken wrapper, not
// a BYO image without a desktop.
const DesktopEnsureUnsupportedMarker = "WEKNORA_DESKTOP_UNSUPPORTED"

// DesktopNoFreeScreenMarker is wk-display claim when every seat is busy.
// ensure.sh forwards it on stderr. The caller must not reset the shared
// websockify in response: that listener carries every other session's desktop.
const DesktopNoFreeScreenMarker = "WEKNORA_NO_FREE_SCREEN"

var desktopReadyLine = regexp.MustCompile(`^READY ([A-Za-z0-9]{32})(?: ([0-9]{1,2}))?$`)

// DesktopEnsureCmd starts the desktop listeners (or takes the image script's
// fast path) and prints READY <secret> on stdout. Invoking DesktopStartScript
// by path hangs the Exec: envd waits for stdout/stderr to close, and the
// daemons keep those pipes. This wrapper points them at a log file.
func DesktopEnsureCmd() string { return desktopEnsureScript }

// DesktopResetListenersCmd drops a half-started desktop so the next
// DesktopEnsureCmd run rebuilds it instead of taking its fast path.
func DesktopResetListenersCmd() string { return desktopResetListenersScript }

// ParseDesktopEnsure requires stdout to be exactly one READY line:
// "READY <secret>" or "READY <secret> <token>". The token is the seat number
// (0 is the shared :0 screen); it is empty for images that predate seats.
// ensure.sh promises a single line; a last-non-empty-line parser would accept
// "garbage\nREADY <secret>" and dial, hiding the leak behind a 401/502.
// A single trailing newline (printf) is allowed; nothing else is.
func ParseDesktopEnsure(stdout string) (secret, token string, ok bool) {
	s := strings.ReplaceAll(stdout, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	m := desktopReadyLine.FindStringSubmatch(s)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// ParseDesktopEnsureSecret is ParseDesktopEnsure without the token, for
// callers that have not been switched over yet.
func ParseDesktopEnsureSecret(stdout string) (string, bool) {
	secret, _, ok := ParseDesktopEnsure(stdout)
	return secret, ok
}

// IsDesktopEnsureUnsupported is the BYO-image miss: exit 2 plus the
// marker. A bare 2 is a shell/wrapper failure and must not become
// ErrDesktopUnsupported.
func IsDesktopEnsureUnsupported(result *ExecuteResult) bool {
	if result == nil || result.Killed || result.ExitCode != DesktopEnsureUnsupportedExit {
		return false
	}
	return strings.Contains(result.Stderr, DesktopEnsureUnsupportedMarker)
}
