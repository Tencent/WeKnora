package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// TestRemoteChat_ExpandsDynamicCustomHeaders walks the whole production path
// for a custom header that carries a placeholder: types.Model → ConfigFromModel
// → NewChat → the wire. The unit test in internal/models/api covers the
// expansion itself; this one proves the model configuration actually reaches
// the request, and that the expansion happens per call rather than once at
// configuration time.
func TestRemoteChat_ExpandsDynamicCustomHeaders(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")

	var captured http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant",` +
			`"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	m := &types.Model{
		ID:     "model-1",
		Name:   "gpt-4o-mini",
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			BaseURL:  server.URL + "/v1",
			APIKey:   "sk-test",
			Provider: "openai",
			CustomHeaders: map[string]string{
				"X-User-Name": "${user_name}",
				"X-Tenant":    "${tenant_name}",
				"X-Static":    "unchanged",
			},
		},
	}

	c, err := NewChat(ConfigFromModel(m, "", ""), nil)
	require.NoError(t, err)

	ctx := context.Background()
	ctx = context.WithValue(ctx, types.UserContextKey, &types.User{ID: "u-1", Username: "alice"})
	ctx = context.WithValue(ctx, types.UserIDContextKey, "u-1")
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{ID: 42, Name: "acme"})

	_, err = c.Chat(ctx, []Message{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)

	require.Equal(t, "alice", captured.Get("X-User-Name"))
	require.Equal(t, "acme", captured.Get("X-Tenant"))
	require.Equal(t, "unchanged", captured.Get("X-Static"))

	// The stored configuration keeps the placeholder: one model serves many
	// callers, so the expansion must not be baked into the config.
	require.Equal(t, "${user_name}", m.Parameters.CustomHeaders["X-User-Name"])
}

// TestRemoteChat_DynamicHeaderFollowsTheCaller proves the header is not
// pinned to whoever configured the model: two callers, two identities.
func TestRemoteChat_DynamicHeaderFollowsTheCaller(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")

	var captured http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant",` +
			`"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	m := &types.Model{
		ID:     "model-1",
		Name:   "gpt-4o-mini",
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			BaseURL:       server.URL + "/v1",
			APIKey:        "sk-test",
			Provider:      "openai",
			CustomHeaders: map[string]string{"X-User-Name": "${user_name}"},
		},
	}

	c, err := NewChat(ConfigFromModel(m, "", ""), nil)
	require.NoError(t, err)

	for _, username := range []string{"alice", "bob"} {
		ctx := context.WithValue(context.Background(), types.UserContextKey,
			&types.User{ID: "u-" + username, Username: username})
		_, err = c.Chat(ctx, []Message{{Role: "user", Content: "hi"}}, nil)
		require.NoError(t, err)
		require.Equal(t, username, captured.Get("X-User-Name"))
	}
}
