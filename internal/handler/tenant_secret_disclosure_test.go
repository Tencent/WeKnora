package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubTenantService struct {
	tenant *types.Tenant
}

func (s *stubTenantService) UpdateTenant(_ context.Context, tenant *types.Tenant) (*types.Tenant, error) {
	s.tenant = tenant
	return tenant, nil
}

func (s *stubTenantService) CreateTenant(context.Context, *types.Tenant) (*types.Tenant, error) {
	return nil, nil
}
func (s *stubTenantService) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return s.tenant, nil
}
func (s *stubTenantService) GetTenantsByIDs(context.Context, []uint64) (map[uint64]*types.Tenant, error) {
	return map[uint64]*types.Tenant{s.tenant.ID: s.tenant}, nil
}
func (s *stubTenantService) DeleteTenant(context.Context, uint64) error { return nil }
func (s *stubTenantService) ListTenants(context.Context) ([]*types.Tenant, error) {
	return []*types.Tenant{s.tenant}, nil
}
func (s *stubTenantService) ListAllTenants(context.Context) ([]*types.Tenant, error) {
	return nil, nil
}
func (s *stubTenantService) SearchTenants(context.Context, string, uint64, int, int) ([]*types.Tenant, int64, error) {
	return nil, 0, nil
}
func (s *stubTenantService) UpdateAPIKey(context.Context, uint64) (string, error) { return "", nil }
func (s *stubTenantService) ExtractTenantIDFromAPIKey(string) (uint64, error)      { return 0, nil }
func (s *stubTenantService) BulkSetStorageQuota(context.Context, int64) (int64, error) {
	return 0, nil
}
func (s *stubTenantService) GetTenantByIDForUser(context.Context, uint64, string) (*types.Tenant, error) {
	return s.tenant, nil
}
func (s *stubTenantService) GetWeKnoraCloudCredentials(context.Context) *types.WeKnoraCloudCredentials {
	return nil
}

func newTenantHandlerTestEngine(t *testing.T, role types.TenantRole, tenant *types.Tenant) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &TenantHandler{service: &stubTenantService{tenant: tenant}}

	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		ctx := c.Request.Context()
		ctx = context.WithValue(ctx, types.TenantIDContextKey, tenant.ID)
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, role)
		ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), tenant.ID)
		c.Next()
	})
	r.GET("/tenants", h.ListTenants)
	r.GET("/tenants/:id", h.GetTenant)
	r.GET("/tenants/kv/:key", h.GetTenantKV)
	r.PUT("/tenants/kv/:key", h.UpdateTenantKV)
	return r
}

func TestListTenantsViewerDoesNotLeakSecrets(t *testing.T) {
	tenant := secretTenantFixture()
	engine := newTenantHandlerTestEngine(t, types.TenantRoleViewer, tenant)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tenants", nil)
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.NotContains(t, body, "tenant-api-key-123")
	assert.NotContains(t, body, "parser-secret-123")
}

func TestGetTenantViewerDoesNotLeakSecrets(t *testing.T) {
	tenant := secretTenantFixture()
	engine := newTenantHandlerTestEngine(t, types.TenantRoleViewer, tenant)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tenants/42", nil)
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "tenant-api-key-123")
}

func TestGetTenantKVViewerForbiddenForSecretKeys(t *testing.T) {
	tenant := secretTenantFixture()
	engine := newTenantHandlerTestEngine(t, types.TenantRoleViewer, tenant)

	for _, key := range []string{"web-search-config", "parser-engine-config", "storage-engine-config"} {
		t.Run(key, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/tenants/kv/"+key, nil)
			engine.ServeHTTP(rec, req)
			require.Equal(t, http.StatusForbidden, rec.Code)
		})
	}
}

func TestGetTenantKVAdminReturnsRedactedSecrets(t *testing.T) {
	tenant := secretTenantFixture()
	engine := newTenantHandlerTestEngine(t, types.TenantRoleAdmin, tenant)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tenants/kv/parser-engine-config", nil)
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload struct {
		Success bool                           `json:"success"`
		Data    types.ParserEngineConfig       `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	assert.Equal(t, types.RedactedSecretPlaceholder, payload.Data.MinerUAPIKey)
	assert.NotContains(t, rec.Body.String(), "parser-secret-123")
}

func secretTenantFixture() *types.Tenant {
	return &types.Tenant{
		ID:     42,
		Name:   "tenant",
		APIKey: "tenant-api-key-123",
		WebSearchConfig: &types.WebSearchConfig{
			APIKey: "legacy-search-secret-999",
		},
		ParserEngineConfig: &types.ParserEngineConfig{
			MinerUAPIKey: "parser-secret-123",
		},
		StorageEngineConfig: &types.StorageEngineConfig{
			MinIO: &types.MinIOEngineConfig{
				SecretAccessKey: "minio-secret-789",
			},
		},
	}
}

func TestGetTenantKVViewerAllowedForNonSecretKey(t *testing.T) {
	tenant := secretTenantFixture()
	tenant.RetrievalConfig = &types.RetrievalConfig{}
	engine := newTenantHandlerTestEngine(t, types.TenantRoleViewer, tenant)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tenants/kv/retrieval-config", nil)
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestPutTenantParserConfigAdminPreservesRedactedSecrets(t *testing.T) {
	tenant := secretTenantFixture()
	engine := newTenantHandlerTestEngine(t, types.TenantRoleAdmin, tenant)

	body := `{"mineru_api_key":"***","mineru_endpoint":"http://new-endpoint"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/tenants/kv/parser-engine-config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, tenant.ParserEngineConfig)
	assert.Equal(t, "parser-secret-123", tenant.ParserEngineConfig.MinerUAPIKey)
	assert.Equal(t, "http://new-endpoint", tenant.ParserEngineConfig.MinerUEndpoint)
}
