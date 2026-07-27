package handler

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubKnowledgeFolderService struct {
	interfaces.KnowledgeFolderService
	folders       map[string]*types.KnowledgeFolder
	getErr        error
	moveErr       error
	movedID       string
	movedFolderID string
	resolveKBID   string
	resolveReq    *types.ResolveFolderPathsRequest
	resolveResp   *types.ResolveFolderPathsResponse
	resolveErr    error
}

func (s *stubKnowledgeFolderService) ResolveOrCreatePaths(
	_ context.Context, kbID string, req *types.ResolveFolderPathsRequest,
) (*types.ResolveFolderPathsResponse, error) {
	s.resolveKBID = kbID
	copyReq := *req
	copyReq.Paths = append([]string(nil), req.Paths...)
	s.resolveReq = &copyReq
	return s.resolveResp, s.resolveErr
}

func (s *stubKnowledgeFolderService) GetFolder(
	_ context.Context, kbID, folderID string,
) (*types.KnowledgeFolder, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	folder, ok := s.folders[kbID+"/"+folderID]
	if !ok {
		return nil, apprepo.ErrKnowledgeFolderNotFound
	}
	return folder, nil
}

func mustKnowledgeFolderHandler(t *testing.T, svc interfaces.KnowledgeFolderService) *KnowledgeFolderHandler {
	t.Helper()
	h, err := NewKnowledgeFolderHandler(svc)
	require.NoError(t, err)
	return h
}

func newKnowledgeFolderHandlerTestEngine(t *testing.T, svc interfaces.KnowledgeFolderService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), uint64(7))
		c.Next()
	})
	h := mustKnowledgeFolderHandler(t, svc)
	r.POST("/knowledge-bases/:id/folders/resolve-paths", h.ResolveOrCreatePaths)
	r.GET("/knowledge-bases/:id/folders/:folder_id", h.GetFolder)
	return r
}

