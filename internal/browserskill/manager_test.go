package browserskill

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestPairInfersGatewayFromPageOrigin(t *testing.T) {
	m := NewManager(testStore(t))
	m.binary = "bsk"
	m.publicURL = ""
	require.True(t, m.Enabled())
	require.NoError(t, m.ValidateConfiguration())
	ctx := context.Background()
	scope := Scope{1, "alice"}
	for _, tt := range []struct{ origin, endpoint string }{
		{"https://weknora.example", "wss://weknora.example/api/v1/local-browser/extension"},
		{"https://weknora.example:8443", "wss://weknora.example:8443/api/v1/local-browser/extension"},
		{"http://localhost:8080", "ws://localhost:8080/api/v1/local-browser/extension"},
		{"http://127.0.0.1:5173", "ws://127.0.0.1:5173/api/v1/local-browser/extension"},
		{"http://[::1]:8080", "ws://[::1]:8080/api/v1/local-browser/extension"},
	} {
		t.Run(tt.origin, func(t *testing.T) {
			link, err := m.Pair(ctx, scope, tt.origin)
			require.NoError(t, err)
			parts := strings.Split(link, "#")
			require.Len(t, parts, 2)
			require.Equal(t, tt.endpoint, parts[0])
			require.NotEmpty(t, parts[1])
			// The automatically generated link still carries a redeemable pair token.
			require.NotEmpty(t, redeemTestPair(ctx, t, m, link))
		})
	}
	m.publicURL = "wss://gateway.example/custom/extension"
	link, err := m.Pair(ctx, scope, "http://intranet.example")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(link, m.publicURL+"#"))
	m.binary = ""
	require.False(t, m.Enabled(), "an empty binary path must still disable the integration")
}

func TestPairRejectsInvalidPageOrigin(t *testing.T) {
	m := &Manager{}
	for _, origin := range []string{
		"", "null", "https://", "https://example.com:invalid", "https://user:pass@example.com",
		"https://example.com/path", "https://example.com?token=x", "https://example.com?",
		"https://example.com#token", "wss://example.com", "file:///tmp/page.html",
		"http://public.example", "http://192.168.1.10:8080", "http://localhost.evil.example",
	} {
		t.Run(origin, func(t *testing.T) {
			_, err := m.pairingURL(origin)
			require.Error(t, err)
		})
	}
	m.publicURL = "http://invalid-gateway.example"
	_, err := m.pairingURL("https://weknora.example")
	require.Error(t, err, "an invalid explicit override must not silently fall back")
}

