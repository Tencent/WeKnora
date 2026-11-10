package compat

import (
	"fmt"
	"strconv"
	"strings"
)

// Level is the client-server version compat level.
type Level int

const (
	OK        Level = iota // compatible
	SoftWarn               // server outdated; some new features may be unavailable
	HardError              // incompatible; CLI or server must be upgraded
)

func (l Level) String() string {
	switch l {
	case OK:
		return "ok"
	case SoftWarn:
		return "soft_warn"
	case HardError:
		return "hard_error"
	}
	return "unknown"
}

// Compat compares client/server versions. Cross-minor drift is allowed:
//
//	same major, client minor <= server minor -> OK
//	same major, client minor > server minor  -> SoftWarn (some new features may be unavailable)
//	different major                          -> HardError
//	unparseable string                       -> OK (fail-open, do not block)
//	"(unknown)" / ""                          -> OK (dev build / server field missing)
func Compat(serverVer, cliVer string) (Level, string) {
	sMaj, sMin, ok := parseSemver(serverVer)
	if !ok {
		return OK, ""
	}
	cMaj, cMin, ok := parseSemver(cliVer)
	if !ok {
		return OK, ""
	}
	if sMaj != cMaj {
		return HardError, fmt.Sprintf("incompatible: client %s vs server %s - upgrade required", cliVer, serverVer)
	}
	if cMin > sMin {
		return SoftWarn, fmt.Sprintf("server is older (server %s, client %s); some new features may be unavailable", serverVer, cliVer)
	}
	return OK, ""
}

// parseSemver extracts (major, minor) from "X.Y.Z" or "X.Y.Z-suffix".
// Accepts the leading "v" common in `git describe` output and Tencent/WeKnora
// tag conventions ("v0.1.0"), since both server `/system/info.version` and
// the CLI's own ldflags-injected build.Version may carry it.
// Returns ok=false when the string is unrecognizable (empty / "(unknown)" / non-numeric).
func parseSemver(s string) (major, minor int, ok bool) {
	if s == "" || s == "(unknown)" {
		return 0, 0, false
	}
	// Accept the "v" prefix; both `git describe` output and Tencent/WeKnora tags carry it.
	s = strings.TrimPrefix(s, "v")
	// Strip prerelease/build metadata.
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.SplitN(s, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	maj, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	min, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return maj, min, true
}
