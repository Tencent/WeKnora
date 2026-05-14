package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// PR 6 (#1303): the rbac middleware's two reject paths now call
// AuditServiceFromContext(c).LogDenied so denials are durably
// recorded. These tests pin that wiring — without them, a future
// refactor that drops the AuditServiceProvider middleware (or breaks
// the context-stash key) would silently lose the durable audit
// without any coverage signal.

// stubDenyAudit captures LogDenied calls. Embeds the interface so any
// other method panics on use — keeps tests honest about which API
// they're exercising.
type stubDenyAudit struct {
	interfaces.AuditLogService
	mu    sync.Mutex
	calls []denyCall
}

type denyCall struct {
	tenantID    uint64
	actorUserID string
	actorRole   string
	required    types.TenantRole
}

func (s *stubDenyAudit) LogDenied(
	_ context.Context,
	_ *gin.Context,
	tenantID uint64,
	actorUserID, actorRole string,
	requiredRole types.TenantRole,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, denyCall{tenantID, actorUserID, actorRole, requiredRole})
	return nil
}

// auditableHarness mirrors rbacTestHarness but additionally wires
// AuditServiceProvider with the supplied stub so the rejection
// paths can exercise the audit hook.
func auditableHarness(
	t *testing.T, role types.TenantRole, userID string, tenantID uint64,
	audit interfaces.AuditLogService, mw gin.HandlerFunc,
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuditServiceProvider(audit))
	r.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantRoleContextKey, role)
		ctx = context.WithValue(ctx, types.UserIDContextKey, userID)
		ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.GET("/protected", mw, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	r.ServeHTTP(w, req)
	return w
}

func TestRequireRole_RejectFiresAuditHook(t *testing.T) {
	audit := &stubDenyAudit{}
	w := auditableHarness(t, types.TenantRoleContributor, "u1", 7, audit,
		RequireRole(types.TenantRoleAdmin, cfgRBAC(true)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
	if len(audit.calls) != 1 {
		t.Fatalf("expected exactly one audit hook call, got %d", len(audit.calls))
	}
	got := audit.calls[0]
	if got.tenantID != 7 || got.actorUserID != "u1" ||
		got.actorRole != string(types.TenantRoleContributor) ||
		got.required != types.TenantRoleAdmin {
		t.Fatalf("audit hook payload mismatch: %+v", got)
	}
}

func TestRequireRole_DormantModeDoesNotFireAuditHook(t *testing.T) {
	// EnableRBAC=false: middleware logs but does NOT 403, so the
	// durable audit must NOT fire either — the dormant rollout window
	// would otherwise generate audit noise for non-rejections.
	audit := &stubDenyAudit{}
	w := auditableHarness(t, types.TenantRoleViewer, "u1", 7, audit,
		RequireRole(types.TenantRoleOwner, cfgRBAC(false)))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 in dormant mode, got %d", w.Code)
	}
	if len(audit.calls) != 0 {
		t.Fatalf("dormant mode must not fire audit, got %d calls", len(audit.calls))
	}
}

func TestRequireOwnershipOrRole_RejectFiresAuditHook(t *testing.T) {
	// Mirror of TestRequireRole_RejectFiresAuditHook but for the
	// ownership variant — the audit hook lives at a different reject
	// site and must NOT skip the durable write.
	audit := &stubDenyAudit{}
	lookup := func(_ *gin.Context) (string, error) {
		return "someone-else", nil // creator != caller, role too low
	}
	w := auditableHarness(t, types.TenantRoleContributor, "u1", 7, audit,
		RequireOwnershipOrRole(types.TenantRoleAdmin, lookup, cfgRBAC(true)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
	if len(audit.calls) != 1 {
		t.Fatalf("expected exactly one audit hook call, got %d", len(audit.calls))
	}
}

func TestRequireRole_NilAuditServiceDoesNotPanic(t *testing.T) {
	// AuditServiceProvider(nil) is a deliberate no-op; the rbac path
	// must degrade to "log to stderr only" rather than crashing.
	w := auditableHarness(t, types.TenantRoleContributor, "u1", 7, nil,
		RequireRole(types.TenantRoleAdmin, cfgRBAC(true)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 even with nil audit service, got %d", w.Code)
	}
}
