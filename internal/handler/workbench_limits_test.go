package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestWorkbenchSocketAuthTimeoutAndSemaphore(t *testing.T) {
	f := newWorkbenchHandlerFixture(t)
	f.h.authTimeout = 100 * time.Millisecond
	f.h.unauthenticated = make(chan struct{}, 1)
	first := f.socket(t)
	_, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.server.URL, "http")+"/api/v1/sandbox-terminal", http.Header{"Origin": []string{"http://127.0.0.1:15173"}})
	require.Error(t, err)
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	_ = response.Body.Close()
	require.NoError(t, first.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err = first.ReadMessage()
	require.Error(t, err)
	require.Eventually(t, func() bool { return len(f.h.unauthenticated) == 0 }, time.Second, 10*time.Millisecond)
}

func TestWorkbenchSocketRejectsQueryCredential(t *testing.T) {
	f := newWorkbenchHandlerFixture(t)
	_, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.server.URL, "http")+"/api/v1/sandbox-terminal?ticket=not-accepted", http.Header{"Origin": []string{"http://127.0.0.1:15173"}})
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, response.StatusCode)
	_ = response.Body.Close()
}

func TestWorkbenchSocketRechecksEveryFrameAndLeaseLoss(t *testing.T) {
	for _, revoke := range []string{"membership", "lease"} {
		t.Run(revoke, func(t *testing.T) {
			f := newWorkbenchHandlerFixture(t)
			// Disable the fast test timer so only the frame authorizer can reject.
			f.h.recheckInterval = time.Hour
			conn := f.socket(t)
			authenticateWorkbenchSocket(t, conn, f.ticket(t))
			if revoke == "membership" {
				require.NoError(t, f.db.Exec("UPDATE tenant_members SET status='suspended'").Error)
			} else {
				f.redis.FlushAll()
			}
			require.NoError(t, conn.WriteJSON(map[string]any{"type": "command", "command": "echo prohibited"}))
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			for {
				_, _, err := conn.ReadMessage()
				if err != nil {
					break
				}
			}
			require.Zero(t, f.manager.opened.Load())
		})
	}
}

func TestWorkbenchBackpressureCancelsAndOutputLimitClosesProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &workbenchConsole{ctx: ctx, cancel: cancel, outgoing: make(chan workbenchOutput, 2)}
	require.True(t, w.enqueue(websocket.BinaryMessage, []byte("a")))
	require.True(t, w.enqueue(websocket.BinaryMessage, []byte("b")))
	require.False(t, w.enqueue(websocket.BinaryMessage, []byte("c")))
	require.Error(t, ctx.Err())
	require.Len(t, w.outgoing, 2)

	f := newWorkbenchHandlerFixture(t)
	ctx, cancel = context.WithCancel(f.ctx)
	defer cancel()
	command := &workbenchCommand{cancel: cancel}
	w = &workbenchConsole{handler: f.h, ctx: ctx, cancel: cancel, identity: service.WorkbenchIdentity{TenantID: 7, UserID: "alice", SessionID: "session"}, outgoing: make(chan workbenchOutput, 32), active: command}
	w.outputBytes.Store(service.WorkbenchMaxOutputBytes)
	w.workers.Add(1)
	w.execute(ctx, command, sandbox.TerminalRequest{Command: "echo excess", Cols: 80, Rows: 24})
	terminal := <-f.manager.terminals
	require.True(t, terminal.closed.Load())
	require.Error(t, ctx.Err())
	require.Equal(t, 2, f.audit.count())
}
