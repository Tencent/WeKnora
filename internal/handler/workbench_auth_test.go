package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/stretchr/testify/require"
)

func TestWorkbenchRevokedMintingTokenRejectsSocketTicket(t *testing.T) {
	f := newWorkbenchHandlerFixture(t)
	ticket := f.ticket(t)
	require.NoError(t, repository.NewAuthTokenRepository(f.db).RevokeTokensByUserID(f.ctx, "alice"))
	conn := f.socket(t)
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "auth", "ticket": ticket}))
	readWorkbenchEvent(t, conn, "error")
	require.Zero(t, f.manager.opened.Load())
}

func TestWorkbenchTokenRevocationClosesRunningCommand(t *testing.T) {
	f := newWorkbenchHandlerFixture(t)
	conn := f.socket(t)
	authenticateWorkbenchSocket(t, conn, f.ticket(t))
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "command", "command": "sleep 60"}))
	readWorkbenchEvent(t, conn, "started")
	_, _, err := conn.ReadMessage()
	require.NoError(t, err)
	terminal := <-f.manager.terminals
	require.NoError(t, repository.NewAuthTokenRepository(f.db).RevokeTokensByUserID(f.ctx, "alice"))
	require.Eventually(t, func() bool { return terminal.closed.Load() && f.audit.count() == 2 },
		2*time.Second, 10*time.Millisecond)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	require.EqualValues(t, 1, f.manager.opened.Load())
}

func TestWorkbenchTokenRevocationIsCheckedBeforeNextCommand(t *testing.T) {
	f := newWorkbenchHandlerFixture(t)
	f.h.recheckInterval = time.Hour
	conn := f.socket(t)
	authenticateWorkbenchSocket(t, conn, f.ticket(t))
	require.NoError(t, repository.NewAuthTokenRepository(f.db).RevokeTokensByUserID(f.ctx, "alice"))
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "command", "command": "echo denied"}))
	readWorkbenchEvent(t, conn, "error")
	require.Zero(t, f.manager.opened.Load())
	require.Zero(t, f.audit.count())
}

func TestWorkbenchTicketRejectsMissingBearerEvenWithWebContext(t *testing.T) {
	f := newWorkbenchHandlerFixture(t)
	for _, value := range []string{"", "Basic credentials", "Bearer", "Bearer unknown"} {
		req := httptest.NewRequest(http.MethodPost, "/sessions/session/sandbox/command-ticket", nil)
		req.Header.Set("Origin", "http://127.0.0.1:15173")
		req.Header.Set("Authorization", value)
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, req)
		require.Equal(t, http.StatusForbidden, w.Code)
	}
}
