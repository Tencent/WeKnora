package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type folderHandlerServiceStub struct {
	interfaces.KnowledgeFolderService
	method       string
	kbID         string
	folderID     string
	parentID     string
	keyword      string
	name         string
	page         types.Pagination
	paths        []types.EnsureFolderPath
	knowledgeIDs []string
}

func (s *folderHandlerServiceStub) List(_ context.Context, kbID, parentID, keyword string, page *types.Pagination) (*types.PageResult, error) {
	s.method, s.kbID, s.parentID, s.keyword, s.page = "list", kbID, parentID, keyword, *page
	return &types.PageResult{Total: 1, Page: page.GetPage(), PageSize: page.GetPageSize(), Data: []*types.KnowledgeFolderView{}}, nil
}
func (s *folderHandlerServiceStub) Get(_ context.Context, kbID, folderID string) (*types.KnowledgeFolderView, error) {
	s.method, s.kbID, s.folderID = "get", kbID, folderID
	return &types.KnowledgeFolderView{KnowledgeFolder: types.KnowledgeFolder{ID: folderID, KnowledgeBaseID: kbID}}, nil
}
func (s *folderHandlerServiceStub) Create(_ context.Context, kbID, parentID, name string) (*types.KnowledgeFolder, error) {
	s.method, s.kbID, s.parentID, s.name = "create", kbID, parentID, name
	return &types.KnowledgeFolder{ID: "created", KnowledgeBaseID: kbID, ParentID: parentID, Name: name}, nil
}
func (s *folderHandlerServiceStub) Update(_ context.Context, kbID, folderID string, name, parentID *string) (*types.KnowledgeFolder, error) {
	s.method, s.kbID, s.folderID = "update", kbID, folderID
	if name != nil {
		s.name = *name
	}
	if parentID != nil {
		s.parentID = *parentID
	}
	return &types.KnowledgeFolder{ID: folderID, KnowledgeBaseID: kbID, ParentID: s.parentID, Name: s.name}, nil
}
func (s *folderHandlerServiceStub) Delete(_ context.Context, kbID, folderID string) error {
	s.method, s.kbID, s.folderID = "delete", kbID, folderID
	return nil
}
func (s *folderHandlerServiceStub) EnsurePaths(_ context.Context, kbID, parentID string, paths []types.EnsureFolderPath) ([]types.EnsureFolderPathResult, error) {
	s.method, s.kbID, s.parentID, s.paths = "ensure", kbID, parentID, paths
	return []types.EnsureFolderPathResult{{ClientKey: paths[0].ClientKey, FolderID: "leaf"}}, nil
}
func (s *folderHandlerServiceStub) MoveKnowledge(_ context.Context, kbID string, knowledgeIDs []string, folderID string) error {
	s.method, s.kbID, s.folderID, s.knowledgeIDs = "move", kbID, folderID, knowledgeIDs
	return nil
}

func newFolderHandlerRouter(service interfaces.KnowledgeFolderService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewKnowledgeFolderHandler(service)
	r.GET("/knowledge-bases/:id/folders", h.List)
	r.POST("/knowledge-bases/:id/folders", h.Create)
	r.POST("/knowledge-bases/:id/folders/ensure-paths", h.EnsurePaths)
	r.GET("/knowledge-bases/:id/folders/:folder_id", h.Get)
	r.PUT("/knowledge-bases/:id/folders/:folder_id", h.Update)
	r.DELETE("/knowledge-bases/:id/folders/:folder_id", h.Delete)
	r.PUT("/knowledge-bases/:id/knowledge/folder", h.MoveKnowledge)
	return r
}

func performFolderRequest(t *testing.T, router http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestKnowledgeFolderHandlerContracts(t *testing.T) {
	service := &folderHandlerServiceStub{}
	router := newFolderHandlerRouter(service)

	response := performFolderRequest(t, router, http.MethodGet, "/knowledge-bases/kb-1/folders?parent_id=parent-1&keyword=report&page=3&page_size=25", nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "list", service.method)
	require.Equal(t, "parent-1", service.parentID)
	require.Equal(t, "report", service.keyword)
	require.Equal(t, types.Pagination{Page: 3, PageSize: 25}, service.page)

	response = performFolderRequest(t, router, http.MethodPost, "/knowledge-bases/kb-1/folders", map[string]any{"name": "Reports", "parent_id": "parent-1"})
	require.Equal(t, http.StatusCreated, response.Code)
	require.Equal(t, "create", service.method)
	require.Equal(t, "Reports", service.name)

	response = performFolderRequest(t, router, http.MethodGet, "/knowledge-bases/kb-1/folders/folder-1", nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "get", service.method)
	require.Equal(t, "folder-1", service.folderID)

	response = performFolderRequest(t, router, http.MethodPut, "/knowledge-bases/kb-1/folders/folder-1", map[string]any{"name": "Renamed", "parent_id": "parent-2"})
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "update", service.method)
	require.Equal(t, "Renamed", service.name)
	require.Equal(t, "parent-2", service.parentID)

	response = performFolderRequest(t, router, http.MethodPost, "/knowledge-bases/kb-1/folders/ensure-paths", map[string]any{
		"parent_id": "parent-1", "paths": []map[string]any{{"client_key": "docs", "segments": []string{"2026", "Q3"}}},
	})
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "ensure", service.method)
	require.Len(t, service.paths, 1)
	require.Equal(t, []string{"2026", "Q3"}, service.paths[0].Segments)

	response = performFolderRequest(t, router, http.MethodPut, "/knowledge-bases/kb-1/knowledge/folder", map[string]any{
		"knowledge_ids": []string{"doc-1", "doc-2"}, "folder_id": "folder-2",
	})
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "move", service.method)
	require.Equal(t, []string{"doc-1", "doc-2"}, service.knowledgeIDs)
	require.Equal(t, "folder-2", service.folderID)

	response = performFolderRequest(t, router, http.MethodDelete, "/knowledge-bases/kb-1/folders/folder-1", nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "delete", service.method)
}
