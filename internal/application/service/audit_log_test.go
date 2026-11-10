package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// stubAuditRepo collects Create calls and answers CountSinceForDedup
// from the in-memory state. Embeds the interface so any unstubbed
// method will nil-panic, surfacing a contract drift loudly instead of
// silently returning zero-values.
type stubAuditRepo struct {
	interfaces.AuditLogRepository

	mu      sync.Mutex
	created []*types.AuditLog
	// countErr lets a test inject a transient lookup failure.
	countErr error
}

func (s *stubAuditRepo) Create(_ context.Context, entry *types.AuditLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.created = append(s.created, entry)
	return nil
}

func (s *stubAuditRepo) CountSinceForDedup(
	_ context.Context, tenantID uint64, actorUserID string,
	action types.AuditAction, requestPath string, since time.Time,
) (int64, error) {
	if s.countErr != nil {
		return 0, s.countErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, e := range s.created {
		if e.TenantID == tenantID &&
			e.ActorUserID == actorUserID &&
			e.Action == action &&
			e.RequestPath == requestPath &&
			!e.CreatedAt.Before(since) {
			n++
		}
	}
	return n, nil
}

// fakeClock returns a controllable time source so the dedup-window
// tests can simulate "1 minute later" without sleeping.
type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time           { return f.t }
func (f *fakeClock) Advance(by time.Duration) { f.t = f.t.Add(by) }

func newSvcForTest() (*auditLogService, *stubAuditRepo, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)}
	repo := &stubAuditRepo{}
	svc := &auditLogService{repo: repo, now: clock.Now}
	return svc, repo, clock
}

func newDeniedCtx(t *testing.T, method, path string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(method, path, nil)
	return c
}

func TestAuditLog_Log_FillsCreatedAtAndOutcome(t *testing.T) {
	svc, repo, clock := newSvcForTest()
	entry := &types.AuditLog{
		TenantID: 7,
		Action:   types.AuditActionMemberAdded,
		// CreatedAt left zero, Outcome left empty
	}
	if err := svc.Log(context.Background(), entry); err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("expected 1 written entry, got %d", len(repo.created))
	}
	if !entry.CreatedAt.Equal(clock.Now()) {
		t.Fatalf("expected CreatedAt to default to clock time, got %v", entry.CreatedAt)
	}
	if entry.Outcome != types.AuditOutcomeSuccess {
		t.Fatalf("expected Outcome to default to success, got %q", entry.Outcome)
	}
}

func TestAuditLog_Log_RejectsEmptyAction(t *testing.T) {
	// Schema requires action; the service guards the contract upfront so
	// callers get a clean error instead of a constraint violation later.
	svc, _, _ := newSvcForTest()
	err := svc.Log(context.Background(), &types.AuditLog{TenantID: 7})
	if err == nil {
		t.Fatalf("expected error when Action is empty")
	}
}

func TestAuditLog_LogDenied_DedupesRepeatedRejectsWithinWindow(t *testing.T) {
	// First denied write hits the table; second within the window does
	// NOT — the dedup primitive is the headline durability guarantee
	// against probing clients filling the audit table at line rate.
	svc, repo, _ := newSvcForTest()
	c := newDeniedCtx(t, "PUT", "/api/v1/tenants/7")

	if err := svc.LogDenied(context.Background(), c, 7, "u-viewer", "viewer", types.TenantRoleAdmin); err != nil {
		t.Fatalf("LogDenied: %v", err)
	}
	if err := svc.LogDenied(context.Background(), c, 7, "u-viewer", "viewer", types.TenantRoleAdmin); err != nil {
		t.Fatalf("LogDenied second call: %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("expected dedup to drop second write, got %d entries", len(repo.created))
	}
}

func TestAuditLog_LogDenied_WritesAgainAfterWindowExpires(t *testing.T) {
	// The dedup is a sliding window, not a once-per-tuple lock — once
	// the trailing window is empty, the next denied call must record.
	svc, repo, clock := newSvcForTest()
	c := newDeniedCtx(t, "PUT", "/api/v1/tenants/7")

	_ = svc.LogDenied(context.Background(), c, 7, "u-viewer", "viewer", types.TenantRoleAdmin)
	clock.Advance(denyDedupWindow + time.Second)
	_ = svc.LogDenied(context.Background(), c, 7, "u-viewer", "viewer", types.TenantRoleAdmin)

	if len(repo.created) != 2 {
		t.Fatalf("expected window-expiry to allow second write, got %d entries", len(repo.created))
	}
}

func TestAuditLog_LogDenied_DedupIsPerActorAndPath(t *testing.T) {
	// Two distinct actors hitting the same endpoint, or the same actor
	// hitting two distinct endpoints, must each record. The dedup key
	// is (tenant, actor, action, path) — anything else collides.
	svc, repo, _ := newSvcForTest()
	c1 := newDeniedCtx(t, "PUT", "/api/v1/tenants/7")
	c2 := newDeniedCtx(t, "PUT", "/api/v1/agents/abc")

	_ = svc.LogDenied(context.Background(), c1, 7, "u-viewer-a", "viewer", types.TenantRoleAdmin)
	_ = svc.LogDenied(context.Background(), c1, 7, "u-viewer-b", "viewer", types.TenantRoleAdmin)
	_ = svc.LogDenied(context.Background(), c2, 7, "u-viewer-a", "viewer", types.TenantRoleAdmin)

	if len(repo.created) != 3 {
		t.Fatalf("expected 3 distinct (actor,path) writes, got %d", len(repo.created))
	}
}

func TestAuditLog_LogDenied_DegradesGracefullyOnDedupLookupError(t *testing.T) {
	// If the dedup count returns an error (DB hiccup, transient), we
	// must NOT silently skip the audit row. Better to write a
	// duplicate than to lose a denied event during incident response.
	svc, repo, _ := newSvcForTest()
	repo.countErr = errors.New("transient")
	c := newDeniedCtx(t, "PUT", "/api/v1/tenants/7")

	if err := svc.LogDenied(context.Background(), c, 7, "u-viewer", "viewer", types.TenantRoleAdmin); err != nil {
		t.Fatalf("LogDenied with dedup error: %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("expected fallthrough write on dedup error, got %d entries", len(repo.created))
	}
}
