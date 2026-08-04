package storageurl

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
)

// Mode selects how stored files are referenced in an API response.
type Mode string

const (
	// ModeHandle returns the internal `resource://<handle>` reference. Clients
	// must fetch the bytes through the authenticated `/files` proxy. This is the
	// default and the only mode that never depends on external reachability.
	ModeHandle Mode = "handle"
	// ModePublic returns a time-limited HTTP(S) URL the client can load
	// directly, so third-party apps can render images without a second
	// authenticated call. References that cannot be turned into an HTTP URL are
	// left as handles.
	ModePublic Mode = "public"
)

const (
	// QueryParam is the request query parameter that selects the mode per call.
	QueryParam = "resource_urls"
	// EnvVar sets the deployment-wide default mode for requests that omit
	// QueryParam. It sits alongside APP_EXTERNAL_URL, which is what actually
	// makes `resource://` handles resolvable to a public `/r/<token>` URL.
	EnvVar = "RESOURCE_URL_MODE"
)

// ParseMode validates a mode supplied by a client or by configuration. An empty
// value yields ModeHandle so an unset parameter or setting keeps the default.
func ParseMode(raw string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(raw))) {
	case "":
		return ModeHandle, nil
	case ModeHandle:
		return ModeHandle, nil
	case ModePublic:
		return ModePublic, nil
	default:
		return ModeHandle, fmt.Errorf(
			"invalid %s value %q: expected %q or %q", QueryParam, raw, ModeHandle, ModePublic)
	}
}

// badDefaultOnce keeps a misconfigured EnvVar to a single log line instead of one
// per request. The value itself is re-read every time so operators can roll a
// change out without a restart.
var badDefaultOnce sync.Once

// DefaultMode returns the deployment-wide default from EnvVar. An unset or
// unparseable value yields ModeHandle, so a typo degrades to the safe default
// rather than failing every request.
func DefaultMode(ctx context.Context) Mode {
	raw := strings.TrimSpace(os.Getenv(EnvVar))
	if raw == "" {
		return ModeHandle
	}
	mode, err := ParseMode(raw)
	if err != nil {
		badDefaultOnce.Do(func() {
			logger.Warnf(ctx, "ignoring %s: %v; falling back to %q", EnvVar, err, ModeHandle)
		})
		return ModeHandle
	}
	return mode
}

// ResolveMode combines the per-request query value with the deployment default.
// An explicit query value always wins; an invalid one is returned as an error so
// integrators find their typo instead of silently receiving handles.
func ResolveMode(ctx context.Context, queryValue string) (Mode, error) {
	if strings.TrimSpace(queryValue) != "" {
		return ParseMode(queryValue)
	}
	return DefaultMode(ctx), nil
}
