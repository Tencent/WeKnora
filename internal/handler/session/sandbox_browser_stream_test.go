package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestBrowserStreamRejectsEndpointAndUnscopedInput(t *testing.T) {
	for _, raw := range []string{
		`{"type":"input_mouse","eventType":"mousePressed"}`,
		`{"type":"command","id":1,"command":{"action":"eval"}}`,
		`{"type":"command","id":0,"command":{"action":"frame"}}`,
		`{"type":"command","id":1,"command":{"action":"pointer","phase":"move","x":99999}}`,
		`{"type":"command","id":1,"command":{"action":"hover","x":-1}}`,
		`{"type":"command","id":1,"command":{"action":"hover","y":801}}`,
	} {
		_, err := validateBrowserStreamInput([]byte(raw), strings.Repeat("a", 32))
		require.Error(t, err, raw)
	}
	raw, err := validateBrowserStreamInput(
		[]byte(`{"type":"command","id":1,"command":{"action":"acquire","token":"other"}}`),
		"connection-token",
	)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"token":"connection-token"`)
	require.Contains(t, string(raw), `"stream":true`)
	raw, err = validateBrowserStreamInput(
		[]byte(`{"type":"command","id":2,"command":{"action":"hover","x":10,"y":20,"token":"other"}}`),
		"connection-token",
	)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"token":"connection-token"`)
}

func TestBrowserStreamRejectsWrongSessionBeforeSandbox(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ticket, err := service.IssueSandboxBrowserTicket("user", 1, "owned", "token", time.Minute)
	require.NoError(t, err)
	h := &Handler{}
	r := gin.New()
	r.GET("/sessions/:id/stream", h.SandboxBrowserStream)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sessions/other/stream?ticket="+ticket, nil))
	require.Equal(t, http.StatusForbidden, w.Code)
}

type browserStreamFake struct {
	*fakeTerminalSession
	writes chan []byte
}

func (s *browserStreamFake) Write(ctx context.Context, p []byte) error {
	select {
	case s.writes <- append([]byte(nil), p...):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestBrowserStreamForwardsRendererAckWithoutGeneratingOne(t *testing.T) {
	stream := &browserStreamFake{fakeTerminalSession: newFakeTerminalSession(), writes: make(chan []byte, 4)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := terminalUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		bridgeBrowserStream(ctx, cancel, conn, stream, strings.Repeat("a", 32), func(context.Context) error {
			return nil
		})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	go func() {
		stream.out <- sandbox.RemoteTerminalEvent{Data: []byte("shell prelude\nWK_BROWSER_STR")}
		stream.out <- sandbox.RemoteTerminalEvent{
			Data: []byte("EAM {\"type\":\"frame\",\"seq\":7,\"data\":\"jpeg\"}\n"),
		}
	}()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	_, payload, err := conn.ReadMessage()
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"frame","seq":7,"data":"jpeg"}`, string(payload))
	select {
	case <-stream.writes:
		t.Fatal("proxy acknowledged an undrawn frame")
	default:
	}
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "ack", "seq": 7}))
	select {
	case ack := <-stream.writes:
		require.JSONEq(t, `{"type":"ack","seq":7}`, string(ack))
	case <-time.After(time.Second):
		t.Fatal("renderer ack not forwarded")
	}
}

func TestBrowserStreamClosesWhenAccessIsRevoked(t *testing.T) {
	previous := browserAuthRecheckInterval
	browserAuthRecheckInterval = 20 * time.Millisecond
	defer func() { browserAuthRecheckInterval = previous }()
	stream := newFakeTerminalSession()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := terminalUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		defer func() { _ = stream.Close() }()
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		bridgeBrowserStream(ctx, cancel, conn, stream, strings.Repeat("a", 32), func(context.Context) error {
			return service.ErrTerminalAuthDenied
		})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err = conn.ReadMessage()
	require.Error(t, err)
	require.Eventually(t, func() bool { return stream.closes.Load() > 0 }, time.Second, time.Millisecond)
}
