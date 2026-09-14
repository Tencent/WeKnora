package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type storageQuotaSettings struct {
	interfaces.SystemSettingService
}

func (storageQuotaSettings) GetInt(_ context.Context, key, _ string, def int64) int64 {
	if key == "tenant.default_storage_quota_mb" {
		return 50
	}
	return def
}

type bulkQuotaTenantService struct {
	interfaces.TenantService
	quota int64
}

func (s *bulkQuotaTenantService) BulkSetStorageQuota(_ context.Context, quota int64) (int64, error) {
	s.quota = quota
	return 3, nil
}

func TestApplyDefaultStorageQuotaUsesMBOverride(t *testing.T) {
	tenants := &bulkQuotaTenantService{}
	h := &SystemHandler{tenantSvc: tenants, systemSettingSvc: storageQuotaSettings{}}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/system/admin/tenants/apply-default-storage-quota", nil)
	h.ApplyDefaultStorageQuotaToAllTenants(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int64(50*1024*1024), tenants.quota)
	var response struct {
		Affected   int64   `json:"affected"`
		QuotaBytes int64   `json:"quota_bytes"`
		QuotaGB    float64 `json:"quota_gb"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, int64(3), response.Affected)
	require.Equal(t, tenants.quota, response.QuotaBytes)
	require.Equal(t, 50.0/1024, response.QuotaGB)
}
