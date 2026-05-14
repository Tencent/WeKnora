package cmdutil

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"

	sdk "github.com/Tencent/WeKnora/client"
)

// AuthRetryTransport wraps a base http.RoundTripper with transparent
// recovery from a single 401 response. On a 401 from an authenticated
// (non-/auth/*) endpoint, it invokes refreshFn to obtain a new bearer
// token, replays the original request with the new Authorization header,
// and returns the replayed response.
//
// Behavior:
//   - 2xx / 4xx / 5xx other than 401: pass through unchanged.
//   - 401 on /api/v1/auth/login or /api/v1/auth/refresh: pass through
//     (otherwise a stale refresh token causes infinite recursion).
//   - 401 with no initial token configured (api-key contexts): pass through
//   - api-key credentials have no refresh semantic.
//   - 401 with non-replayable request body (req.GetBody == nil): pass
//     through. The SDK always uses bytes.Buffer bodies; this is a safety
//     net for hand-built requests.
//
// Concurrency: multiple goroutines may hit 401 simultaneously. The first
// one through acquires the mutex and refreshes; subsequent waiters observe
// the updated currentToken and replay without re-refreshing (singleflight).
//
// The transport intentionally shadows the SDK's bearerToken (set via
// WithBearerToken). After a refresh, the SDK is unaware of the new token -
// the transport's per-request override is the single source of truth for
// the Authorization header. Tokens persisted to the secrets store happen
// inside refreshFn (see cmdutil.RefreshAndPersist).
type AuthRetryTransport struct {
	base http.RoundTripper

	mu           sync.Mutex
	currentToken string
	refreshFn    func(context.Context) (string, error)
}

// NewAuthRetryTransport builds a retry transport. initialToken seeds the
// in-memory token state so the first request's Authorization header
// agrees with whatever the SDK was constructed with.
//
// Pass an empty initialToken to indicate "no bearer credential configured"
// (e.g. an api-key context). In that mode the transport never invokes
// refreshFn - a 401 is propagated as-is.
func NewAuthRetryTransport(base http.RoundTripper, initialToken string, refreshFn func(context.Context) (string, error)) *AuthRetryTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &AuthRetryTransport{
		base:         base,
		currentToken: initialToken,
		refreshFn:    refreshFn,
	}
}

// RoundTrip implements http.RoundTripper.
func (t *AuthRetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	token := t.currentToken
	t.mu.Unlock()
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	if token == "" {
		return resp, nil
	}
	if isAuthEndpoint(req.URL.Path) {
		return resp, nil
	}
	if req.Body != nil && req.GetBody == nil {
		return resp, nil
	}

	// Singleflight: re-check whether another goroutine already refreshed.
	t.mu.Lock()
	if t.currentToken != token {
		token = t.currentToken
		t.mu.Unlock()
	} else {
		newToken, refreshErr := t.refreshFn(req.Context())
		if refreshErr != nil {
			t.mu.Unlock()
			drainAndClose(resp)
			return nil, refreshErr
		}
		t.currentToken = newToken
		token = newToken
		t.mu.Unlock()
	}

	drainAndClose(resp)

	replay := req.Clone(req.Context())
	if req.GetBody != nil {
		body, gberr := req.GetBody()
		if gberr != nil {
			return nil, gberr
		}
		replay.Body = body
	}
	replay.Header.Set("Authorization", "Bearer "+token)
	return t.base.RoundTrip(replay)
}

// isAuthEndpoint reports whether path is one of the auth endpoints where
// 401 retry would either recurse (refresh) or duplicate work (login). The
// path constants are sourced from the SDK so the CLI and SDK can never
// drift on the canonical paths.
func isAuthEndpoint(path string) bool {
	return strings.HasPrefix(path, sdk.PathAuthLogin) ||
		strings.HasPrefix(path, sdk.PathAuthRefresh)
}

// drainAndClose consumes the response body so the underlying connection
// can be reused, then closes it. Errors are ignored because the response
// is being discarded anyway.
func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}
