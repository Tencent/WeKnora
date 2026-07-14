package router

import (
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var _ interfaces.FileService = (*stubFileService)(nil)

type stubFileService struct {
	getFile func(ctx context.Context, filePath string) (io.ReadCloser, error)
}

func (s *stubFileService) CheckConnectivity(ctx context.Context) error {
	return nil
}

func (s *stubFileService) SaveFile(ctx context.Context, file *multipart.FileHeader, tenantID uint64, knowledgeID string) (string, error) {
	panic("unexpected call to SaveFile")
}

func (s *stubFileService) SaveBytes(ctx context.Context, data []byte, tenantID uint64, fileName string, temp bool) (string, error) {
	panic("unexpected call to SaveBytes")
}

func (s *stubFileService) GetFile(ctx context.Context, filePath string) (io.ReadCloser, error) {
	if s.getFile == nil {
		panic("unexpected call to GetFile")
	}
	return s.getFile(ctx, filePath)
}

func (s *stubFileService) GetFileURL(ctx context.Context, filePath string) (string, error) {
	panic("unexpected call to GetFileURL")
}

func (s *stubFileService) DeleteFile(ctx context.Context, filePath string) error {
	panic("unexpected call to DeleteFile")
}

func (s *stubFileService) CopyFile(ctx context.Context, srcPath string, tenantID uint64, knowledgeID string) (string, error) {
	panic("unexpected call to CopyFile")
}

func TestServeFilesFallsBackToGlobalFileService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("STORAGE_TYPE", "local")

	engine := gin.New()
	var requestedPath string
	serveFiles(engine, &stubFileService{
		getFile: func(ctx context.Context, filePath string) (io.ReadCloser, error) {
			requestedPath = filePath
			return io.NopCloser(strings.NewReader("fallback-body")), nil
		},
	})

	filePath := "local://42/docs/example.txt"
	req := httptest.NewRequest(http.MethodGet, "/files?file_path="+url.QueryEscape(filePath), nil)
	req = req.WithContext(context.WithValue(req.Context(), types.TenantInfoContextKey, &types.Tenant{ID: 42}))

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if got, want := recorder.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if requestedPath != filePath {
		t.Fatalf("requested path = %q, want %q", requestedPath, filePath)
	}
	if body := recorder.Body.String(); body != "fallback-body" {
		t.Fatalf("body = %q, want %q", body, "fallback-body")
	}
}

