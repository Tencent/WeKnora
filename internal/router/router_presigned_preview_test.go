package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

func servePresignedPreviewForTest(t *testing.T, catalog interfaces.ResourceCatalog, filePath string) int {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("LOCAL_STORAGE_BASE_DIR", t.TempDir())
	engine := gin.New()
	servePresignedPreview(engine, nil, nil, catalog)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/files/presigned-preview?file_path="+url.QueryEscape(filePath), nil)
	ctx := context.WithValue(req.Context(), types.TenantInfoContextKey, &types.Tenant{ID: 42})
	ctx = types.WithCaller(ctx, types.Caller{TenantID: 42, UserID: "admin", Role: types.TenantRoleAdmin})
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req.WithContext(ctx))
	return recorder.Code
}

// A known handle of another tenant's resource must not be exchangeable for an
// anonymous /r/ grant (the stub catalog panics if a grant is minted).
func TestPresignedPreviewRejectsCrossTenantResource(t *testing.T) {
	catalog := &stubResourceCatalog{resource: &types.StoredResource{
		TenantID: 7, PhysicalPath: "local://7/exports/a.png",
	}}
	code := servePresignedPreviewForTest(t, catalog, "resource://AbCdEfGhIjKlMnOpQrStUv")
	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", code, http.StatusForbidden)
	}
}

func TestPresignedPreviewRejectsCrossTenantPath(t *testing.T) {
	for _, filePath := range []string{"local://7/knowledge/secret.pdf", "local://docs/example.txt"} {
		if code := servePresignedPreviewForTest(t, &stubResourceCatalog{}, filePath); code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want %d", filePath, code, http.StatusForbidden)
		}
	}
}

// Without a catalog a resource reference cannot be attributed to a tenant, so
// it is refused rather than signed.
func TestPresignedPreviewRejectsUnresolvableResource(t *testing.T) {
	if code := servePresignedPreviewForTest(t, nil, "resource://AbCdEfGhIjKlMnOpQrStUv"); code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", code, http.StatusForbidden)
	}
}

func TestPresignedPreviewAllowsOwnTenantPath(t *testing.T) {
	code := servePresignedPreviewForTest(t, &stubResourceCatalog{}, "local://42/exports/a.png")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want %d", code, http.StatusOK)
	}
}
