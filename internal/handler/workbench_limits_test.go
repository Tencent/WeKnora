package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestWorkbenchMemoryLimitStatusSocketAndAudit(t *testing.T) {
	f := newWorkbenchHandlerFixture(t)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sessions/session/sandbox/workbench", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var status struct {
		Data service.WorkbenchStatus `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
	require.EqualValues(t, 512<<20, status.Data.Limits.MemoryBytes)
	require.Equal(t, "per_process_as_and_aggregate_rss_sampled", status.Data.Limits.MemoryEnforcement)

	conn := f.socket(t)
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "auth", "ticket": f.ticket(t)}))
	ready := readWorkbenchEvent(t, conn, "ready")
	limits := ready["limits"].(map[string]any)
	require.EqualValues(t, 512<<20, limits["memory_bytes"])
	require.Equal(t, status.Data.Limits.MemoryEnforcement, limits["memory_enforcement"])
	for _, exit := range []sandbox.CommandTerminalExit{
		{ExitCode: 200, Reason: "memory_limit"},
		{ExitCode: 137, Reason: "exited"},
	} {
		require.NoError(t, conn.WriteJSON(map[string]any{"type": "command", "command": "test command"}))
		started := readWorkbenchEvent(t, conn, "started")
		kind, _, err := conn.ReadMessage()
		require.NoError(t, err)
		require.Equal(t, websocket.BinaryMessage, kind)
		terminal := <-f.manager.terminals
		terminal.finish(exit.ExitCode, exit.Reason)
		event := readWorkbenchEvent(t, conn, "exit")
		require.EqualValues(t, exit.ExitCode, event["exit_code"])
		require.Equal(t, exit.Reason, event["reason"])
		f.audit.mu.Lock()
		row := f.audit.rows[len(f.audit.rows)-1]
		f.audit.mu.Unlock()
		require.Equal(t, types.AuditOutcomeFailed, row.Outcome)
		var details map[string]any
		require.NoError(t, json.Unmarshal(row.Details, &details))
		require.EqualValues(t, exit.ExitCode, details["exit_code"])
		require.Equal(t, exit.Reason, details["reason"])
		require.Equal(t, started["execution_id"], details["execution_id"])
	}
}

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

func TestWorkbenchCumulativeOutputLimitRetainsCauseAndCleanupOutcome(t *testing.T) {
	for _, cleanupFailed := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleaned", true: "cleanup-unknown"}[cleanupFailed], func(t *testing.T) {
			f := newWorkbenchHandlerFixture(t)
			if cleanupFailed {
				f.manager.closeErr = errors.New("private cleanup failure")
			}
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			command := &workbenchCommand{cancel: cancel}
			w := &workbenchConsole{
				handler: f.h, ctx: ctx, cancel: cancel,
				outgoing: make(chan workbenchOutput, 32), active: command,
				identity: service.WorkbenchIdentity{TenantID: 7, UserID: "alice", SessionID: "session"},
			}
			w.outputBytes.Store(service.WorkbenchMaxOutputBytes)
			w.workers.Add(1)
			w.execute(ctx, command, sandbox.CommandTerminalRequest{Command: "echo excess", Cols: 80, Rows: 24})
			terminal := <-f.manager.terminals
			require.True(t, terminal.closed.Load())
			require.Equal(t, 2, f.audit.count())
			var details struct {
				Reason   string `json:"reason"`
				ExitCode int    `json:"exit_code"`
			}
			require.NoError(t, json.Unmarshal(f.audit.rows[1].Details, &details))
			require.Equal(t, "output_limit", details.Reason)
			if cleanupFailed {
				require.Equal(t, -1, details.ExitCode)
				require.Equal(t, types.AuditOutcome("unknown"), f.audit.rows[1].Outcome)
			} else {
				require.Equal(t, 137, details.ExitCode)
				require.Equal(t, types.AuditOutcomeFailed, f.audit.rows[1].Outcome)
			}
			require.NotContains(t, string(f.audit.rows[1].Details), "private")
		})
	}
}
