package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appservice "github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubDocumentFolderKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *stubDocumentFolderKBService) GetKnowledgeBaseByID(
	_ context.Context, id string,
) (*types.KnowledgeBase, error) {
	if s.kb != nil {
		return s.kb, nil
	}
	return &types.KnowledgeBase{
		ID:   id,
		Type: types.KnowledgeBaseTypeDocument,
	}, nil
}

type stubDocumentFolderService struct {
	interfaces.DocumentFolderService
	createCalls  int
	deleteCalls  int
	impactTenant uint64
	renameCalls  int
	renamedTo    string
	listTenant   uint64
	submitCalls  int
	submitMode   types.DocumentFolderDeleteMode
	submitErr    error
}

func (s *stubDocumentFolderService) GetDeleteImpact(
	_ context.Context, _ string, tenantID uint64, _ string,
) (*types.DocumentFolderDeleteImpact, error) {
	s.impactTenant = tenantID
	return &types.DocumentFolderDeleteImpact{
		FolderCount:         4,
		DocumentCount:       326,
		ActiveDocumentCount: 2,
	}, nil
}

func (s *stubDocumentFolderService) ListFolders(
	_ context.Context,
	_ string,
	tenantID uint64,
	_ string,
	_ string,
	_ string,
	_ int,
) (*types.DocumentFolderListResponse, error) {
	s.listTenant = tenantID
	return &types.DocumentFolderListResponse{}, nil
}

func (s *stubDocumentFolderService) CreateFolder(
	_ context.Context, kbID string, tenantID uint64, parentID string, name string,
) (*types.DocumentFolder, error) {
	s.createCalls++
	return &types.DocumentFolder{
		ID:              "folder-new",
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		ParentID:        parentID,
		Name:            name,
		Path:            name,
	}, nil
}

func (s *stubDocumentFolderService) RenameFolder(
	_ context.Context, kbID string, id string, newName string,
) (*types.DocumentFolder, error) {
	s.renameCalls++
	s.renamedTo = newName
	return &types.DocumentFolder{
		ID:              id,
		KnowledgeBaseID: kbID,
		Name:            newName,
		Path:            newName,
	}, nil
}

func (s *stubDocumentFolderService) DeleteFolder(
	_ context.Context, _ string, _ string,
) error {
	s.deleteCalls++
	return nil
}

func (s *stubDocumentFolderService) SubmitDeleteFolderTree(
	_ context.Context,
	_ string,
	_ uint64,
	_ string,
	mode types.DocumentFolderDeleteMode,
) (string, error) {
	s.submitCalls++
	s.submitMode = mode
	if s.submitErr != nil {
		return "", s.submitErr
	}
	return "folder-delete-task", nil
}

func newDocumentFolderHandlerTestRouter(folderService interfaces.DocumentFolderService) *gin.Engine {
	return newDocumentFolderHandlerTestRouterWithKB(folderService, nil)
}

func newDocumentFolderHandlerTestRouterWithKB(
	folderService interfaces.DocumentFolderService,
	kb *types.KnowledgeBase,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewDocumentFolderHandler(
		&config.Config{KnowledgeBase: &config.KnowledgeBaseConfig{DocumentFoldersEnabled: true}},
		folderService,
		&stubDocumentFolderKBService{kb: kb},
	)
	router.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(99))
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
		c.Request = c.Request.WithContext(ctx)
	})
	router.GET("/knowledgebase/:kb_id/document-folders", handler.ListFolders)
	router.POST("/knowledgebase/:kb_id/document-folders", handler.CreateFolder)
	router.PUT("/knowledgebase/:kb_id/document-folders/:folder_id", handler.UpdateFolder)
	router.GET("/knowledgebase/:kb_id/document-folders/:folder_id/delete-impact", handler.GetDeleteImpact)
	router.DELETE("/knowledgebase/:kb_id/document-folders/:folder_id", handler.DeleteFolder)
	return router
}

func TestDocumentFolderHandler_GetDeleteImpactReturnsSubtreeSummary(t *testing.T) {
	service := &stubDocumentFolderService{}
	request := httptest.NewRequest(
		http.MethodGet,
		"/knowledgebase/kb-1/document-folders/folder-1/delete-impact",
		nil,
	)
	response := httptest.NewRecorder()

	newDocumentFolderHandlerTestRouter(service).ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, uint64(1), service.impactTenant)
	assert.JSONEq(t, `{
		"folder_count": 4,
		"document_count": 326,
		"active_document_count": 2
	}`, response.Body.String())
}

func TestDocumentFolderHandler_DeleteFolderSubmitsExplicitMode(t *testing.T) {
	for _, mode := range []types.DocumentFolderDeleteMode{
		types.DocumentFolderDeleteModeKeepDocuments,
		types.DocumentFolderDeleteModeDeleteAll,
	} {
		t.Run(string(mode), func(t *testing.T) {
			service := &stubDocumentFolderService{}
			request := httptest.NewRequest(
				http.MethodDelete,
				"/knowledgebase/kb-1/document-folders/folder-1?mode="+string(mode),
				nil,
			)
			response := httptest.NewRecorder()

			newDocumentFolderHandlerTestRouter(service).ServeHTTP(response, request)

			require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
			assert.JSONEq(t, `{"task_id":"folder-delete-task"}`, response.Body.String())
			assert.Equal(t, 1, service.submitCalls)
			assert.Equal(t, mode, service.submitMode)
			assert.Zero(t, service.deleteCalls)
		})
	}
}

