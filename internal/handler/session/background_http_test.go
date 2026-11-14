package session

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type httpWorkHandler struct {
	interfaces.WorkHandler
	status map[string]interfaces.WorkResult
	cancel []string
	refs   []interfaces.SessionRef
	opts   []interfaces.StatusOptions
}

func (h *httpWorkHandler) Status(_ context.Context, ref interfaces.SessionRef,
	ids []string, opts interfaces.StatusOptions,
) ([]interfaces.WorkResult, error) {
	h.refs = append(h.refs, ref)
	h.opts = append(h.opts, opts)
	out := make([]interfaces.WorkResult, 0, len(ids))
	for _, id := range ids {
		if r, ok := h.status[id]; ok {
			out = append(out, r)
			continue
		}
		// A live id is not in the terminal map. Detail still has to resolve.
		out = append(out, interfaces.WorkResult{ID: id, State: interfaces.WorkStateRunning})
	}
	return out, nil
}

func (h *httpWorkHandler) Cancel(
	_ context.Context, ref interfaces.SessionRef, ids []string,
) ([]interfaces.WorkResult, error) {
	h.refs = append(h.refs, ref)
	h.cancel = append(h.cancel, ids...)
	out := make([]interfaces.WorkResult, 0, len(ids))
	for _, id := range ids {
		out = append(out, interfaces.WorkResult{ID: id, State: interfaces.WorkStateCancelled, Terminal: true})
	}
	return out, nil
}

func (s *stubSessionService) GetOwnedSession(ctx context.Context, id string) (*types.Session, error) {
	session, err := s.GetSession(ctx, id)
	if err != nil || session == nil {
		if err == nil {
			err = errors.New("session not found")
		}
		return nil, err
	}
	// Strict owner: a caller executing as another tenant must not mutate.
	if tenant, ok := types.TenantIDFromContext(ctx); ok && tenant != session.TenantID {
		return nil, errors.New("session not found")
	}
	return session, nil
}

func newBackgroundHTTPRouter(t *testing.T, work *service.BackgroundWork,
	sessionSvc interfaces.SessionService,
) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &Handler{background: work, sessionService: sessionSvc}
	r := gin.New()
	r.GET("/api/v1/sessions/:id/background-work", h.ListBackgroundWork)
	r.GET("/api/v1/sessions/:id/background-work/:work_id", h.GetBackgroundWork)
	r.POST("/api/v1/sessions/:session_id/background-work/:work_id/cancel", h.CancelBackgroundWork)
	return r
}

func ownedSessionContext(t *testing.T, tenant uint64, sessionID string) context.Context {
	t.Helper()
	ctx := types.WithExecutionTenant(context.Background(), tenant)
	ctx = types.WithSandboxTenantID(ctx, tenant)
	return types.WithSessionID(ctx, sessionID)
}

// splitCallerContext is a read whose execution tenant and sandbox-pin tenant
// both differ from the session row. The handler must still use session.TenantID.
func splitCallerContext(t *testing.T, sessionID string) context.Context {
	t.Helper()
	ctx := types.WithExecutionTenant(context.Background(), 99)
	ctx = types.WithSandboxTenantID(ctx, 100)
	return types.WithSessionID(ctx, sessionID)
}