func TestServeFilesDoesNotFallbackWhenProviderDoesNotMatchGlobalStorage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("STORAGE_TYPE", "minio")

	engine := gin.New()
	serveFiles(engine, &stubFileService{
		getFile: func(ctx context.Context, filePath string) (io.ReadCloser, error) {
			t.Fatalf("GetFile should not be called for mismatched provider, got %q", filePath)
			return nil, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/files?file_path="+url.QueryEscape("local://42/docs/example.txt"), nil)
	req = req.WithContext(context.WithValue(req.Context(), types.TenantInfoContextKey, &types.Tenant{ID: 42}))

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if got, want := recorder.Code, http.StatusBadRequest; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

func TestServeFilesRejectsCrossTenantPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("STORAGE_TYPE", "local")

	engine := gin.New()
	serveFiles(engine, &stubFileService{
		getFile: func(ctx context.Context, filePath string) (io.ReadCloser, error) {
			t.Fatalf("GetFile should not be called for cross-tenant path, got %q", filePath)
			return nil, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/files?file_path="+url.QueryEscape("local://7/knowledge/secret.pdf"), nil)
	req = req.WithContext(context.WithValue(req.Context(), types.TenantInfoContextKey, &types.Tenant{ID: 42}))

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if got, want := recorder.Code, http.StatusForbidden; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

func TestServeFilesRejectsPathWithoutTenantSegment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("STORAGE_TYPE", "local")

	engine := gin.New()
	serveFiles(engine, &stubFileService{
		getFile: func(ctx context.Context, filePath string) (io.ReadCloser, error) {
			t.Fatalf("GetFile should not be called without tenant segment, got %q", filePath)
			return nil, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/files?file_path="+url.QueryEscape("local://docs/example.txt"), nil)
	req = req.WithContext(context.WithValue(req.Context(), types.TenantInfoContextKey, &types.Tenant{ID: 42}))

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if got, want := recorder.Code, http.StatusForbidden; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

func TestServeFilesRejectsAPIKeyPrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("STORAGE_TYPE", "local")

	engine := gin.New()
	serveFiles(engine, &stubFileService{
		getFile: func(ctx context.Context, filePath string) (io.ReadCloser, error) {
			t.Fatalf("GetFile should not be called for API-key principal, got %q", filePath)
			return nil, nil
		},
	})

	filePath := "local://42/docs/example.txt"
	req := httptest.NewRequest(http.MethodGet, "/files?file_path="+url.QueryEscape(filePath), nil)
	ctx := context.WithValue(req.Context(), types.TenantInfoContextKey, &types.Tenant{ID: 42})
	ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{FullAccess: true})
	req = req.WithContext(ctx)

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if got, want := recorder.Code, http.StatusForbidden; got != want {
		t.Fatalf("status = %d, want %d body=%s", got, want, recorder.Body.String())
	}
}

// newKBScopedFilesTestEngine wires newKBScopedFileServeHandler behind a
// middleware that injects effectiveTenantID into the request context, mirroring
// what RequireKBAccess does after resolving an org-shared KB to its source
// tenant. This lets the handler be exercised without the full RBAC stack.
func newKBScopedFilesTestEngine(
	effectiveTenantID uint64,
	tenantSvc interfaces.TenantService,
	global interfaces.FileService,
) *gin.Engine {
	engine := gin.New()
	engine.GET("/knowledge-bases/:id/files",
		func(c *gin.Context) {
			ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, effectiveTenantID)
			c.Request = c.Request.WithContext(ctx)
			c.Next()
		},
		newKBScopedFileServeHandler(tenantSvc, global),
	)
	return engine
}

// A tenant whose owner-tenant (10008) storage objects are requested by a
// borrowing tenant via a shared KB: the effective tenant in context is the
// owner, so the path validates and the file is served.
func TestKBScopedFilesServesOwnerTenantPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("STORAGE_TYPE", "local")

	const ownerTenantID = uint64(10008)
	var requestedPath string
	engine := newKBScopedFilesTestEngine(
		ownerTenantID,
		&stubTenantService{get: func(_ context.Context, id uint64) (*types.Tenant, error) {
			return &types.Tenant{ID: id}, nil
		}},
		&stubFileService{getFile: func(_ context.Context, filePath string) (io.ReadCloser, error) {
			requestedPath = filePath
			return io.NopCloser(strings.NewReader("shared-body")), nil
		}},
	)

	filePath := "local://10008/exports/img.jpg"
	req := httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb-1/files?file_path="+url.QueryEscape(filePath), nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if got, want := recorder.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d body=%s", got, want, recorder.Body.String())
	}
	if requestedPath != filePath {
		t.Fatalf("requested path = %q, want %q", requestedPath, filePath)
	}
	if body := recorder.Body.String(); body != "shared-body" {
		t.Fatalf("body = %q, want %q", body, "shared-body")
	}
}

// The path must belong to the effective (owner) tenant. A path pointing at a
// different tenant than the resolved KB owner is still rejected, so the guard
// cannot be used to reach arbitrary tenants' files.
func TestKBScopedFilesRejectsPathNotOwnedByKBTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("STORAGE_TYPE", "local")

	const ownerTenantID = uint64(10008)
	engine := newKBScopedFilesTestEngine(
		ownerTenantID,
		&stubTenantService{get: func(_ context.Context, id uint64) (*types.Tenant, error) {
			t.Fatalf("GetTenantByID should not be called for mismatched path, got %d", id)
			return nil, nil
		}},
		&stubFileService{getFile: func(_ context.Context, filePath string) (io.ReadCloser, error) {
			t.Fatalf("GetFile should not be called for mismatched path, got %q", filePath)
			return nil, nil
		}},
	)

	req := httptest.NewRequest(http.MethodGet,
		"/knowledge-bases/kb-1/files?file_path="+url.QueryEscape("local://9999/exports/other.jpg"), nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if got, want := recorder.Code, http.StatusForbidden; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

// KB-scoped proxy is for embedded exports/ images only; raw knowledge uploads
// must use /knowledge/:id/download even when the tenant matches.
func TestKBScopedFilesRejectsNonExportsPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("STORAGE_TYPE", "local")

	const ownerTenantID = uint64(10008)
	engine := newKBScopedFilesTestEngine(
		ownerTenantID,
		&stubTenantService{get: func(_ context.Context, id uint64) (*types.Tenant, error) {
			t.Fatalf("GetTenantByID should not be called for non-exports path, got %d", id)
			return nil, nil
		}},
		&stubFileService{getFile: func(_ context.Context, filePath string) (io.ReadCloser, error) {
			t.Fatalf("GetFile should not be called for non-exports path, got %q", filePath)
			return nil, nil
		}},
	)

	req := httptest.NewRequest(http.MethodGet,
		"/knowledge-bases/kb-1/files?file_path="+url.QueryEscape("local://10008/knowledge-id/123.pdf"), nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if got, want := recorder.Code, http.StatusForbidden; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

func TestKBScopedFilesRequiresFilePath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	engine := newKBScopedFilesTestEngine(
		10008,
		&stubTenantService{},
		&stubFileService{},
	)

	req := httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb-1/files", nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if got, want := recorder.Code, http.StatusBadRequest; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

func TestServeFilesForcesActiveContentDownload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("STORAGE_TYPE", "local")

	engine := gin.New()
	serveFiles(engine, &stubFileService{
		getFile: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(`<svg onload="alert(1)"></svg>`)), nil
		},
	})

	filePath := "local://42/docs/payload.svg"
	req := httptest.NewRequest(http.MethodGet, "/files?file_path="+url.QueryEscape(filePath), nil)
	req = req.WithContext(context.WithValue(req.Context(), types.TenantInfoContextKey, &types.Tenant{ID: 42}))

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if got, want := recorder.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream", got)
	}
	if got := recorder.Header().Get("Content-Disposition"); got != "attachment" {
		t.Fatalf("Content-Disposition = %q, want attachment", got)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
}
