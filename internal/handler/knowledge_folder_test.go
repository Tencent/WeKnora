package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type knowledgeFolderHandlerServiceStub struct {
	interfaces.KnowledgeFolderService
	err                 error
	effectiveTenantID   uint64
	kbID                string
	folderID            string
	createRequest       *types.KnowledgeFolderCreateRequest
	updateRequest       *types.KnowledgeFolderUpdateRequest
	page                *types.Pagination
	parentID            string
	createResult        *types.KnowledgeFolder
	createResultSet     bool
	getResult           *types.KnowledgeFolderWithStats
	getResultSet        bool
	listResult          *types.PageResult
	listResultSet       bool
	updateResult        *types.KnowledgeFolder
	updateResultSet     bool
	breadcrumbResult    []*types.KnowledgeFolder
	breadcrumbResultSet bool
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
	if s.createResultSet || s.createResult != nil {
		return s.createResult, nil
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
	if s.getResultSet || s.getResult != nil {
		return s.getResult, nil
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
	if s.listResultSet || s.listResult != nil {
		return s.listResult, nil
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
	if s.updateResultSet || s.updateResult != nil {
		return s.updateResult, nil
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
	if s.breadcrumbResultSet || s.breadcrumbResult != nil {
		return s.breadcrumbResult, nil
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

func TestKnowledgeFolderHandlerListResponseUsesStatsDTO(t *testing.T) {
	createdAt := time.Date(2026, time.July, 21, 10, 0, 0, 0, time.UTC)
	first := &types.KnowledgeFolderWithStats{
		KnowledgeFolder: types.KnowledgeFolder{
			ID:              "folder-a",
			TenantID:        51,
			KnowledgeBaseID: "kb-1",
			ParentID:        "",
			Name:            "A",
			Path:            "/folder-a/",
			Depth:           1,
			SortOrder:       1,
			CreatedAt:       createdAt,
			UpdatedAt:       createdAt.Add(time.Hour),
		},
		KnowledgeCount: 0,
		HasChildren:    false,
	}
	second := &types.KnowledgeFolderWithStats{
		KnowledgeFolder: types.KnowledgeFolder{
			ID:              "folder-b",
			TenantID:        51,
			KnowledgeBaseID: "kb-1",
			ParentID:        "",
			Name:            "B",
			Path:            "/folder-b/",
			Depth:           1,
			SortOrder:       2,
			CreatedAt:       createdAt,
			UpdatedAt:       createdAt.Add(2 * time.Hour),
		},
		KnowledgeCount: 9,
		HasChildren:    true,
	}
	firstBefore := *first
	secondBefore := *second
	originalData := []*types.KnowledgeFolderWithStats{first, second}
	pageResult := &types.PageResult{Total: 41, Page: 2, PageSize: 30, Data: originalData}
	serviceStub := &knowledgeFolderHandlerServiceStub{listResult: pageResult}
	engine := newKnowledgeFolderHandlerTestEngine(serviceStub, 51)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/knowledge-bases/kb-1/folders?page=2&page_size=30",
		nil,
	)
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	data := decodeKnowledgeFolderSuccessData(t, recorder)
	var page struct {
		Total    int64                        `json:"total"`
		Page     int                          `json:"page"`
		PageSize int                          `json:"page_size"`
		Data     []map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(data, &page))
	require.Equal(t, int64(41), page.Total)
	require.Equal(t, 2, page.Page)
	require.Equal(t, 30, page.PageSize)
	require.Len(t, page.Data, 2)

	expectedKeys := knowledgeFolderHandlerKeySet(
		"id",
		"parent_id",
		"name",
		"depth",
		"sort_order",
		"created_at",
		"updated_at",
		"knowledge_count",
		"has_children",
	)
	for _, item := range page.Data {
		requireKnowledgeFolderHandlerExactKeys(t, item, expectedKeys)
	}
	require.Equal(t, "folder-a", decodeKnowledgeFolderJSONString(t, page.Data[0]["id"]))
	require.Equal(t, "folder-b", decodeKnowledgeFolderJSONString(t, page.Data[1]["id"]))
	require.Equal(t, "", decodeKnowledgeFolderJSONString(t, page.Data[0]["parent_id"]))
	var knowledgeCount int64
	require.NoError(t, json.Unmarshal(page.Data[0]["knowledge_count"], &knowledgeCount))
	require.Zero(t, knowledgeCount)
	var hasChildren bool
	require.NoError(t, json.Unmarshal(page.Data[0]["has_children"], &hasChildren))
	require.False(t, hasChildren)

	unchangedData, ok := pageResult.Data.([]*types.KnowledgeFolderWithStats)
	require.True(t, ok)
	require.Len(t, unchangedData, 2)
	require.Same(t, first, unchangedData[0])
	require.Same(t, second, unchangedData[1])
	require.Equal(t, int64(41), pageResult.Total)
	require.Equal(t, 2, pageResult.Page)
	require.Equal(t, 30, pageResult.PageSize)
	require.Equal(t, firstBefore, *first)
	require.Equal(t, secondBefore, *second)
}

func TestKnowledgeFolderHandlerListEmptyDataIsArray(t *testing.T) {
	var typedNil []*types.KnowledgeFolderWithStats
	tests := []struct {
		name string
		data interface{}
	}{
		{name: "untyped nil", data: nil},
		{name: "typed nil", data: typedNil},
		{name: "empty", data: []*types.KnowledgeFolderWithStats{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceStub := &knowledgeFolderHandlerServiceStub{
				listResult: &types.PageResult{Total: 0, Page: 1, PageSize: 20, Data: tt.data},
			}
			engine := newKnowledgeFolderHandlerTestEngine(serviceStub, 1)

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb-1/folders", nil)
			engine.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			data := decodeKnowledgeFolderSuccessData(t, recorder)
			var page struct {
				Data json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(data, &page))
			require.Equal(t, "[]", string(page.Data))
			var items []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(page.Data, &items))
			require.NotNil(t, items)
			require.Empty(t, items)
		})
	}
}

func TestKnowledgeFolderHandlerCreateResponseUsesBaseDTO(t *testing.T) {
	createdAt := time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC)
	serviceStub := &knowledgeFolderHandlerServiceStub{
		createResult: &types.KnowledgeFolder{
			ID:              "folder-created",
			TenantID:        52,
			KnowledgeBaseID: "kb-1",
			ParentID:        "",
			Name:            "Created",
			Path:            "/folder-created/",
			Depth:           1,
			SortOrder:       3,
			CreatedAt:       createdAt,
			UpdatedAt:       createdAt,
		},
	}
	engine := newKnowledgeFolderHandlerTestEngine(serviceStub, 52)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/folders",
		bytes.NewBufferString(`{"name":"Created"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	item := decodeKnowledgeFolderJSONObject(t, decodeKnowledgeFolderSuccessData(t, recorder))
	requireKnowledgeFolderHandlerExactKeys(t, item, knowledgeFolderHandlerBaseKeys())
	require.Contains(t, item, "parent_id")
	require.Equal(t, "", decodeKnowledgeFolderJSONString(t, item["parent_id"]))
	require.NotContains(t, item, "knowledge_count")
	require.NotContains(t, item, "has_children")
}

func TestKnowledgeFolderHandlerGetResponseUsesStatsDTO(t *testing.T) {
	serviceStub := &knowledgeFolderHandlerServiceStub{
		getResult: &types.KnowledgeFolderWithStats{
			KnowledgeFolder: types.KnowledgeFolder{
				ID:              "folder-1",
				TenantID:        53,
				KnowledgeBaseID: "kb-1",
				ParentID:        "parent-1",
				Name:            "Folder",
				Path:            "/parent-1/folder-1/",
				Depth:           2,
				SortOrder:       4,
			},
			KnowledgeCount: 17,
			HasChildren:    true,
		},
	}
	engine := newKnowledgeFolderHandlerTestEngine(serviceStub, 53)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb-1/folders/folder-1", nil)
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	item := decodeKnowledgeFolderJSONObject(t, decodeKnowledgeFolderSuccessData(t, recorder))
	requireKnowledgeFolderHandlerExactKeys(t, item, knowledgeFolderHandlerKeySet(
		"id",
		"parent_id",
		"name",
		"depth",
		"sort_order",
		"created_at",
		"updated_at",
		"knowledge_count",
		"has_children",
	))
	var knowledgeCount int64
	require.NoError(t, json.Unmarshal(item["knowledge_count"], &knowledgeCount))
	require.Equal(t, int64(17), knowledgeCount)
	var hasChildren bool
	require.NoError(t, json.Unmarshal(item["has_children"], &hasChildren))
	require.True(t, hasChildren)
}

func TestKnowledgeFolderHandlerUpdateResponseUsesBaseDTO(t *testing.T) {
	serviceStub := &knowledgeFolderHandlerServiceStub{
		updateResult: &types.KnowledgeFolder{
			ID:              "folder-1",
			TenantID:        54,
			KnowledgeBaseID: "kb-1",
			ParentID:        "",
			Name:            "Renamed",
			Path:            "/folder-1/",
			Depth:           1,
			SortOrder:       5,
		},
	}
	engine := newKnowledgeFolderHandlerTestEngine(serviceStub, 54)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPatch,
		"/knowledge-bases/kb-1/folders/folder-1",
		bytes.NewBufferString(`{"name":"Renamed"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	item := decodeKnowledgeFolderJSONObject(t, decodeKnowledgeFolderSuccessData(t, recorder))
	requireKnowledgeFolderHandlerExactKeys(t, item, knowledgeFolderHandlerBaseKeys())
	require.NotContains(t, item, "knowledge_count")
	require.NotContains(t, item, "has_children")
}

func TestKnowledgeFolderHandlerBreadcrumbResponseUsesMinimalDTOAndPreservesOrder(t *testing.T) {
	serviceStub := &knowledgeFolderHandlerServiceStub{
		breadcrumbResult: []*types.KnowledgeFolder{
			{
				ID:              "folder-parent",
				TenantID:        55,
				KnowledgeBaseID: "kb-1",
				ParentID:        "",
				Name:            "Parent",
				Path:            "/folder-parent/",
				Depth:           1,
				SortOrder:       1,
			},
			{
				ID:              "folder-child",
				TenantID:        55,
				KnowledgeBaseID: "kb-1",
				ParentID:        "folder-parent",
				Name:            "Child",
				Path:            "/folder-parent/folder-child/",
				Depth:           2,
				SortOrder:       2,
			},
		},
	}
	engine := newKnowledgeFolderHandlerTestEngine(serviceStub, 55)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/knowledge-bases/kb-1/folders/folder-child/breadcrumb",
		nil,
	)
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	data := decodeKnowledgeFolderSuccessData(t, recorder)
	var items []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &items))
	require.Len(t, items, 2)
	expectedKeys := knowledgeFolderHandlerKeySet("id", "parent_id", "name", "depth")
	for _, item := range items {
		requireKnowledgeFolderHandlerExactKeys(t, item, expectedKeys)
		for _, excludedKey := range []string{
			"sort_order",
			"created_at",
			"updated_at",
			"knowledge_count",
			"has_children",
		} {
			require.NotContains(t, item, excludedKey)
		}
	}
	require.Equal(t, "folder-parent", decodeKnowledgeFolderJSONString(t, items[0]["id"]))
	require.Equal(t, "folder-child", decodeKnowledgeFolderJSONString(t, items[1]["id"]))
}

func TestKnowledgeFolderHandlerBreadcrumbEmptyDataIsArray(t *testing.T) {
	var nilBreadcrumb []*types.KnowledgeFolder
	tests := []struct {
		name    string
		folders []*types.KnowledgeFolder
	}{
		{name: "nil", folders: nilBreadcrumb},
		{name: "empty", folders: []*types.KnowledgeFolder{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceStub := &knowledgeFolderHandlerServiceStub{
				breadcrumbResult:    tt.folders,
				breadcrumbResultSet: true,
			}
			engine := newKnowledgeFolderHandlerTestEngine(serviceStub, 1)

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodGet,
				"/knowledge-bases/kb-1/folders/folder-1/breadcrumb",
				nil,
			)
			engine.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			data := decodeKnowledgeFolderSuccessData(t, recorder)
			require.Equal(t, "[]", string(data))
			var items []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(data, &items))
			require.NotNil(t, items)
			require.Empty(t, items)
		})
	}
}

