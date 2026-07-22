package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type knowledgeFolderHandlerServiceStub struct {
	interfaces.KnowledgeFolderService
	err               error
	effectiveTenantID uint64
	kbID              string
	folderID          string
	createRequest     *types.KnowledgeFolderCreateRequest
	updateRequest     *types.KnowledgeFolderUpdateRequest
	page              *types.Pagination
	parentID          string
}

func (s *knowledgeFolderHandlerServiceStub) captureContext(ctx context.Context) {
	s.effectiveTenantID, _ = types.TenantIDFromContext(ctx)
}

func (s *knowledgeFolderHandlerServiceStub) CreateFolder(
	ctx context.Context,
	kbID string,
	req *types.KnowledgeFolderCreateRequest,
) (*types.KnowledgeFolder, error) {
	s.captureContext(ctx)
	s.kbID = kbID
	requestCopy := *req
	s.createRequest = &requestCopy
	if s.err != nil {
		return nil, s.err
	}
	return &types.KnowledgeFolder{ID: "created", KnowledgeBaseID: kbID, Name: req.Name}, nil
}

func (s *knowledgeFolderHandlerServiceStub) GetFolder(
	ctx context.Context,
	kbID string,
	folderID string,
) (*types.KnowledgeFolderWithStats, error) {
	s.captureContext(ctx)
	s.kbID = kbID
	s.folderID = folderID
	if s.err != nil {
		return nil, s.err
	}
	return &types.KnowledgeFolderWithStats{
		KnowledgeFolder: types.KnowledgeFolder{ID: folderID, KnowledgeBaseID: kbID},
	}, nil
}

func (s *knowledgeFolderHandlerServiceStub) ListFolders(
	ctx context.Context,
	kbID string,
	parentID string,
	page *types.Pagination,
) (*types.PageResult, error) {
	s.captureContext(ctx)
	s.kbID = kbID
	s.parentID = parentID
	pageCopy := *page
	s.page = &pageCopy
	if s.err != nil {
		return nil, s.err
	}
	return types.NewPageResult(0, page, []*types.KnowledgeFolderWithStats{}), nil
}

func (s *knowledgeFolderHandlerServiceStub) UpdateFolder(
	ctx context.Context,
	kbID string,
	folderID string,
	req *types.KnowledgeFolderUpdateRequest,
) (*types.KnowledgeFolder, error) {
	s.captureContext(ctx)
	s.kbID = kbID
	s.folderID = folderID
	requestCopy := *req
	s.updateRequest = &requestCopy
	if s.err != nil {
		return nil, s.err
	}
	return &types.KnowledgeFolder{ID: folderID, KnowledgeBaseID: kbID}, nil
}

func (s *knowledgeFolderHandlerServiceStub) DeleteFolder(
	ctx context.Context,
	kbID string,
	folderID string,
) error {
	s.captureContext(ctx)
	s.kbID = kbID
	s.folderID = folderID
	return s.err
}

func (s *knowledgeFolderHandlerServiceStub) GetBreadcrumb(
	ctx context.Context,
	kbID string,
	folderID string,
) ([]*types.KnowledgeFolder, error) {
	s.captureContext(ctx)
	s.kbID = kbID
	s.folderID = folderID
	if s.err != nil {
		return nil, s.err
	}
	return []*types.KnowledgeFolder{{ID: folderID}}, nil
}

func newKnowledgeFolderHandlerTestEngine(
	serviceStub interfaces.KnowledgeFolderService,
	effectiveTenantID uint64,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.ErrorHandler())
	engine.Use(func(c *gin.Context) {
		ctx := context.WithValue(
			c.Request.Context(),
			types.TenantIDContextKey,
			effectiveTenantID,
		)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})

	handler := NewKnowledgeFolderHandler(serviceStub)
	folders := engine.Group("/knowledge-bases/:id/folders")
	folders.GET("", handler.ListFolders)
	folders.POST("", handler.CreateFolder)
	folders.GET("/:folder_id", handler.GetFolder)
	folders.GET("/:folder_id/breadcrumb", handler.GetBreadcrumb)
	folders.PATCH("/:folder_id", handler.UpdateFolder)
	folders.DELETE("/:folder_id", handler.DeleteFolder)
	return engine
}

func TestKnowledgeFolderHandlerUsesEffectiveTenantContextAndPagination(t *testing.T) {
	serviceStub := &knowledgeFolderHandlerServiceStub{}
	engine := newKnowledgeFolderHandlerTestEngine(serviceStub, 99)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/knowledge-bases/kb-shared/folders?parent_id=parent&page=2&page_size=30",
		nil,
	)
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, uint64(99), serviceStub.effectiveTenantID)
	assert.Equal(t, "kb-shared", serviceStub.kbID)
	assert.Equal(t, "parent", serviceStub.parentID)
	require.NotNil(t, serviceStub.page)
	assert.Equal(t, 2, serviceStub.page.Page)
	assert.Equal(t, 30, serviceStub.page.PageSize)
}