func TestPairingURL(t *testing.T) {
	for _, raw := range []string{
		"ws://public.example/path", "https://example.com", "wss://user:pass@example.com",
		"wss://example.com/?token=x", "wss://example.com/#secret",
	} {
		if _, err := pairingEndpoint(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, raw := range []string{
		"wss://example.com/api/v1/local-browser/extension", "ws://127.0.0.1:8080/api/v1/local-browser/extension",
	} {
		if _, err := pairingEndpoint(raw); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRejectUnauthenticatedExtension(t *testing.T) {
	m := NewManager(testStore(t))
	m.binary = "bsk"
	m.publicURL = "wss://example.com"
	for _, origin := range []string{"https://evil.example", "chrome-extension://" + strings.Repeat("a", 32)} {
		r := httptest.NewRequest("GET", "http://localhost/extension", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		if w.Code != 401 && w.Code != 403 {
			t.Fatalf("unexpected status %d", w.Code)
		}
	}
	if (*Manager)(nil).Status(Scope{1, "a"}, "chat").Enabled {
		t.Fatal("nil manager enabled")
	}
}

// Uses the released native daemon with a protocol fixture, not a real Chrome.
// Run with BROWSERSKILL_TEST_BINARY=/absolute/path/bsk.
func TestNativeDaemonRelay(t *testing.T) {
	binary := os.Getenv("BROWSERSKILL_TEST_BINARY")
	if binary == "" {
		t.Skip("set BROWSERSKILL_TEST_BINARY to run native protocol integration")
	}
	m := NewManager(testStore(t))
	m.binary = binary
	server := httptest.NewServer(m)
	defer server.Close()
	m.publicURL = ""
	t.Cleanup(m.Close)
	scope := Scope{1, "alice"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := m.Control(ctx, scope, "chat-a", "select"); err != nil {
		t.Fatal(err)
	}
	if status := m.Status(scope, "chat-a"); !status.Selected || status.SessionID != "" {
		t.Fatal("selection should be saved before pairing without starting a window")
	}
	link, err := m.Pair(ctx, scope, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	link = redeemTestPair(ctx, t, m, link)
	parts := strings.Split(link, "#")
	dialer := websocket.Dialer{Subprotocols: []string{AuthProtocol + parts[1]}}
	ws, _, err := dialer.Dial(
		parts[0],
		http.Header{"Origin": []string{"chrome-extension://" + strings.Repeat("a", 32)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.Close() }()
	err = ws.WriteJSON(
		map[string]any{
			"id":     "hs-test",
			"method": "system.handshake",
			"params": map[string]any{
				"client":                  "browser-skill-extension",
				"version":                 "0.2.1",
				"protocol_version":        "1.1",
				"instance_id":             "aabbccdd",
				"browser":                 map[string]any{"name": "chrome", "version": "125"},
				"label":                   "Protocol fixture",
				"min_compatible_protocol": "1.0",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var handshake map[string]any
	if err = ws.ReadJSON(&handshake); err != nil {
		t.Fatal(err)
	}
	if handshake["error"] != nil {
		t.Fatalf("handshake: %v", handshake)
	}
	calls := make(chan map[string]any, 20)
	var starts atomic.Int32
	go func() {
		for {
			var req struct {
				ID     string         `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if ws.ReadJSON(&req) != nil {
				return
			}
			if req.Method == "" {
				continue
			}
			result := map[string]any{}
			switch req.Method {
			case "tool.session_start":
				starts.Add(1)
				result = map[string]any{"session_id": req.Params["session_id"], "agent_window_id": 100}
			case "tool.session_stop":
				result = map[string]any{
					"session_id":       req.Params["session_id"],
					"returned_tab_ids": []int{},
					"return_failures":  []any{},
				}
			case "tool.snapshot":
				result = map[string]any{"text": "fixture snapshot", "ref_count": 0, "tab_id": 1}
			case "tool.navigate":
				calls <- req.Params
				if req.Params["url"] == "https://slow.example" {
					continue
				}
				result = map[string]any{"tab_id": 1, "url": req.Params["url"], "reached": "domcontentloaded"}
			case "cancel":
				_ = ws.WriteJSON(
					map[string]any{
						"id":    req.Params["rpc_id"],
						"error": map[string]any{"code": "cancelled", "message": "fixture cancelled"},
					},
				)
				result = map[string]any{"cancelled": true}
			case "system.ping":
				result = map[string]any{"pong": true}
			}
			if ws.WriteJSON(map[string]any{"id": req.ID, "result": result}) != nil {
				return
			}
		}
	}()
	if err = m.Control(ctx, scope, "chat-a", "select"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Call(ctx, scope, "chat-a", "snapshot", nil); err != nil {
		t.Fatal(err)
	}
	if err = m.Control(ctx, scope, "chat-b", "start"); err != nil {
		t.Fatal(err)
	}
	a, b := m.Status(scope, "chat-a"), m.Status(scope, "chat-b")
	if a.SessionID == "" || a.SessionID == b.SessionID {
		t.Fatalf("tasks not isolated: %+v %+v", a, b)
	}
	if _, err = m.Call(ctx, scope, "chat-a", "snapshot", nil); err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 2 {
		t.Fatal("repeated browser calls created another task window")
	}
	if err = m.Control(ctx, scope, "not-started", "select"); err != nil {
		t.Fatal(err)
	}
	if err = m.Control(ctx, scope, "not-started", "pause"); err != nil {
		t.Fatal(err)
	}
	if err = m.Control(ctx, scope, "not-started", "select"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Call(ctx, scope, "not-started", "snapshot", nil); err == nil || starts.Load() != 2 {
		t.Fatal("re-selecting a paused pending task allowed automatic startup")
	}
	if m.Status(Scope{1, "bob"}, "chat-a").Selected {
		t.Fatal("scope leaked")
	}
	if _, err = m.Call(ctx, Scope{2, "alice"}, "chat-a", "navigate", nil); err == nil {
		t.Fatal("cross tenant call accepted")
	}
	_, err = m.Call(
		ctx,
		scope,
		"chat-a",
		"navigate",
		map[string]any{"session_id": b.SessionID, "url": "https://example.com"},
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case params := <-calls:
		if params["session_id"] != a.SessionID {
			t.Fatal("caller replaced task ownership")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	pending := make(chan error, 1)
	go func() {
		_, err := m.Call(ctx, scope, "chat-a", "navigate", map[string]any{"url": "https://slow.example"})
		pending <- err
	}()
	select {
	case <-calls:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = m.Control(ctx, scope, "chat-a", "pause"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-pending:
		if err == nil {
			t.Fatal("in-flight mutation was not cancelled")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pause did not cancel pending RPC")
	}
	if _, err = m.Call(ctx, scope, "chat-a", "navigate", nil); err == nil {
		t.Fatal("paused mutation accepted")
	}
	if err = m.Control(ctx, scope, "chat-a", "resume"); err != nil {
		t.Fatal(err)
	}
	if err = m.Control(ctx, scope, "chat-a", "stop"); err != nil {
		t.Fatal(err)
	}
	if m.Status(scope, "chat-a").Selected {
		t.Fatal("ended task selected")
	}
	statusJSON, _ := json.Marshal(m.Status(scope, "chat-b"))
	if strings.Contains(string(statusJSON), parts[1]) {
		t.Fatal("credential leaked in status")
	}
	m.Forget(scope, []string{"chat-b"})
	if m.Status(scope, "chat-b").Selected {
		t.Fatal("deleted conversation remained selected")
	}
	if _, err = m.Call(ctx, scope, "chat-b", "navigate", nil); err == nil {
		t.Fatal("deleted conversation accepted browser action")
	}
	for m.Status(scope, "chat-b").SessionID != "" {
		select {
		case <-ctx.Done():
			t.Fatal("deleted conversation did not clean up its native session")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err = m.Control(ctx, scope, "chat-c", "start"); err != nil {
		t.Fatal(err)
	}
	_ = ws.Close()
	for m.Status(scope, "chat-c").Connected {
		select {
		case <-ctx.Done():
			t.Fatal("connection did not close")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err = m.Control(ctx, scope, "chat-c", "select"); err != nil {
		t.Fatal(err)
	}
	if !m.Status(scope, "chat-c").Paused {
		t.Fatal("source selection cleared the disconnected task interruption")
	}
	if _, err = m.Call(ctx, scope, "chat-c", "snapshot", nil); err == nil {
		t.Fatal("disconnected task resumed automatically")
	}
	if err = m.Control(ctx, scope, "chat-c", "stop"); err != nil {
		t.Fatalf("disconnected task could not be ended: %v", err)
	}
	if err := m.Revoke(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if _, _, err = dialer.Dial(parts[0], http.Header{
		"Origin": []string{"chrome-extension://" + strings.Repeat("a", 32)},
	}); err == nil {
		t.Fatal("revoked credential accepted")
	}
}