func TestKnowledgeFolderHandlerCreateNilResultFailsClosed(t *testing.T) {
	serviceStub := &knowledgeFolderHandlerServiceStub{createResultSet: true}
	engine := newKnowledgeFolderHandlerTestEngine(serviceStub, 1)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/folders",
		bytes.NewBufferString(`{"name":"Folder"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	requireKnowledgeFolderHandlerDataIntegrityFailure(t, recorder)
}

func TestKnowledgeFolderHandlerGetNilResultFailsClosed(t *testing.T) {
	serviceStub := &knowledgeFolderHandlerServiceStub{getResultSet: true}
	engine := newKnowledgeFolderHandlerTestEngine(serviceStub, 1)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb-1/folders/folder-1", nil)
	engine.ServeHTTP(recorder, request)

	requireKnowledgeFolderHandlerDataIntegrityFailure(t, recorder)
}

func TestKnowledgeFolderHandlerUpdateNilResultFailsClosed(t *testing.T) {
	serviceStub := &knowledgeFolderHandlerServiceStub{updateResultSet: true}
	engine := newKnowledgeFolderHandlerTestEngine(serviceStub, 1)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPatch,
		"/knowledge-bases/kb-1/folders/folder-1",
		bytes.NewBufferString(`{"name":"Renamed"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	requireKnowledgeFolderHandlerDataIntegrityFailure(t, recorder)
}

func TestKnowledgeFolderHandlerListNilPageResultFailsClosed(t *testing.T) {
	serviceStub := &knowledgeFolderHandlerServiceStub{listResultSet: true}
	engine := newKnowledgeFolderHandlerTestEngine(serviceStub, 1)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb-1/folders", nil)
	engine.ServeHTTP(recorder, request)

	requireKnowledgeFolderHandlerDataIntegrityFailure(t, recorder)
}

func TestKnowledgeFolderHandlerListUnexpectedDataTypeFailsClosed(t *testing.T) {
	serviceStub := &knowledgeFolderHandlerServiceStub{
		listResult: &types.PageResult{
			Total:    1,
			Page:     1,
			PageSize: 20,
			Data: []*types.KnowledgeFolder{
				{ID: "internal-folder", TenantID: 61, KnowledgeBaseID: "kb-1", Path: "/internal-folder/"},
			},
		},
	}
	engine := newKnowledgeFolderHandlerTestEngine(serviceStub, 61)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb-1/folders", nil)
	engine.ServeHTTP(recorder, request)

	requireKnowledgeFolderHandlerDataIntegrityFailure(t, recorder)
}

func TestKnowledgeFolderHandlerListNilElementFailsClosed(t *testing.T) {
	validFolder := &types.KnowledgeFolderWithStats{
		KnowledgeFolder: types.KnowledgeFolder{
			ID:              "valid-folder",
			TenantID:        62,
			KnowledgeBaseID: "kb-1",
			ParentID:        "",
			Name:            "Valid",
			Path:            "/valid-folder/",
			Depth:           1,
		},
	}
	folders := []*types.KnowledgeFolderWithStats{validFolder, nil}
	pageResult := &types.PageResult{Total: 2, Page: 1, PageSize: 20, Data: folders}
	serviceStub := &knowledgeFolderHandlerServiceStub{listResult: pageResult}
	engine := newKnowledgeFolderHandlerTestEngine(serviceStub, 62)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb-1/folders", nil)
	engine.ServeHTTP(recorder, request)

	requireKnowledgeFolderHandlerDataIntegrityFailure(t, recorder)
	unchangedFolders, ok := pageResult.Data.([]*types.KnowledgeFolderWithStats)
	require.True(t, ok)
	require.Len(t, unchangedFolders, 2)
	require.Same(t, validFolder, unchangedFolders[0])
	require.Nil(t, unchangedFolders[1])
}

func TestKnowledgeFolderHandlerBreadcrumbNilElementFailsClosed(t *testing.T) {
	validParent := &types.KnowledgeFolder{
		ID:              "valid-parent",
		TenantID:        63,
		KnowledgeBaseID: "kb-1",
		ParentID:        "",
		Name:            "Valid parent",
		Path:            "/valid-parent/",
		Depth:           1,
	}
	folders := []*types.KnowledgeFolder{validParent, nil}
	serviceStub := &knowledgeFolderHandlerServiceStub{breadcrumbResult: folders}
	engine := newKnowledgeFolderHandlerTestEngine(serviceStub, 63)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/knowledge-bases/kb-1/folders/folder-1/breadcrumb",
		nil,
	)
	engine.ServeHTTP(recorder, request)

	requireKnowledgeFolderHandlerDataIntegrityFailure(t, recorder)
	require.Len(t, folders, 2)
	require.Same(t, validParent, folders[0])
	require.Nil(t, folders[1])
}

func requireKnowledgeFolderHandlerDataIntegrityFailure(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
) {
	t.Helper()
	require.Equal(t, http.StatusInternalServerError, recorder.Code, recorder.Body.String())
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(
		t,
		knowledgeFolderHandlerKeySet("success", "error"),
		knowledgeFolderHandlerRawKeySet(envelope),
	)
	require.NotContains(t, envelope, "data")
	var success bool
	require.NoError(t, json.Unmarshal(envelope["success"], &success))
	require.False(t, success)

	var errorEnvelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(envelope["error"], &errorEnvelope))
	require.Equal(
		t,
		knowledgeFolderHandlerKeySet("code", "message", "details"),
		knowledgeFolderHandlerRawKeySet(errorEnvelope),
	)
	for _, internalKey := range []string{"tenant_id", "knowledge_base_id", "path", "deleted_at"} {
		require.NotContains(t, envelope, internalKey)
		require.NotContains(t, errorEnvelope, internalKey)
	}
	var code apperrors.ErrorCode
	require.NoError(t, json.Unmarshal(errorEnvelope["code"], &code))
	require.Equal(t, apperrors.ErrInternalServer, code)
	var message string
	require.NoError(t, json.Unmarshal(errorEnvelope["message"], &message))
	require.Equal(t, "目录操作失败", message)
	var details interface{}
	require.NoError(t, json.Unmarshal(errorEnvelope["details"], &details))
	require.Nil(t, details)
}

