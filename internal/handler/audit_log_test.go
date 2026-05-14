package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubAuditService implements just AuditLogService.List for the
// handler tests. Embeds the interface so any other call panics —
// surfaces a contract drift loudly instead of silently working.
type stubAuditService struct {
	interfaces.AuditLogService
	list func(ctx context.Context, tenantID uint64, q *interfaces.AuditLogQuery) ([]*types.AuditLog, error)
}

func (s *stubAuditService) List(
	ctx context.Context, tenantID uint64, q *interfaces.AuditLogQuery,
) ([]*types.AuditLog, error) {
	return s.list(ctx, tenantID, q)
}

// newAuditHandlerTestRouter mounts the handler with the production
// ErrorHandler so c.Error renders the canonical envelope. Path :id is
// resolved by parseTenantIDFromPath; tenant context is not required by
// the handler (PathTenantMatch is stripped from this layer because the
// tests focus on the handler's own surface).
func newAuditHandlerTestRouter(svc interfaces.AuditLogService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	h := NewAuditLogHandler(svc)
	r.GET("/tenants/:id/audit-log", h.ListTenantAuditLog)
	return r
}

func TestAuditLogHandler_ReturnsEnvelopeAndCursor(t *testing.T) {
	// Two rows: smallest id appears in the next_cursor field so the
	// frontend can re-request older pages without re-parsing the body.
	svc := &stubAuditService{
		list: func(_ context.Context, tenantID uint64, q *interfaces.AuditLogQuery) ([]*types.AuditLog, error) {
			if tenantID != 7 {
				t.Fatalf("expected tenant 7, got %d", tenantID)
			}
			return []*types.AuditLog{
				{ID: 102, TenantID: 7, Action: types.AuditActionMemberAdded},
				{ID: 95, TenantID: 7, Action: types.AuditActionAccessDenied, Outcome: types.AuditOutcomeDenied},
			}, nil
		},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tenants/7/audit-log", nil)
	newAuditHandlerTestRouter(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var got auditLogListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Success {
		t.Fatalf("expected success=true")
	}
	if len(got.Data) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got.Data))
	}
	if got.NextCursor != 95 {
		t.Fatalf("expected next_cursor to be the smallest id (95), got %d", got.NextCursor)
	}
}

func TestAuditLogHandler_PassesQueryFiltersThrough(t *testing.T) {
	// The handler must propagate after_id / limit / action / outcome /
	// actor exactly as the service expects them. A regression here
	// would silently drop a filter and over-return rows on the wire.
	svc := &stubAuditService{
		list: func(_ context.Context, _ uint64, q *interfaces.AuditLogQuery) ([]*types.AuditLog, error) {
			if q.AfterID != 100 {
				t.Fatalf("expected after_id=100, got %d", q.AfterID)
			}
			if q.Limit != 25 {
				t.Fatalf("expected limit=25, got %d", q.Limit)
			}
			if q.Action != types.AuditActionAccessDenied {
				t.Fatalf("expected action=access_denied, got %q", q.Action)
			}
			if q.Outcome != types.AuditOutcomeDenied {
				t.Fatalf("expected outcome=denied, got %q", q.Outcome)
			}
			if q.ActorUserID != "u-probing" {
				t.Fatalf("expected actor=u-probing, got %q", q.ActorUserID)
			}
			return nil, nil
		},
	}
	w := httptest.NewRecorder()
	q := "after_id=100&limit=25&action=rbac.access_denied&outcome=denied&actor=u-probing"
	req := httptest.NewRequest(http.MethodGet, "/tenants/7/audit-log?"+q, nil)
	newAuditHandlerTestRouter(svc).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestAuditLogHandler_EmptyResultProducesZeroCursor(t *testing.T) {
	// next_cursor=0 is the documented "no more rows" signal; the frontend
	// stops paginating when it sees this, so a regression that returns
	// the previous cursor on an empty page would loop forever.
	svc := &stubAuditService{
		list: func(_ context.Context, _ uint64, _ *interfaces.AuditLogQuery) ([]*types.AuditLog, error) {
			return nil, nil
		},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tenants/7/audit-log", nil)
	newAuditHandlerTestRouter(svc).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var got auditLogListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.NextCursor != 0 {
		t.Fatalf("expected next_cursor=0 on empty page, got %d", got.NextCursor)
	}
}

func TestAuditLogHandler_InvalidTenantIDReturns400(t *testing.T) {
	// parseTenantIDFromPath rejects non-numeric tenant ids with 400 so
	// the handler never even calls the service. The harness still has
	// to not crash on an empty service if it's reached — guard with a
	// service that fails the test loudly.
	svc := &stubAuditService{
		list: func(_ context.Context, _ uint64, _ *interfaces.AuditLogQuery) ([]*types.AuditLog, error) {
			return nil, fmt.Errorf("must not be called")
		},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tenants/not-a-number/audit-log", nil)
	newAuditHandlerTestRouter(svc).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-numeric tenant id, got %d body=%s", w.Code, w.Body.String())
	}
}
