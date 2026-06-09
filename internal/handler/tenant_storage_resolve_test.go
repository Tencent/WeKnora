package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// makeJSONCtxWithTenant creates a gin.Context with a real ResponseRecorder so
// handlers calling c.JSON do not panic, and injects the tenant the same way
// the real middleware does.
func makeJSONCtxWithTenant(tenant *types.Tenant) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// TenantInfoFromContext reads from request.Context(), not gin's KV store.
	ctx := context.WithValue(req.Context(), types.TenantInfoContextKey, tenant)
	c.Request = req.WithContext(ctx)
	c.Set(types.TenantInfoContextKey.String(), tenant)
	return c, w
}

func decodeStorageConfigBody(t *testing.T, w *httptest.ResponseRecorder) *types.StorageEngineConfig {
	t.Helper()
	var body struct {
		Success bool                        `json:"success"`
		Data    *types.StorageEngineConfig `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v\nraw=%s", err, w.Body.String())
	}
	if !body.Success {
		t.Fatalf("success=false, raw=%s", w.Body.String())
	}
	return body.Data
}

// Tenant has no DefaultProvider, builtin supplies one → response is filled.
func TestGetTenantStorageEngineConfig_BuiltinDefaultProviderFills(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	types.SetBuiltinStorageEngineForTest(&types.StorageEngineConfig{
		DefaultProvider: "s3",
		S3: &types.S3EngineConfig{
			Endpoint: "ep", Region: "r", AccessKey: "ak", SecretKey: "sk", BucketName: "b",
		},
	})

	h := &TenantHandler{}
	c, w := makeJSONCtxWithTenant(&types.Tenant{})
	h.GetTenantStorageEngineConfig(c)

	got := decodeStorageConfigBody(t, w)
	if got == nil || got.DefaultProvider != "s3" {
		t.Fatalf("want DefaultProvider=s3, got=%+v", got)
	}
	// Builtin provider blocks (containing secrets) must NOT leak into response.
	if got.S3 != nil {
		t.Fatalf("S3 block must not be merged from builtin; got %+v", got.S3)
	}
}

// Tenant has its own DefaultProvider → builtin is ignored.
func TestGetTenantStorageEngineConfig_TenantDefaultProviderWins(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	types.SetBuiltinStorageEngineForTest(&types.StorageEngineConfig{
		DefaultProvider: "s3",
	})

	h := &TenantHandler{}
	c, w := makeJSONCtxWithTenant(&types.Tenant{
		StorageEngineConfig: &types.StorageEngineConfig{
			DefaultProvider: "oss",
		},
	})
	h.GetTenantStorageEngineConfig(c)

	got := decodeStorageConfigBody(t, w)
	if got.DefaultProvider != "oss" {
		t.Fatalf("want DefaultProvider=oss (tenant), got=%q", got.DefaultProvider)
	}
}

// No tenant config and no builtin → DefaultProvider stays empty (frontend falls back).
func TestGetTenantStorageEngineConfig_NoBuiltinNoTenant(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)

	h := &TenantHandler{}
	c, w := makeJSONCtxWithTenant(&types.Tenant{})
	h.GetTenantStorageEngineConfig(c)

	got := decodeStorageConfigBody(t, w)
	if got.DefaultProvider != "" {
		t.Fatalf("want empty DefaultProvider, got=%q", got.DefaultProvider)
	}
}

// Builtin DefaultProvider exists but is not in STORAGE_ALLOW_LIST → not filled.
func TestGetTenantStorageEngineConfig_BuiltinDefaultNotAllowed(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	types.SetBuiltinStorageEngineForTest(&types.StorageEngineConfig{
		DefaultProvider: "s3",
	})
	t.Setenv("STORAGE_ALLOW_LIST", "local,minio") // 故意排除 s3

	h := &TenantHandler{}
	c, w := makeJSONCtxWithTenant(&types.Tenant{})
	h.GetTenantStorageEngineConfig(c)

	got := decodeStorageConfigBody(t, w)
	if got.DefaultProvider != "" {
		t.Fatalf("want empty (builtin default disallowed), got=%q", got.DefaultProvider)
	}
}