func decodeKnowledgeFolderSuccessData(t *testing.T, recorder *httptest.ResponseRecorder) json.RawMessage {
	t.Helper()
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, knowledgeFolderHandlerKeySet("success", "data"), knowledgeFolderHandlerRawKeySet(envelope))
	var success bool
	require.NoError(t, json.Unmarshal(envelope["success"], &success))
	require.True(t, success)
	return envelope["data"]
}

func decodeKnowledgeFolderJSONObject(t *testing.T, data json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var item map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &item))
	return item
}

func decodeKnowledgeFolderJSONString(t *testing.T, data json.RawMessage) string {
	t.Helper()
	var value string
	require.NoError(t, json.Unmarshal(data, &value))
	return value
}

func knowledgeFolderHandlerBaseKeys() map[string]struct{} {
	return knowledgeFolderHandlerKeySet(
		"id",
		"parent_id",
		"name",
		"depth",
		"sort_order",
		"created_at",
		"updated_at",
	)
}

func knowledgeFolderHandlerKeySet(keys ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		set[key] = struct{}{}
	}
	return set
}

func knowledgeFolderHandlerRawKeySet(raw map[string]json.RawMessage) map[string]struct{} {
	set := make(map[string]struct{}, len(raw))
	for key := range raw {
		set[key] = struct{}{}
	}
	return set
}

func requireKnowledgeFolderHandlerExactKeys(
	t *testing.T,
	item map[string]json.RawMessage,
	expected map[string]struct{},
) {
	t.Helper()
	require.Equal(t, expected, knowledgeFolderHandlerRawKeySet(item))
	for _, internalKey := range []string{"tenant_id", "knowledge_base_id", "path", "deleted_at"} {
		require.NotContains(t, item, internalKey)
	}
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
		{name: "wrapped not found", err: fmt.Errorf("wrapped: %w", service.ErrKnowledgeFolderNotFound), wantStatus: http.StatusNotFound},
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
			var envelope map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
			require.Equal(t, knowledgeFolderHandlerKeySet("success", "error"), knowledgeFolderHandlerRawKeySet(envelope))
			var success bool
			require.NoError(t, json.Unmarshal(envelope["success"], &success))
			require.False(t, success)
			var errorEnvelope map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(envelope["error"], &errorEnvelope))
			require.Equal(
				t,
				knowledgeFolderHandlerKeySet("code", "message", "details"),
				knowledgeFolderHandlerRawKeySet(errorEnvelope),
			)
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
