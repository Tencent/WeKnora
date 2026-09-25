package wecom

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	ws "github.com/gorilla/websocket"
)

// newTestConn dials a throwaway WebSocket server and returns the client side.
// The server drains frames until the client goes away.
func newTestConn(t *testing.T) *ws.Conn {
	t.Helper()

	upgrader := ws.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverConn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = serverConn.Close() }()
		for {
			if _, _, err := serverConn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	conn, _, err := ws.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial test websocket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// connUsable reports whether conn still accepts writes.
func connUsable(t *testing.T, conn *ws.Conn) bool {
	t.Helper()
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return conn.WriteMessage(ws.TextMessage, []byte("probe")) == nil
}

func TestCloseConnClosesAndClearsActiveConnection(t *testing.T) {
	conn := newTestConn(t)
	c := &LongConnClient{conn: conn}

	c.closeConn()

	if c.conn != nil {
		t.Error("closeConn left c.conn set")
	}
	if connUsable(t, conn) {
		t.Error("closeConn did not close the connection")
	}
}

func TestStopClosesActiveConnection(t *testing.T) {
	conn := newTestConn(t)
	c := &LongConnClient{conn: conn}

	c.Stop()

	if !c.closed.Load() {
		t.Error("Stop did not mark the client closed")
	}
	if c.conn != nil {
		t.Error("Stop left c.conn set")
	}
	if connUsable(t, conn) {
		t.Error("Stop did not close the connection")
	}
}

func TestCloseConnIfClosesActiveConnection(t *testing.T) {
	conn := newTestConn(t)
	c := &LongConnClient{conn: conn}

	c.closeConnIf(conn)

	if c.conn != nil {
		t.Error("closeConnIf left c.conn set")
	}
	if connUsable(t, conn) {
		t.Error("closeConnIf did not close the active connection")
	}
}

// A heartbeat loop or detached callback handler from a previous connection
// generation must not tear down the connection opened by a later reconnect.
func TestCloseConnIfIgnoresSupersededConnection(t *testing.T) {
	stale := newTestConn(t)
	active := newTestConn(t)
	c := &LongConnClient{conn: active}

	c.closeConnIf(stale)

	if c.conn != active {
		t.Fatal("closeConnIf cleared the active connection on behalf of a stale caller")
	}
	if !connUsable(t, active) {
		t.Fatal("closeConnIf closed the active connection on behalf of a stale caller")
	}
}

func TestCloseConnIfIgnoresNilConnection(t *testing.T) {
	conn := newTestConn(t)
	c := &LongConnClient{conn: conn}

	c.closeConnIf(nil)

	if c.conn != conn {
		t.Error("closeConnIf(nil) cleared the active connection")
	}
	if !connUsable(t, conn) {
		t.Error("closeConnIf(nil) closed the active connection")
	}
}

// Exercises the close and write paths together so the race detector can catch
// any regression that reintroduces unsynchronized access to c.conn.
func TestCloseConnRacesWithWriteJSON(t *testing.T) {
	for i := 0; i < 20; i++ {
		conn := newTestConn(t)
		c := &LongConnClient{conn: conn}

		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			_ = c.writeJSON(wsFrame{Cmd: cmdPing})
		}()
		go func() {
			defer wg.Done()
			c.closeConnIf(conn)
		}()
		go func() {
			defer wg.Done()
			c.closeConn()
		}()
		wg.Wait()

		if c.conn != nil {
			t.Fatal("connection pointer still set after concurrent closes")
		}
	}
}