func TestDocumentFolderHandler_DeleteFolderWithoutModeKeepsLegacyNoContentContract(t *testing.T) {
	service := &stubDocumentFolderService{}
	request := httptest.NewRequest(
		http.MethodDelete,
		"/knowledgebase/kb-1/document-folders/folder-1",
		nil,
	)
	response := httptest.NewRecorder()

	newDocumentFolderHandlerTestRouter(service).ServeHTTP(response, request)

	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	assert.Equal(t, 1, service.deleteCalls)
	assert.Zero(t, service.submitCalls)
}

func TestDocumentFolderHandler_DeleteFolderRejectsUnknownMode(t *testing.T) {
	service := &stubDocumentFolderService{}
	request := httptest.NewRequest(
		http.MethodDelete,
		"/knowledgebase/kb-1/document-folders/folder-1?mode=surprise",
		nil,
	)
	response := httptest.NewRecorder()

	newDocumentFolderHandlerTestRouter(service).ServeHTTP(response, request)

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Zero(t, service.submitCalls)
	assert.Zero(t, service.deleteCalls)
}

func TestDocumentFolderHandler_DeleteFolderReportsActiveParsingConflict(t *testing.T) {
	service := &stubDocumentFolderService{submitErr: appservice.ErrFolderDocumentsProcessing}
	request := httptest.NewRequest(
		http.MethodDelete,
		"/knowledgebase/kb-1/document-folders/folder-1?mode=keep_documents",
		nil,
	)
	response := httptest.NewRecorder()

	newDocumentFolderHandlerTestRouter(service).ServeHTTP(response, request)

	assert.Equal(t, http.StatusConflict, response.Code)
	assert.Equal(t, 1, service.submitCalls)
}

func TestDocumentFolderHandler_ListUsesEffectiveTenantFromRequestContext(t *testing.T) {
	service := &stubDocumentFolderService{}
	router := newDocumentFolderHandlerTestRouter(service)
	request := httptest.NewRequest(
		http.MethodGet,
		"/knowledgebase/kb-1/document-folders",
		nil,
	)
	request = request.WithContext(context.WithValue(
		request.Context(),
		types.TenantIDContextKey,
		uint64(42),
	))
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, uint64(1), service.listTenant)
}

func TestDocumentFolderHandler_UpdateFolderRejectsMoveFields(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "parent_id", body: `{"name":"Renamed","parent_id":""}`},
		{name: "move_parent", body: `{"name":"Renamed","move_parent":false}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &stubDocumentFolderService{}
			request := httptest.NewRequest(
				http.MethodPut,
				"/knowledgebase/kb-1/document-folders/folder-1",
				strings.NewReader(tt.body),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			newDocumentFolderHandlerTestRouter(service).ServeHTTP(response, request)

			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Contains(t, response.Body.String(), "moving folders is not supported")
			assert.Zero(t, service.renameCalls)
		})
	}
}

func TestDocumentFolderHandler_UpdateFolderRenamesFolder(t *testing.T) {
	service := &stubDocumentFolderService{}
	request := httptest.NewRequest(
		http.MethodPut,
		"/knowledgebase/kb-1/document-folders/folder-1",
		strings.NewReader(`{"name":"Renamed"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	newDocumentFolderHandlerTestRouter(service).ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, 1, service.renameCalls)
	assert.Equal(t, "Renamed", service.renamedTo)
	assert.Contains(t, response.Body.String(), `"name":"Renamed"`)
}

func TestDocumentFolderHandler_RejectsWikiKnowledgeBaseForEveryFolderOperation(t *testing.T) {
	wikiKB := &types.KnowledgeBase{
		ID:       "kb-wiki",
		TenantID: 1,
		Type:     types.KnowledgeBaseTypeDocument,
		IndexingStrategy: types.IndexingStrategy{
			WikiEnabled: true,
		},
	}
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "list",
			method: http.MethodGet,
			path:   "/knowledgebase/kb-wiki/document-folders",
		},
		{
			name:   "create",
			method: http.MethodPost,
			path:   "/knowledgebase/kb-wiki/document-folders",
			body:   `{"name":"Hidden"}`,
		},
		{
			name:   "rename",
			method: http.MethodPut,
			path:   "/knowledgebase/kb-wiki/document-folders/folder-1",
			body:   `{"name":"Hidden"}`,
		},
		{
			name:   "delete",
			method: http.MethodDelete,
			path:   "/knowledgebase/kb-wiki/document-folders/folder-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &stubDocumentFolderService{}
			request := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			response := httptest.NewRecorder()

			newDocumentFolderHandlerTestRouterWithKB(service, wikiKB).ServeHTTP(response, request)

			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Contains(t, response.Body.String(), "Document folders are not supported")
			assert.Zero(t, service.listTenant)
			assert.Zero(t, service.createCalls)
			assert.Zero(t, service.renameCalls)
			assert.Zero(t, service.deleteCalls)
		})
	}
}