func TestKnowledgeFolderHandlerResolveOrCreatePathsNormalizesRootAndReturnsMapping(t *testing.T) {
	svc := &stubKnowledgeFolderService{resolveResp: &types.ResolveFolderPathsResponse{Paths: []types.ResolvedFolderPath{
		{RelativePath: "Project/docs", FolderID: "folder-docs"},
	}}}
	r := newKnowledgeFolderHandlerTestEngine(t, svc)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb-1/folders/resolve-paths",
		strings.NewReader(`{"current_folder_id":"__root__","paths":["Project/docs"]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Equal(t, "kb-1", svc.resolveKBID)
	require.NotNil(t, svc.resolveReq)
	require.Equal(t, types.FolderRootID, svc.resolveReq.CurrentFolderID)
	require.Equal(t, []string{"Project/docs"}, svc.resolveReq.Paths)
	require.JSONEq(t, `{"paths":[{"relative_path":"Project/docs","folder_id":"folder-docs"}]}`,
		rec.Body.String())
}

func TestKnowledgeFolderHandlerResolveOrCreatePathsRejectsInvalidJSONAndMapsErrors(t *testing.T) {
	t.Run("invalid JSON", func(t *testing.T) {
		svc := &stubKnowledgeFolderService{}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb-1/folders/resolve-paths",
			strings.NewReader(`{"paths":`))
		req.Header.Set("Content-Type", "application/json")
		newKnowledgeFolderHandlerTestEngine(t, svc).ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())
		require.Nil(t, svc.resolveReq)
	})

	t.Run("service not found", func(t *testing.T) {
		svc := &stubKnowledgeFolderService{resolveErr: apprepo.ErrKnowledgeFolderNotFound}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb-1/folders/resolve-paths",
			strings.NewReader(`{"paths":["Project"]}`))
		req.Header.Set("Content-Type", "application/json")
		newKnowledgeFolderHandlerTestEngine(t, svc).ServeHTTP(rec, req)
		require.Equal(t, http.StatusNotFound, rec.Code, "body=%s", rec.Body.String())
	})
}

func TestNewKnowledgeFolderHandlerRejectsNilService(t *testing.T) {
	h, err := NewKnowledgeFolderHandler(nil)
	require.Nil(t, h)
	require.Error(t, err)
}

func TestKnowledgeFolderHandlerScopesFolderByKBAndFolderID(t *testing.T) {
	svc := &stubKnowledgeFolderService{folders: map[string]*types.KnowledgeFolder{
		"kb-A/folder-1": {ID: "folder-1", KnowledgeBaseID: "kb-A"},
	}}
	r := newKnowledgeFolderHandlerTestEngine(t, svc)

	for _, tc := range []struct {
		name string
		path string
		want int
	}{
		{name: "correct pair", path: "/knowledge-bases/kb-A/folders/folder-1", want: http.StatusOK},
		{name: "folder belongs to another KB", path: "/knowledge-bases/kb-B/folders/folder-1", want: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			require.Equal(t, tc.want, rec.Code, "body=%s", rec.Body.String())
		})
	}
}

func TestKnowledgeFolderHandlerErrorMapping(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid", err: types.ErrInvalidArgument, want: http.StatusBadRequest},
		{name: "duplicate", err: types.ErrFolderAlreadyExists, want: http.StatusConflict},
		{name: "non empty", err: types.ErrFolderNotEmpty, want: http.StatusConflict},
		{name: "wrapped folder not found", err: errors.Join(errors.New("lookup"), apprepo.ErrKnowledgeFolderNotFound), want: http.StatusNotFound},
		{name: "knowledge base not found", err: apprepo.ErrKnowledgeBaseNotFound, want: http.StatusNotFound},
		{name: "knowledge not found is not exposed", err: apprepo.ErrKnowledgeNotFound, want: http.StatusInternalServerError},
		{name: "infrastructure", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &stubKnowledgeFolderService{getErr: tt.err}
			rec := httptest.NewRecorder()
			newKnowledgeFolderHandlerTestEngine(t, svc).ServeHTTP(
				rec,
				httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb-a/folders/folder-b", nil),
			)
			require.Equal(t, tt.want, rec.Code, "body=%s", rec.Body.String())
		})
	}
}

func (s *stubKnowledgeFolderService) MoveKnowledgeToFolder(
	_ context.Context, knowledgeID, folderID string,
) error {
	s.movedID, s.movedFolderID = knowledgeID, folderID
	return s.moveErr
}

func TestMoveKnowledgeToFolderCallsService(t *testing.T) {
	svc := &stubKnowledgeFolderService{}
	h := mustKnowledgeFolderHandler(t, svc)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.PUT("/knowledges/:id/folder", h.MoveKnowledgeToFolder)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/knowledges/k-1/folder", strings.NewReader(`{"folder_id":"folder-1"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Equal(t, "k-1", svc.movedID)
	require.Equal(t, "folder-1", svc.movedFolderID)
}

type createURLFolderServiceStub struct {
	interfaces.KnowledgeService
	folderID  string
	createErr error
}

func (s *createURLFolderServiceStub) CreateKnowledgeFromURL(
	_ context.Context, _ string, _ string, _ string, _ string, _ *bool, _ string, _ []string, _ string,
	folderID string, _ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	s.folderID = folderID
	if s.createErr != nil {
		return nil, s.createErr
	}
	return &types.Knowledge{ID: "knowledge-1"}, nil
}

type createURLFolderKBStub struct {
	interfaces.KnowledgeBaseService
}

func (*createURLFolderKBStub) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: id, TenantID: 7}, nil
}

func TestCreateKnowledgeFromURLPassesFolder(t *testing.T) {
	t.Setenv("SSRF_WHITELIST_EXTRA", "example.com")
	for _, tc := range []struct {
		name, requested, expected string
	}{
		{name: "named folder", requested: "folder-current", expected: "folder-current"},
		{name: "root sentinel", requested: types.FolderRootFilter, expected: types.FolderRootID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serviceStub := &createURLFolderServiceStub{}
			h := &KnowledgeHandler{
				cfg:       &config.Config{},
				kgService: serviceStub,
				kbService: &createURLFolderKBStub{},
			}
			r := gin.New()
			r.Use(middleware.ErrorHandler())
			r.Use(func(c *gin.Context) {
				ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
				c.Request = c.Request.WithContext(ctx)
				c.Set(types.TenantIDContextKey.String(), uint64(7))
				c.Next()
			})
			r.POST("/knowledge-bases/:id/knowledge/url", h.CreateKnowledgeFromURL)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb-1/knowledge/url",
				strings.NewReader(`{"url":"https://example.com/document","folder_id":"`+tc.requested+`"}`))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(rec, req)

			require.Equal(t, http.StatusCreated, rec.Code, "body=%s", rec.Body.String())
			require.Equal(t, tc.expected, serviceStub.folderID)
		})
	}
}

