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
	_, response, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(f.server.URL, "http")+"/api/v1/sandbox-terminal",
		http.Header{"Origin": []string{"http://127.0.0.1:15173"}},
	)
	require.Error(t, err)
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	_ = response.Body.Close()
	require.NoError(t, first.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err = first.ReadMessage()
	require.Error(t, err)
	require.Eventually(t, func() bool { return len(f.h.unauthenticated) == 0 }, time.Second, 10*time.Millisecond)
}

func TestWorkbenchShutdownClosesUnauthenticatedSocket(t *testing.T) {
	f := newWorkbenchHandlerFixture(t)
	f.h.authTimeout = time.Hour
	conn := f.socket(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, f.h.Shutdown(ctx))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err := conn.ReadMessage()
	require.Error(t, err)
	require.Eventually(t, func() bool { return len(f.h.unauthenticated) == 0 }, time.Second, 10*time.Millisecond)
}

func TestWorkbenchSocketRejectsQueryCredential(t *testing.T) {
	f := newWorkbenchHandlerFixture(t)
	_, response, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(f.server.URL, "http")+"/api/v1/sandbox-terminal?ticket=not-accepted",
		http.Header{"Origin": []string{"http://127.0.0.1:15173"}},
	)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, response.StatusCode)
	_ = response.Body.Close()
}

func TestWorkbenchSocketFramesOnlyRenewAndCommandReauthorizes(t *testing.T) {
	f := newWorkbenchHandlerFixture(t)
	f.h.recheckInterval = time.Hour
	conn := f.socket(t)
	authenticateWorkbenchSocket(t, conn, f.ticket(t))
	baseline := f.policy.calls.Load()

	f.policy.disabled.Store(true)
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "ping"}))
	readWorkbenchEvent(t, conn, "pong")
	require.Equal(t, baseline, f.policy.calls.Load(), "ordinary frames must not run full authorization")

	require.NoError(t, conn.WriteJSON(map[string]any{"type": "command", "command": "echo prohibited"}))
	require.Equal(t, "policy_disabled", readWorkbenchEvent(t, conn, "error")["code"])
	require.Equal(t, baseline+1, f.policy.calls.Load(), "OpenTerminal must reauthorize each command")
	require.Zero(t, f.manager.opened.Load())
}

func TestWorkbenchSocketFrameFailsClosedOnLeaseLoss(t *testing.T) {
	f := newWorkbenchHandlerFixture(t)
	f.h.recheckInterval = time.Hour
	conn := f.socket(t)
	authenticateWorkbenchSocket(t, conn, f.ticket(t))
	f.redis.FlushAll()
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "ping"}))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
	require.Zero(t, f.manager.opened.Load())
}

func TestWorkbenchInterruptCancelsCommandWhileStarting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	command := &workbenchCommand{cancel: cancel}
	w := &workbenchConsole{ctx: ctx, cancel: cancel, outgoing: make(chan workbenchOutput, 1), active: command}
	require.True(t, w.handle(workbenchClientFrame{Type: "interrupt"}))
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.True(t, command.interrupted.Load())
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
	w = &workbenchConsole{
		handler: f.h, ctx: ctx, cancel: cancel,
		identity: service.WorkbenchIdentity{TenantID: 7, UserID: "alice", SessionID: "session"},
		outgoing: make(chan workbenchOutput, 32), active: command,
	}
	w.outputBytes.Store(service.WorkbenchMaxOutputBytes)
	w.workers.Add(1)
	w.execute(ctx, command, sandbox.CommandTerminalRequest{Command: "echo excess", Cols: 80, Rows: 24})
	terminal := <-f.manager.terminals
	require.True(t, terminal.closed.Load())
	require.Error(t, ctx.Err())
	require.Equal(t, 2, f.audit.count())
}
