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

	appservice "github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func testFileServeConfig(getFile func(ctx context.Context, filePath string) (io.ReadCloser, error)) fileServeConfig {
	return fileServeConfig{
		globalFileService: &stubFileService{getFile: getFile},
	}
}

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

type stubTenantServiceForFiles struct {
	tenants map[uint64]*types.Tenant
}

func (s *stubTenantServiceForFiles) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	if t, ok := s.tenants[id]; ok {
		return t, nil
	}
	return nil, http.ErrMissingFile
}

type stubKBRepoForFiles struct {
	kbs map[string]*types.KnowledgeBase
}

func (s *stubKBRepoForFiles) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if kb, ok := s.kbs[id]; ok {
		return kb, nil
	}
	return nil, nil
}

type stubChunkRepoForFiles struct {
	byRef map[string][]string
}

func (s *stubChunkRepoForFiles) ListKnowledgeBaseIDsByStorageReference(_ context.Context, _ uint64, ref string) ([]string, error) {
	return s.byRef[ref], nil
}

type stubKBShareForFiles struct {
	shared map[string]bool
}

func (s *stubKBShareForFiles) CheckTenantKBPermission(_ context.Context, kbID string, _ uint64, _ types.TenantRole) (types.OrgMemberRole, bool, error) {
	if s.shared[kbID] {
		return types.OrgRoleViewer, true, nil
	}
	return "", false, nil
}

func sharedCrossTenantFileServeConfig(
	getFile func(ctx context.Context, filePath string) (io.ReadCloser, error),
) fileServeConfig {
	const (
		kbID     = "kb-shared"
		filePath = "local://10008/exports/a.png"
	)
	return fileServeConfig{
		globalFileService: &stubFileService{getFile: getFile},
		tenantService: &stubTenantServiceForFiles{
			tenants: map[uint64]*types.Tenant{
				10008: {ID: 10008},
			},
		},
		storageAccess: appservice.NewStorageAccessAuthorizer(
			&stubKBShareForFiles{shared: map[string]bool{kbID: true}},
			nil,
			&stubKBRepoForFiles{kbs: map[string]*types.KnowledgeBase{
				kbID: {ID: kbID, TenantID: 10008},
			}},
			nil,
			&stubChunkRepoForFiles{byRef: map[string][]string{filePath: {kbID}}},
		),
	}
}

func TestServeFilesFallsBackToGlobalFileService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("STORAGE_TYPE", "local")

	engine := gin.New()
	var requestedPath string
	serveFiles(engine, testFileServeConfig(func(ctx context.Context, filePath string) (io.ReadCloser, error) {
		requestedPath = filePath
		return io.NopCloser(strings.NewReader("fallback-body")), nil
	}))

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
	serveFiles(engine, testFileServeConfig(func(ctx context.Context, filePath string) (io.ReadCloser, error) {
		t.Fatalf("GetFile should not be called for mismatched provider, got %q", filePath)
		return nil, nil
	}))

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
	serveFiles(engine, testFileServeConfig(func(ctx context.Context, filePath string) (io.ReadCloser, error) {
		t.Fatalf("GetFile should not be called for cross-tenant path, got %q", filePath)
		return nil, nil
	}))

	req := httptest.NewRequest(http.MethodGet, "/files?file_path="+url.QueryEscape("local://7/knowledge/secret.pdf"), nil)
	req = req.WithContext(context.WithValue(req.Context(), types.TenantInfoContextKey, &types.Tenant{ID: 42}))

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if got, want := recorder.Code, http.StatusForbidden; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

func TestServeFilesAllowsCrossTenantPathViaSharedKB(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("STORAGE_TYPE", "local")

	engine := gin.New()
	filePath := "local://10008/exports/a.png"
	var requestedPath string
	serveFiles(engine, sharedCrossTenantFileServeConfig(func(ctx context.Context, path string) (io.ReadCloser, error) {
		requestedPath = path
		return io.NopCloser(strings.NewReader("shared-image")), nil
	}))

	req := httptest.NewRequest(http.MethodGet, "/files?file_path="+url.QueryEscape(filePath), nil)
	ctx := context.WithValue(req.Context(), types.TenantInfoContextKey, &types.Tenant{ID: 10005})
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleOwner)
	req = req.WithContext(ctx)

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if got, want := recorder.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d body=%s", got, want, recorder.Body.String())
	}
	if requestedPath != filePath {
		t.Fatalf("requested path = %q, want %q", requestedPath, filePath)
	}
}

func TestServeFilesRejectsPathWithoutTenantSegment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("STORAGE_TYPE", "local")

	engine := gin.New()
	serveFiles(engine, testFileServeConfig(func(ctx context.Context, filePath string) (io.ReadCloser, error) {
		t.Fatalf("GetFile should not be called without tenant segment, got %q", filePath)
		return nil, nil
	}))

	req := httptest.NewRequest(http.MethodGet, "/files?file_path="+url.QueryEscape("local://docs/example.txt"), nil)
	req = req.WithContext(context.WithValue(req.Context(), types.TenantInfoContextKey, &types.Tenant{ID: 42}))

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if got, want := recorder.Code, http.StatusForbidden; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}