func TestCreateKnowledgeFromURLMapsInvalidFolderToNotFound(t *testing.T) {
	t.Setenv("SSRF_WHITELIST_EXTRA", "example.com")
	serviceStub := &createURLFolderServiceStub{createErr: apprepo.ErrKnowledgeFolderNotFound}
	h := &KnowledgeHandler{
		cfg:       &config.Config{},
		kgService: serviceStub,
		kbService: &createURLFolderKBStub{},
	}
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), uint64(7))
		c.Next()
	})
	r.POST("/knowledge-bases/:id/knowledge/url", h.CreateKnowledgeFromURL)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb-1/knowledge/url",
		strings.NewReader(`{"url":"https://example.com/document","folder_id":"missing"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code, "body=%s", rec.Body.String())
}

type createManualFolderServiceStub struct {
	interfaces.KnowledgeService
	folderID  string
	createErr error
}

func (s *createManualFolderServiceStub) CreateKnowledgeFromManual(
	_ context.Context, _ string, _ *types.ManualKnowledgePayload, _ string,
	folderID string,
) (*types.Knowledge, error) {
	s.folderID = folderID
	if s.createErr != nil {
		return nil, s.createErr
	}
	return &types.Knowledge{ID: "knowledge-1", FolderID: folderID}, nil
}

func TestCreateManualKnowledgePassesFolder(t *testing.T) {
	for _, tc := range []struct {
		name, requested, expected string
	}{
		{name: "named folder", requested: "folder-current", expected: "folder-current"},
		{name: "root sentinel", requested: types.FolderRootFilter, expected: types.FolderRootID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serviceStub := &createManualFolderServiceStub{}
			h := &KnowledgeHandler{
				cfg:       &config.Config{},
				kgService: serviceStub,
				kbService: &createURLFolderKBStub{},
			}
			r := gin.New()
			r.Use(middleware.ErrorHandler())
			r.Use(func(c *gin.Context) {
				ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
				c.Request = c.Request.WithContext(ctx)
				c.Set(types.TenantIDContextKey.String(), uint64(7))
				c.Next()
			})
			r.POST("/knowledge-bases/:id/knowledge/manual", h.CreateManualKnowledge)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb-1/knowledge/manual",
				strings.NewReader(`{"title":"Manual","content":"hello","folder_id":"`+tc.requested+`"}`))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
			require.Equal(t, tc.expected, serviceStub.folderID)
		})
	}
}

func TestCreateManualKnowledgeMapsInvalidFolderToNotFound(t *testing.T) {
	serviceStub := &createManualFolderServiceStub{createErr: apprepo.ErrKnowledgeFolderNotFound}
	h := &KnowledgeHandler{
		cfg:       &config.Config{},
		kgService: serviceStub,
		kbService: &createURLFolderKBStub{},
	}
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), uint64(7))
		c.Next()
	})
	r.POST("/knowledge-bases/:id/knowledge/manual", h.CreateManualKnowledge)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb-1/knowledge/manual",
		strings.NewReader(`{"title":"Manual","content":"hello","folder_id":"missing"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code, "body=%s", rec.Body.String())
}

