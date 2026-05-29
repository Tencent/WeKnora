package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// stubTenantService is a minimal stub of interfaces.TenantService that only
// implements GetTenantByID. Every other method panics — those code paths
// must not be exercised by presigned routes; the panic surfaces test bugs
// instead of silently returning zero values.
type stubTenantService struct {
	get func(ctx context.Context, id uint64) (*types.Tenant, error)
}

var _ interfaces.TenantService = (*stubTenantService)(nil)

func (s *stubTenantService) GetTenantByID(ctx context.Context, id uint64) (*types.Tenant, error) {
	if s.get == nil {
		return nil, os.ErrNotExist
	}
	return s.get(ctx, id)
}

func (s *stubTenantService) CreateTenant(context.Context, *types.Tenant) (*types.Tenant, error) {
	panic("unexpected")
}

func (s *stubTenantService) GetTenantsByIDs(context.Context, []uint64) (map[uint64]*types.Tenant, error) {
	panic("unexpected")
}
func (s *stubTenantService) ListTenants(context.Context) ([]*types.Tenant, error) {
	panic("unexpected")
}

func (s *stubTenantService) UpdateTenant(context.Context, *types.Tenant) (*types.Tenant, error) {
	panic("unexpected")
}
func (s *stubTenantService) DeleteTenant(context.Context, uint64) error { panic("unexpected") }
func (s *stubTenantService) UpdateAPIKey(context.Context, uint64) (string, error) {
	panic("unexpected")
}
func (s *stubTenantService) ExtractTenantIDFromAPIKey(string) (uint64, error) { panic("unexpected") }
func (s *stubTenantService) ListAllTenants(context.Context) ([]*types.Tenant, error) {
	panic("unexpected")
}
func (s *stubTenantService) BulkSetStorageQuota(context.Context, int64) (int64, error) {
	panic("unexpected")
}

func (s *stubTenantService) SearchTenants(context.Context, string, uint64, int, int) ([]*types.Tenant, int64, error) {
	panic("unexpected")
}

func (s *stubTenantService) GetTenantByIDForUser(context.Context, uint64, string) (*types.Tenant, error) {
	panic("unexpected")
}

func (s *stubTenantService) GetWeKnoraCloudCredentials(context.Context) *types.WeKnoraCloudCredentials {
	panic("unexpected")
}

// setupPresignedTestServer wires presignedFileHandler with a real local file
// service rooted at a temp dir, returning the engine, baseDir, and the
// presigned URL generator helper.
func setupPresignedTestServer(t *testing.T) (engine *gin.Engine, baseDir string, signURL func(filePath string, tenantID uint64, ttl time.Duration) string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("SYSTEM_AES_KEY", "weknora-test-aes-key-32bytes!!!")

	baseDir = t.TempDir()

	tenant := &types.Tenant{
		ID: 1,
		StorageEngineConfig: &types.StorageEngineConfig{
			DefaultProvider: "local",
			Local:           &types.LocalEngineConfig{},
		},
	}
	stubTS := &stubTenantService{
		get: func(_ context.Context, id uint64) (*types.Tenant, error) {
			if id == tenant.ID {
				return tenant, nil
			}
			return nil, os.ErrNotExist
		},
	}

	engine = gin.New()
	handler := presignedFileHandler(stubTS, baseDir)
	engine.GET("/api/v1/files/presigned", handler)
	engine.HEAD("/api/v1/files/presigned", handler)

	signURL = func(filePath string, tenantID uint64, ttl time.Duration) string {
		signed, err := secutils.SignFileURL("https://weknora.example.com", filePath, tenantID, ttl)
		if err != nil {
			t.Fatalf("SignFileURL: %v", err)
		}
		// Re-parse so we only keep the query part: the test server is on
		// a different host than the canonical signing baseURL.
		u, _ := url.Parse(signed)
		return "/api/v1/files/presigned?" + u.RawQuery
	}

	return engine, baseDir, signURL
}

// writeTestFile creates baseDir/<relPath> with the given content and returns
// the matching `local://<relPath>` storage path.
func writeTestFile(t *testing.T, baseDir, relPath, content string) string {
	t.Helper()
	full := filepath.Join(baseDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return "local://" + relPath
}

func TestPresignedFile_HEAD_Returns200WithoutBody(t *testing.T) {
	engine, baseDir, signURL := setupPresignedTestServer(t)
	storagePath := writeTestFile(t, baseDir, "1/img.png", "PNG-BYTES")

	req := httptest.NewRequest(http.MethodHead, signURL(storagePath, 1, time.Hour), nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if got, want := w.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d (body=%q)", got, want, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", got)
	}
	// HEAD must not stream the body — protects backing storage from a
	// full read on every IM preview probe.
	if w.Body.Len() != 0 {
		t.Fatalf("HEAD response body should be empty, got %d bytes", w.Body.Len())
	}
}

func TestPresignedFile_GET_ReturnsContent(t *testing.T) {
	engine, baseDir, signURL := setupPresignedTestServer(t)
	storagePath := writeTestFile(t, baseDir, "1/img.png", "PNG-BYTES")

	req := httptest.NewRequest(http.MethodGet, signURL(storagePath, 1, time.Hour), nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if got, want := w.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got := w.Body.String(); got != "PNG-BYTES" {
		t.Fatalf("body = %q, want %q", got, "PNG-BYTES")
	}
}

func TestPresignedFile_InvalidSig_403(t *testing.T) {
	engine, _, _ := setupPresignedTestServer(t)

	// Hand-craft a URL with a tampered signature.
	q := url.Values{}
	q.Set("file_path", "local://1/img.png")
	q.Set("tenant_id", "1")
	q.Set("expires", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	q.Set("sig", "deadbeefdeadbeef")

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/v1/files/presigned?"+q.Encode(), nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			if got, want := w.Code, http.StatusForbidden; got != want {
				t.Fatalf("status = %d, want %d", got, want)
			}
		})
	}
}

func TestPresignedFile_MissingParams_400(t *testing.T) {
	engine, _, _ := setupPresignedTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/presigned?file_path=local%3A%2F%2F1%2Fimg.png", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if got, want := w.Code, http.StatusBadRequest; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if !strings.Contains(w.Body.String(), "missing required parameters") {
		t.Fatalf("body = %q, want missing-params error", w.Body.String())
	}
}

func TestPresignedFile_MissingFile_404(t *testing.T) {
	engine, _, signURL := setupPresignedTestServer(t)

	// A legitimately signed URL pointing at a file that does not exist.
	req := httptest.NewRequest(http.MethodGet, signURL("local://1/nope.png", 1, time.Hour), nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if got, want := w.Code, http.StatusNotFound; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}
