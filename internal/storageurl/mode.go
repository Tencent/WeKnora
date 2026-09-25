package storageurl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
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

// ErrPublicModeForbidden is returned when the caller asked for ModePublic but
// its credentials must not receive anonymous, time-limited file URLs. Callers
// map it to 403 rather than 400: the request is well-formed, the scope is not.
var ErrPublicModeForbidden = errors.New(
	"resource_urls=public is not available for a knowledge-base-restricted API key")

// forcedHandleKey marks a request that must never receive public URLs, whatever
// the query parameter or the deployment default says.
type forcedHandleKey struct{}

// WithForcedHandleMode pins a request to ModeHandle. Anonymous surfaces use it:
// an embed visitor is authorized only by the channel's session handle, so it
// must keep fetching images through the channel-scoped `/embed/…/files` proxy
// instead of receiving a shareable, credential-free URL.
func WithForcedHandleMode(ctx context.Context) context.Context {
	return context.WithValue(ctx, forcedHandleKey{}, true)
}

// HandleModeForced reports whether ctx was pinned by WithForcedHandleMode.
func HandleModeForced(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	forced, _ := ctx.Value(forcedHandleKey{}).(bool)
	return forced
}

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

// badDefault keeps a misconfigured EnvVar to a single log line per distinct bad
// value instead of one per request, while still warning again if an operator
// rolls out a second typo. The value itself is re-read every time so a fix takes
// effect without a restart.
var badDefault struct {
	sync.Mutex
	warned map[string]bool
}

func warnBadDefaultOnce(ctx context.Context, raw string, err error) {
	badDefault.Lock()
	defer badDefault.Unlock()
	if badDefault.warned[raw] {
		return
	}
	if badDefault.warned == nil {
		badDefault.warned = make(map[string]bool)
	}
	badDefault.warned[raw] = true
	logger.Warnf(ctx, "ignoring %s: %v; falling back to %q", EnvVar, err, ModeHandle)
}

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
		warnBadDefaultOnce(ctx, raw, err)
		return ModeHandle
	}
	return mode
}

// ResolveMode combines the per-request query value with the deployment default,
// then applies the two limits that no caller may opt out of.
//
// An explicit query value wins over the deployment default; an invalid one is
// returned as an error so integrators find their typo instead of silently
// receiving handles.
//
// Limits:
//   - A ctx pinned by WithForcedHandleMode (anonymous embed traffic) is silently
//     downgraded rather than rejected, so an embed client that happens to send
//     the parameter keeps working — it just never gets a public URL.
//   - A knowledge-base-restricted API key is rejected with
//     ErrPublicModeForbidden. Such a key is already denied the `/files` proxy,
//     so handing it anonymous file URLs would widen its scope from "chunk text"
//     to "file bytes" through the back door.
func ResolveMode(ctx context.Context, queryValue string) (Mode, error) {
	if HandleModeForced(ctx) {
		return ModeHandle, nil
	}
	mode, err := requestedMode(ctx, queryValue)
	if err != nil {
		return ModeHandle, err
	}
	if mode == ModePublic {
		if scope, ok := types.TenantAPIKeyScopeFromContext(ctx); ok && scope.IsKnowledgeBaseRestricted() {
			return ModeHandle, ErrPublicModeForbidden
		}
	}
	return mode, nil
}

func requestedMode(ctx context.Context, queryValue string) (Mode, error) {
	if strings.TrimSpace(queryValue) != "" {
		return ParseMode(queryValue)
	}
	return DefaultMode(ctx), nil
}
