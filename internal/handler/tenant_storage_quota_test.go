package handler

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// quotaTenantService is a TenantService stub for the storage-quota
// handler tests. Only the methods UpdateTenant / GetTenantStorageStats
// exercise are implemented; the embedded nil interface panics on
// anything unexpected.
type quotaTenantService struct {
	interfaces.TenantService
	tenant           *types.Tenant
	updateQuotaCalls int
	updateQuotaErr   error
}

func (s *quotaTenantService) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	cp := *s.tenant
	cp.ID = id
	return &cp, nil
}

func (s *quotaTenantService) UpdateTenant(_ context.Context, tenant *types.Tenant) (*types.Tenant, error) {
	return tenant, nil
}

func (s *quotaTenantService) UpdateStorageQuota(
	_ context.Context, tenantID uint64, quotaBytes int64,
) (*types.Tenant, error) {
	s.updateQuotaCalls++
	if s.updateQuotaErr != nil {
		return nil, s.updateQuotaErr
	}
	cp := *s.tenant
	cp.ID = tenantID
	cp.StorageQuota = quotaBytes
	return &cp, nil
}

func (s *quotaTenantService) GetTenantStorageStats(
	_ context.Context, tenantID uint64,
) (*types.TenantStorageStats, error) {
	return &types.TenantStorageStats{
		DiskFreeBytes:    600,
		QuotaMaxBytes:    1000,
		StorageUsedBytes: 400,
	}, nil
}

// quotaAuditService captures audit rows so tests can assert the
// Owner-initiated quota change was recorded.
type quotaAuditService struct {
	interfaces.AuditLogService
	entries []*types.AuditLog
}

func (s *quotaAuditService) Log(_ context.Context, entry *types.AuditLog) error {
	s.entries = append(s.entries, entry)
	return nil
}

func setupQuotaUpdateRouter(svc *quotaTenantService, audit *quotaAuditService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &TenantHandler{service: svc, auditSvc: audit}
	r := gin.New()
	// tenantPolicyErrorCapture serialises the full AppError (including
	// details) so the validation-detail assertions below can see the
	// service's reason string.
	r.Use(tenantPolicyErrorCapture())
	r.PUT("/tenants/:id", h.UpdateTenant)
	return r
}

func putTenantBody(t *testing.T, r *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/tenants/1", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestUpdateTenantWithoutStorageQuotaLeavesQuotaUntouched(t *testing.T) {
	svc := &quotaTenantService{tenant: &types.Tenant{ID: 1, Name: "ws", StorageQuota: 1000, StorageUsed: 400}}
	audit := &quotaAuditService{}
	r := setupQuotaUpdateRouter(svc, audit)

	w := putTenantBody(t, r, `{"name":"renamed"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if svc.updateQuotaCalls != 0 {
		t.Fatalf("UpdateStorageQuota called %d times for a body without storage_quota", svc.updateQuotaCalls)
	}
	if !strings.Contains(w.Body.String(), `"storage_quota":1000`) {
		t.Fatalf("quota changed in response: %s", w.Body.String())
	}
	if len(audit.entries) != 0 {
		t.Fatalf("unexpected audit entries without a quota change: %d", len(audit.entries))
	}
}

func TestUpdateTenantRejectsQuotaWhenServiceValidationFails(t *testing.T) {
	svc := &quotaTenantService{
		tenant:         &types.Tenant{ID: 1, Name: "ws", StorageQuota: 1000, StorageUsed: 400},
		updateQuotaErr: errors.New("storage quota exceeds available disk capacity"),
	}
	audit := &quotaAuditService{}
	r := setupQuotaUpdateRouter(svc, audit)

	w := putTenantBody(t, r, `{"storage_quota":50}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "storage quota exceeds available disk capacity") {
		t.Fatalf("validation detail missing from response: %s", w.Body.String())
	}
	if len(audit.entries) != 0 {
		t.Fatalf("audit entries written for a rejected change: %d", len(audit.entries))
	}
}

func TestUpdateTenantAppliesStorageQuotaAndAudits(t *testing.T) {
	svc := &quotaTenantService{tenant: &types.Tenant{ID: 1, Name: "ws", StorageQuota: 1000, StorageUsed: 400}}
	audit := &quotaAuditService{}
	r := setupQuotaUpdateRouter(svc, audit)

	w := putTenantBody(t, r, `{"storage_quota":900}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if svc.updateQuotaCalls != 1 {
		t.Fatalf("UpdateStorageQuota called %d times, want 1", svc.updateQuotaCalls)
	}
	if !strings.Contains(w.Body.String(), `"storage_quota":900`) {
		t.Fatalf("response does not report the new quota: %s", w.Body.String())
	}
	if len(audit.entries) != 1 {
		t.Fatalf("audit entries=%d, want 1", len(audit.entries))
	}
	entry := audit.entries[0]
	if entry.Action != types.AuditActionTenantStorageQuotaUpdated {
		t.Fatalf("audit action=%q, want %q", entry.Action, types.AuditActionTenantStorageQuotaUpdated)
	}
	if entry.TenantID != 1 || entry.TargetID != "1" || entry.TargetType != "tenant_storage_quota" {
		t.Fatalf("audit target mismatch: %+v", entry)
	}
	details := string(entry.Details)
	for _, want := range []string{`"old_quota_bytes":1000`, `"new_quota_bytes":900`, `"disk_free_bytes":600`} {
		if !strings.Contains(details, want) {
			t.Fatalf("audit details missing %s: %s", want, details)
		}
	}
}

func TestGetTenantStorageStats(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &quotaTenantService{tenant: &types.Tenant{ID: 1, Name: "ws", StorageQuota: 1000, StorageUsed: 400}}
	h := &TenantHandler{service: svc}
	r := gin.New()
	r.Use(errorCapture())
	r.GET("/tenants/:id/storage-stats", h.GetTenantStorageStats)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tenants/1/storage-stats", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	for _, want := range []string{`"disk_free_bytes":600`, `"quota_max_bytes":1000`, `"storage_used_bytes":400`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("stats response missing %s: %s", want, w.Body.String())
		}
	}
}
