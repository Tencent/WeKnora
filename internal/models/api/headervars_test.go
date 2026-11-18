package api

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// contextWithUser builds a context that looks like an authenticated web
// request: a real user row plus its workspace and session.
func contextWithUser() context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, types.UserContextKey, &types.User{
		ID:       "u-1",
		Username: "alice",
		Email:    "alice@example.com",
	})
	ctx = context.WithValue(ctx, types.UserIDContextKey, "u-1")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{ID: 42, Name: "acme"})
	ctx = types.WithSessionID(ctx, "session-1")
	ctx = context.WithValue(ctx, types.RequestIDContextKey, "req-1")
	return ctx
}

func TestHeaderVariables_CollectsIdentity(t *testing.T) {
	vars := HeaderVariables(contextWithUser())

	require.Equal(t, "u-1", vars["user_id"])
	require.Equal(t, "alice", vars["user_name"])
	require.Equal(t, "alice@example.com", vars["user_email"])
	require.Equal(t, "42", vars["tenant_id"])
	require.Equal(t, "acme", vars["tenant_name"])
	require.Equal(t, "session-1", vars["session_id"])
	require.Equal(t, "req-1", vars["request_id"])
}

func TestHeaderVariables_NoUserRowMeansNoDisplayIdentity(t *testing.T) {
	// An API key authenticates as a principal, not as a WeKnora account.
	// There is no user row, so there is no user_name to send — the variable
	// must be absent rather than silently empty, so the caller can tell
	// "no such user" from "user named nothing".
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "u-9")

	vars := HeaderVariables(ctx)

	require.Equal(t, "u-9", vars["user_id"])
	require.NotContains(t, vars, "user_name")
	require.NotContains(t, vars, "user_email")
}

func TestHeaderVariables_NilContext(t *testing.T) {
	require.Nil(t, HeaderVariables(nil))
}

// The whole point of the feature: one configured model, different callers,
// different identity headers on the wire.
func TestEndpointExpandsDynamicHeaderPerRequest(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	ep := Endpoint{
		Headers: map[string]string{
			"X-User-Name": "${user_name}",
			"X-Tenant":    "${tenant_name}",
			"X-Static":    "unchanged",
		},
	}

	req, _, err := ep.NewRequest(contextWithUser(), "http://127.0.0.1/v1/chat/completions", map[string]any{}, false)
	require.NoError(t, err)
	require.Equal(t, "alice", req.Header.Get("X-User-Name"))
	require.Equal(t, "acme", req.Header.Get("X-Tenant"))
	require.Equal(t, "unchanged", req.Header.Get("X-Static"))

	// The configuration itself must not be rewritten: the expansion belongs to
	// the request, not to the shared model settings.
	require.Equal(t, "${user_name}", ep.Headers["X-User-Name"])
}

func TestEndpointUnavailableVariableExpandsToEmpty(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	ep := Endpoint{
		Headers: map[string]string{"X-User-Name": "${user_name}"},
	}

	// No user in this context: the header is still sent, but with an empty
	// value rather than the literal placeholder.
	req, _, err := ep.NewRequest(context.Background(), "http://127.0.0.1/v1/chat/completions", map[string]any{}, false)
	require.NoError(t, err)
	require.Equal(t, "", req.Header.Get("X-User-Name"))
	require.NotContains(t, req.Header.Get("X-User-Name"), "${")
}
