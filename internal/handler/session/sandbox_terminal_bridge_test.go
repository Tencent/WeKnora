package session

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/sandbox"
)

// fakeTerminalSession is a RemoteTerminalSession that never produces output,
// so a test controls exactly when the bridge ends.
type fakeTerminalSession struct {
	out    chan sandbox.RemoteTerminalEvent
	closes atomic.Int32
}

func newFakeTerminalSession() *fakeTerminalSession {
	return &fakeTerminalSession{out: make(chan sandbox.RemoteTerminalEvent)}
}

func (s *fakeTerminalSession) Output() <-chan sandbox.RemoteTerminalEvent { return s.out }
func (s *fakeTerminalSession) PID() uint32                                { return 4321 }
func (s *fakeTerminalSession) Write(context.Context, []byte) error        { return nil }
func (s *fakeTerminalSession) Resize(context.Context, uint32, uint32) error {
	return nil
}

func (s *fakeTerminalSession) Close() error {
	// The real adapters make Close idempotent; mirror that and record the
	// call so a test can assert the PTY was released.
	if s.closes.Add(1) == 1 {
		close(s.out)
	}
	return nil
}

// serveTerminalBridge stands up a real WebSocket endpoint driven by
// terminalBridge and dials it, so tests exercise the actual protocol rather
// than the bridge's internals.
func serveTerminalBridge(
	t *testing.T,
	pty *fakeTerminalSession,
	authCheck func(context.Context) error,
) *websocket.Conn {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := terminalUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		bridge := &terminalBridge{
			conn:     conn,
			ctx:      ctx,
			cancel:   cancel,
			session:  "sess-1",
			terminal: &service.SessionTerminal{Session: pty, Backend: "fake"},
			// Idle disconnect off: these tests are about the auth watcher.
			idleDisconnect: 0,
			authCheck:      authCheck,
		}
		bridge.run()
	}))
	t.Cleanup(server.Close)

	client, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http"), nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func readTerminalFrame(t *testing.T, conn *websocket.Conn) terminalControlFrame {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, payload, err := conn.ReadMessage()
	require.NoError(t, err)
	var frame terminalControlFrame
	require.NoError(t, json.Unmarshal(payload, &frame))
	return frame
}

func TestTerminalBridgeTearsDownWhenAuthIsRevoked(t *testing.T) {
	prev := terminalAuthRecheckInterval
	terminalAuthRecheckInterval = 20 * time.Millisecond
	t.Cleanup(func() { terminalAuthRecheckInterval = prev })

	pty := newFakeTerminalSession()
	client := serveTerminalBridge(t, pty, func(context.Context) error {
		return service.ErrTerminalAuthDenied
	})

	require.Equal(t, "ready", readTerminalFrame(t, client).Type)

	errFrame := readTerminalFrame(t, client)
	require.Equal(t, "error", errFrame.Type)
	require.Equal(t, terminalErrAuth, errFrame.Code)

	// The socket must actually close, and the PTY must be released with it.
	require.NoError(t, client.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, _, err := client.ReadMessage()
	require.Error(t, err, "server must close the socket after revoking auth")
	require.Eventually(t, func() bool {
		return pty.closes.Load() > 0
	}, 2*time.Second, 10*time.Millisecond, "PTY must be closed on auth teardown")
}

// The counterpart to the service-level fail-open tests: a lookup that merely
// failed must leave the terminal running so a database hiccup does not
// disconnect every open session at once.
func TestTerminalBridgeSurvivesTransientAuthLookupFailure(t *testing.T) {
	prev := terminalAuthRecheckInterval
	terminalAuthRecheckInterval = 10 * time.Millisecond
	t.Cleanup(func() { terminalAuthRecheckInterval = prev })

	var checks atomic.Int32
	pty := newFakeTerminalSession()
	client := serveTerminalBridge(t, pty, func(context.Context) error {
		checks.Add(1)
		return errors.New("db down")
	})

	require.Equal(t, "ready", readTerminalFrame(t, client).Type)

	require.Eventually(t, func() bool {
		return checks.Load() >= 3
	}, 2*time.Second, 10*time.Millisecond, "watcher must keep retrying")

	// Nothing further should have been sent, and the PTY is still live.
	require.NoError(t, client.SetReadDeadline(time.Now().Add(150*time.Millisecond)))
	_, _, err := client.ReadMessage()
	require.Error(t, err)
	netErr, ok := err.(interface{ Timeout() bool })
	require.True(t, ok && netErr.Timeout(),
		"expected a read timeout (terminal still open), got %v", err)
	require.Zero(t, pty.closes.Load())
}