type knowledgeFolderAwareHandlerServiceStub struct {
	interfaces.KnowledgeService
	fileFolderID string
	listFilter   types.KnowledgeListFilter
	listErr      error
}

func (s *knowledgeFolderAwareHandlerServiceStub) CreateKnowledgeFromFile(
	_ context.Context,
	_ string,
	_ *multipart.FileHeader,
	_ map[string]string,
	_ *bool,
	_ string,
	_ []string,
	_ string,
	folderID string,
	_ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	s.fileFolderID = folderID
	return &types.Knowledge{ID: "knowledge-file", FolderID: folderID}, nil
}

func (s *knowledgeFolderAwareHandlerServiceStub) ListPagedKnowledgeByKnowledgeBaseID(
	_ context.Context, _ string, page *types.Pagination, filter types.KnowledgeListFilter,
) (*types.PageResult, error) {
	s.listFilter = filter
	if s.listErr != nil {
		return nil, s.listErr
	}
	return types.NewPageResult(0, page, []*types.Knowledge{}), nil
}

func newFolderAwareKnowledgeHandlerEngine(
	t *testing.T, service interfaces.KnowledgeService,
) *gin.Engine {
	t.Helper()
	h := &KnowledgeHandler{
		cfg:       &config.Config{},
		kgService: service,
		kbService: &createURLFolderKBStub{},
	}
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleAdmin)
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), uint64(7))
		c.Next()
	})
	r.POST("/knowledge-bases/:id/knowledge/file", h.CreateKnowledgeFromFile)
	r.GET("/knowledge-bases/:id/knowledge", h.ListKnowledge)
	return r
}

func TestCreateKnowledgeFromFilePassesMultipartFolder(t *testing.T) {
	service := &knowledgeFolderAwareHandlerServiceStub{}
	r := newFolderAwareKnowledgeHandlerEngine(t, service)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "document.txt")
	require.NoError(t, err)
	_, err = part.Write([]byte("folder-aware upload"))
	require.NoError(t, err)
	require.NoError(t, writer.WriteField("folder_id", "folder-current"))
	require.NoError(t, writer.Close())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb-1/knowledge/file", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Equal(t, "folder-current", service.fileFolderID)
}

func TestListKnowledgePassesRootNamedAndOmittedFolderFilters(t *testing.T) {
	for _, tc := range []struct {
		name      string
		query     string
		wantSet   bool
		wantValue string
	}{
		{name: "omitted", query: "", wantSet: false},
		{name: "root", query: "?folder_id=" + types.FolderRootFilter, wantSet: true, wantValue: types.FolderRootID},
		{name: "named", query: "?folder_id=folder-current", wantSet: true, wantValue: "folder-current"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &knowledgeFolderAwareHandlerServiceStub{}
			rec := httptest.NewRecorder()
			newFolderAwareKnowledgeHandlerEngine(t, service).ServeHTTP(rec, httptest.NewRequest(
				http.MethodGet, "/knowledge-bases/kb-1/knowledge"+tc.query, nil,
			))
			require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
			require.Equal(t, tc.wantSet, service.listFilter.FolderIDSet)
			require.Equal(t, tc.wantValue, service.listFilter.FolderID)
		})
	}
}

func TestListKnowledgeMapsInvalidFolderToNotFound(t *testing.T) {
	service := &knowledgeFolderAwareHandlerServiceStub{listErr: apprepo.ErrKnowledgeFolderNotFound}
	rec := httptest.NewRecorder()
	newFolderAwareKnowledgeHandlerEngine(t, service).ServeHTTP(rec, httptest.NewRequest(
		http.MethodGet, "/knowledge-bases/kb-1/knowledge?folder_id=missing", nil,
	))
	require.Equal(t, http.StatusNotFound, rec.Code, "body=%s", rec.Body.String())
}
