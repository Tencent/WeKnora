package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestWorkbenchAuditCursorKeepsOlderCommandsQueryable(t *testing.T) {
	f := newWorkbenchHandlerFixture(t)
	require.NoError(t, f.db.AutoMigrate(&types.AuditLog{}))
	f.h.service = service.NewWorkbenchService(service.WorkbenchServiceDeps{
		Authorization: repository.NewWorkbenchAuthorizationRepository(f.db),
		Sessions:      workbenchHandlerSessions{db: f.db},
		Audit:         repository.NewAuditLogRepository(f.db),
	})
	f.router.GET("/sessions/:id/sandbox/audit", f.h.Audit)
	for id := uint64(1); id <= 123; id++ {
		row := &types.AuditLog{
			ID: id, TenantID: 7, ActorUserID: "alice", ScopeType: "sandbox_workbench", ScopeID: "session",
			Action: "sandbox_workbench.command", TargetID: fmt.Sprintf("command-%d", id),
			Outcome: types.AuditOutcomeSuccess,
		}
		switch id {
		case 121:
			row.TenantID = 8
		case 122:
			row.ActorUserID = "bob"
		case 123:
			row.ScopeID = "other-session"
		}
		require.NoError(t, f.db.Create(row).Error)
	}
	get := func(query string) auditLogListResponse {
		t.Helper()
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sessions/session/sandbox/audit"+query, nil))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		var response auditLogListResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.True(t, response.Success)
		return response
	}
	page := get("?after_id=100&limit=2")
	require.Len(t, page.Data, 2)
	require.EqualValues(t, 99, page.Data[0].ID, "the cursor must not silently repeat the latest page")
	require.EqualValues(t, 98, page.NextCursor)
	seen := make(map[uint64]bool)
	var cursor uint64
	for pageNumber := 0; pageNumber < 3; pageNumber++ {
		page = get(fmt.Sprintf("?after_id=%d&limit=100", cursor))
		for _, row := range page.Data {
			require.False(t, seen[row.ID], "cursor pagination repeated an entry")
			seen[row.ID] = true
			require.EqualValues(t, 7, row.TenantID)
			require.Equal(t, "alice", row.ActorUserID)
			require.Equal(t, "session", row.ScopeID)
		}
		cursor = page.NextCursor
		if cursor == 0 {
			break
		}
	}
	require.Zero(t, cursor)
	require.Len(t, seen, 120)
	require.True(t, seen[1], "the earliest command must remain queryable")
	page = get("?after_id=100&actor=bob&scope_id=other-session")
	require.EqualValues(t, 99, page.Data[0].ID)
	require.Equal(t, "alice", page.Data[0].ActorUserID)
	for _, query := range []string{"?after_id=-1", "?after_id=abc", "?after_id=18446744073709551616", "?limit=0"} {
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sessions/session/sandbox/audit"+query, nil))
		require.Equal(t, http.StatusBadRequest, w.Code, query)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sessions/missing/sandbox/audit?after_id=100", nil))
	require.Equal(t, http.StatusNotFound, w.Code)
}