func TestKnowledgeFolderHandlerRequestDTOCannotOverrideInternalFields(t *testing.T) {
	serviceStub := &knowledgeFolderHandlerServiceStub{}
	engine := newKnowledgeFolderHandlerTestEngine(serviceStub, 1)

	createBody := []byte(`{
		"id":"attacker-id",
		"tenant_id":999,
		"knowledge_base_id":"other-kb",
		"parent_id":"parent",
		"name":"Reports",
		"path":"/attacker/",
		"depth":32,
		"sort_order":7,
		"deleted_at":"2026-07-20T00:00:00Z"
	}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/folders",
		bytes.NewReader(createBody),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	assert.Equal(t, "kb-1", serviceStub.kbID)
	require.NotNil(t, serviceStub.createRequest)
	assert.Equal(t, "parent", serviceStub.createRequest.ParentID)
	assert.Equal(t, "Reports", serviceStub.createRequest.Name)
	assert.Equal(t, 7, serviceStub.createRequest.SortOrder)

	updateBody := []byte(`{
		"id":"attacker-id",
		"tenant_id":999,
		"knowledge_base_id":"other-kb",
		"parent_id":"",
		"name":"Renamed",
		"path":"/attacker/",
		"depth":1,
		"sort_order":0,
		"deleted_at":"2026-07-20T00:00:00Z"
	}`)
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(
		http.MethodPatch,
		"/knowledge-bases/kb-1/folders/folder-1",
		bytes.NewReader(updateBody),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, "kb-1", serviceStub.kbID)
	assert.Equal(t, "folder-1", serviceStub.folderID)
	require.NotNil(t, serviceStub.updateRequest)
	require.NotNil(t, serviceStub.updateRequest.ParentID)
	require.NotNil(t, serviceStub.updateRequest.Name)
	require.NotNil(t, serviceStub.updateRequest.SortOrder)
	assert.Equal(t, "", *serviceStub.updateRequest.ParentID)
	assert.Equal(t, "Renamed", *serviceStub.updateRequest.Name)
	assert.Equal(t, 0, *serviceStub.updateRequest.SortOrder)
}

func TestKnowledgeFolderHandlerMapsDomainErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid argument", err: service.ErrKnowledgeFolderInvalidArgument, wantStatus: http.StatusBadRequest},
		{name: "invalid name", err: service.ErrKnowledgeFolderInvalidName, wantStatus: http.StatusBadRequest},
		{name: "not found", err: service.ErrKnowledgeFolderNotFound, wantStatus: http.StatusNotFound},
		{name: "duplicate", err: service.ErrKnowledgeFolderConflict, wantStatus: http.StatusConflict},
		{name: "not empty", err: service.ErrKnowledgeFolderNotEmpty, wantStatus: http.StatusConflict},
		{name: "cycle", err: service.ErrKnowledgeFolderCycle, wantStatus: http.StatusConflict},
		{name: "depth", err: service.ErrKnowledgeFolderDepthExceeded, wantStatus: http.StatusConflict},
		{name: "integrity", err: service.ErrKnowledgeFolderDataIntegrity, wantStatus: http.StatusInternalServerError},
		{name: "unsupported database", err: service.ErrKnowledgeFolderUnsupportedDB, wantStatus: http.StatusInternalServerError},
		{name: "internal", err: service.ErrKnowledgeFolderInternal, wantStatus: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceStub := &knowledgeFolderHandlerServiceStub{err: tt.err}
			engine := newKnowledgeFolderHandlerTestEngine(serviceStub, 1)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodGet,
				"/knowledge-bases/kb-1/folders/folder-1",
				nil,
			)
			engine.ServeHTTP(recorder, request)
			assert.Equal(t, tt.wantStatus, recorder.Code, recorder.Body.String())
			if tt.wantStatus == http.StatusInternalServerError {
				assert.NotContains(t, recorder.Body.String(), tt.err.Error())
			}
		})
	}
}

func TestKnowledgeFolderHandlerRejectsInvalidJSONAndPagination(t *testing.T) {
	serviceStub := &knowledgeFolderHandlerServiceStub{}
	engine := newKnowledgeFolderHandlerTestEngine(serviceStub, 1)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/folders",
		bytes.NewBufferString(`{"name":`),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(
		http.MethodGet,
		"/knowledge-bases/kb-1/folders?page=0",
		nil,
	)
	engine.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
}

func TestKnowledgeFolderHandlerDeleteReturnsNoContent(t *testing.T) {
	serviceStub := &knowledgeFolderHandlerServiceStub{}
	engine := newKnowledgeFolderHandlerTestEngine(serviceStub, 1)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodDelete,
		"/knowledge-bases/kb-1/folders/folder-1",
		nil,
	)
	engine.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
	assert.Empty(t, recorder.Body.String())
}