func TestListMergesRegistryAndInboxWithoutExec(t *testing.T) {
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	work := service.NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	handler := &httpWorkHandler{status: map[string]interfaces.WorkResult{
		"sj_ab01": {
			ID: "sj_ab01", State: interfaces.WorkStateSucceeded, Terminal: true, Title: "build",
		},
	}}
	work.Handler = handler
	err := work.Registry.Add(context.Background(), ref,
		interfaces.BackgroundActivity{ID: "sj_0123", Kind: "shell", Title: "tests"}, time.Minute, 0)
	require.NoError(t, err)
	finished := time.Now().UTC()
	_, err = work.Inbox.Push(context.Background(), ref, interfaces.BackgroundNotice{
		ID: "sj_ab01", Kind: "shell", Text: "Background shell sj_ab01 succeeded: build.",
		FinishedAt: finished,
	})
	require.NoError(t, err)

	r := newBackgroundHTTPRouter(t, work, &stubSessionService{session: &types.Session{ID: "s", TenantID: 7}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/s/background-work", nil).
		WithContext(splitCallerContext(t, "s"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data, 2, "运行中（登记表）+ 24h 内终态（信箱）")
	states := map[string]any{}
	titles := map[string]any{}
	for _, item := range body.Data {
		states[item["work_id"].(string)] = item["state"]
		titles[item["work_id"].(string)] = item["title"]
	}
	require.Equal(t, "running", states["sj_0123"])
	require.Equal(t, "succeeded", states["sj_ab01"])
	require.Equal(t, "tests", titles["sj_0123"])
	require.Equal(t, "build", titles["sj_ab01"])
	require.NotEmpty(t, handler.opts, "终态 state 由 Status(Detailed=false) 批量补齐")
	for _, opts := range handler.opts {
		require.False(t, opts.Detailed)
		require.Empty(t, opts.RunID)
	}
	require.NotEmpty(t, handler.refs)
	for _, got := range handler.refs {
		require.Equal(t, uint64(7), got.TenantID)
		require.Equal(t, "s", got.SessionID)
	}
}

func TestDetailAndCancelEnforceOwnership(t *testing.T) {
	work := service.NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	handler := &httpWorkHandler{status: map[string]interfaces.WorkResult{}}
	work.Handler = handler
	svc := &stubSessionService{session: &types.Session{ID: "s", TenantID: 7}}
	r := newBackgroundHTTPRouter(t, work, svc)

	// Owner read goes through GetSession. Caller tenant and sandbox pin differ
	// from the session row; the status ref must still be the session tenant.
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/sessions/s/background-work/sj_0123", nil).
		WithContext(splitCallerContext(t, "s"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"state":"running"`)
	require.NotEmpty(t, handler.opts)
	last := handler.opts[len(handler.opts)-1]
	require.True(t, last.Detailed)
	require.Empty(t, last.RunID, "a user read must not move the model cursor")
	require.Equal(t, interfaces.SessionRef{TenantID: 7, SessionID: "s"}, handler.refs[len(handler.refs)-1])

	// 取消走严格 owner（stub 拒绝非 owner）
	req = httptest.NewRequest(http.MethodPost,
		"/api/v1/sessions/s/background-work/sj_0123/cancel", nil).
		WithContext(ownedSessionContext(t, 8, "s"))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code, "GetOwnedSession 失败 → 404，不区分 403 防探测")
	require.Empty(t, handler.cancel)
}

type errActivityRegistry struct {
	interfaces.ActivityRegistry
}

func (errActivityRegistry) List(context.Context, interfaces.SessionRef) ([]interfaces.BackgroundActivity, error) {
	return nil, errors.New("redis down")
}

func TestListStorageErrorIsNotAnEmptySuccess(t *testing.T) {
	work := service.NewBackgroundWork(errActivityRegistry{}, background.NewMemoryNoticeInbox(nil))
	work.Handler = &httpWorkHandler{}
	r := newBackgroundHTTPRouter(t, work, &stubSessionService{session: &types.Session{ID: "s", TenantID: 7}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/s/background-work", nil).
		WithContext(ownedSessionContext(t, 7, "s"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.GreaterOrEqual(t, rec.Code, 500)
	require.NotContains(t, rec.Body.String(), `"data":[]`)
}

func TestDisabledBackgroundWorkIsEmptyListAnd404(t *testing.T) {
	sessionSvc := &stubSessionService{session: &types.Session{ID: "s", TenantID: 7}}
	r := newBackgroundHTTPRouter(t, nil, sessionSvc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/s/background-work", nil).
		WithContext(ownedSessionContext(t, 7, "s"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"data":[]`)
	req = httptest.NewRequest(http.MethodGet,
		"/api/v1/sessions/s/background-work/sj_0123", nil).
		WithContext(ownedSessionContext(t, 7, "s"))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
	req = httptest.NewRequest(http.MethodPost,
		"/api/v1/sessions/s/background-work/sj_0123/cancel", nil).
		WithContext(ownedSessionContext(t, 7, "s"))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	// Enabled() is false until Handler, Registry, and Inbox are all set.
	// Rows already stored must not leak through a disabled bundle.
	work := service.NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	err := work.Registry.Add(context.Background(), ref,
		interfaces.BackgroundActivity{ID: "sj_0123", Kind: "shell", Title: "tests"}, time.Minute, 0)
	require.NoError(t, err)
	_, err = work.Inbox.Push(context.Background(), ref, interfaces.BackgroundNotice{
		ID: "sj_ab01", Kind: "shell", Text: "Background shell sj_ab01 succeeded: build.",
		FinishedAt: time.Now().UTC(),
	})
	require.NoError(t, err)
	require.False(t, work.Enabled())
	r = newBackgroundHTTPRouter(t, work, sessionSvc)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/sessions/s/background-work", nil).
		WithContext(ownedSessionContext(t, 7, "s"))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"data":[]`)
	req = httptest.NewRequest(http.MethodGet,
		"/api/v1/sessions/s/background-work/sj_0123", nil).
		WithContext(ownedSessionContext(t, 7, "s"))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}
