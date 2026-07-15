package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type folderRouteServiceStub struct {
	interfaces.KnowledgeFolderService
}

func (s *folderRouteServiceStub) List(
	_ context.Context, _ string, _ string, _ string, page *types.Pagination,
) (*types.PageResult, error) {
	return types.NewPageResult(0, page, []*types.KnowledgeFolderView{}), nil
}

func (s *folderRouteServiceStub) Create(
	_ context.Context, kbID string, parentID string, name string,
) (*types.KnowledgeFolder, error) {
	return &types.KnowledgeFolder{
		ID: "folder-created", KnowledgeBaseID: kbID, ParentID: parentID, Name: name,
	}, nil
}

type folderRouteKBShareStub struct {
	interfaces.KBShareService
	permission types.OrgMemberRole
	source     uint64
}

func (s *folderRouteKBShareStub) CheckTenantKBPermission(
	context.Context, string, uint64, types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	return s.permission, true, nil
}

func (s *folderRouteKBShareStub) GetKBSourceTenant(context.Context, string) (uint64, error) {
	return s.source, nil
}

func newKnowledgeFolderRouteTestEngine(
	t *testing.T,
	callerTenantID uint64,
	role types.TenantRole,
	userID string,
	kbLookup *stubWikiKBLookup,
	shareService interfaces.KBShareService,
	apiKeyScope *types.TenantAPIKeyScope,
) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	enabled := true
	guards := &rbacGuards{
		cfg:            &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}},
		kbService:      kbLookup,
		kbShareService: shareService,
	}
	guards.kbCreator = func(c *gin.Context) (string, error) {
		kb, err := kbLookup.GetKnowledgeBaseByID(c.Request.Context(), c.Param("id"))
		if err != nil {
			if err == apprepo.ErrKnowledgeBaseNotFound {
				return "", middleware.ErrResourceNotFound
			}
			return "", err
		}
		tenantID, _ := types.TenantIDFromContext(c.Request.Context())
		if kb.TenantID != tenantID {
			return "", middleware.ErrResourceNotFound
		}
		return kb.CreatorID, nil
	}

	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, callerTenantID)
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, role)
		ctx = context.WithValue(ctx, types.UserIDContextKey, userID)
		if apiKeyScope != nil {
			ctx = types.WithTenantAPIKeyScope(ctx, *apiKeyScope)
		}
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), callerTenantID)
		c.Next()
	})

	h := handler.NewKnowledgeFolderHandler(&folderRouteServiceStub{})
	RegisterKnowledgeFolderRoutes(r.Group("/api/v1"), h, guards)
	return r
}

func performKnowledgeFolderRouteRequest(
	t *testing.T, engine http.Handler, method string, path string, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	engine.ServeHTTP(recorder, request)
	return recorder
}

func TestKnowledgeFolderRoutesEnforceViewerAndSharedEditorPermissions(t *testing.T) {
	ownedKBs := &stubWikiKBLookup{kbs: map[string]*types.KnowledgeBase{
		"kb-owned": {ID: "kb-owned", TenantID: 1, CreatorID: "owner"},
	}}

	t.Run("viewer can read owned folders", func(t *testing.T) {
		engine := newKnowledgeFolderRouteTestEngine(t, 1, types.TenantRoleViewer, "viewer", ownedKBs, nil, nil)
		response := performKnowledgeFolderRouteRequest(t, engine, http.MethodGet, "/api/v1/knowledge-bases/kb-owned/folders", "")
		require.Equal(t, http.StatusOK, response.Code, "body=%s", response.Body.String())
	})

	t.Run("viewer cannot create owned folders", func(t *testing.T) {
		engine := newKnowledgeFolderRouteTestEngine(t, 1, types.TenantRoleViewer, "viewer", ownedKBs, nil, nil)
		response := performKnowledgeFolderRouteRequest(t, engine, http.MethodPost, "/api/v1/knowledge-bases/kb-owned/folders", `{"name":"Reports","parent_id":""}`)
		require.Equal(t, http.StatusForbidden, response.Code, "body=%s", response.Body.String())
	})

	sharedKBs := &stubWikiKBLookup{kbs: map[string]*types.KnowledgeBase{
		"kb-shared": {ID: "kb-shared", TenantID: 9, CreatorID: "source-owner"},
	}}

	t.Run("shared viewer can read folders", func(t *testing.T) {
		share := &folderRouteKBShareStub{permission: types.OrgRoleViewer, source: 9}
		engine := newKnowledgeFolderRouteTestEngine(t, 1, types.TenantRoleViewer, "viewer", sharedKBs, share, nil)
		response := performKnowledgeFolderRouteRequest(t, engine, http.MethodGet, "/api/v1/knowledge-bases/kb-shared/folders", "")
		require.Equal(t, http.StatusOK, response.Code, "body=%s", response.Body.String())
	})

	t.Run("read-only share cannot create folders", func(t *testing.T) {
		share := &folderRouteKBShareStub{permission: types.OrgRoleViewer, source: 9}
		engine := newKnowledgeFolderRouteTestEngine(t, 1, types.TenantRoleContributor, "contributor", sharedKBs, share, nil)
		response := performKnowledgeFolderRouteRequest(t, engine, http.MethodPost, "/api/v1/knowledge-bases/kb-shared/folders", `{"name":"Reports","parent_id":""}`)
		require.Equal(t, http.StatusForbidden, response.Code, "body=%s", response.Body.String())
	})

	t.Run("editable share can create folders", func(t *testing.T) {
		share := &folderRouteKBShareStub{permission: types.OrgRoleEditor, source: 9}
		engine := newKnowledgeFolderRouteTestEngine(t, 1, types.TenantRoleContributor, "contributor", sharedKBs, share, nil)
		response := performKnowledgeFolderRouteRequest(t, engine, http.MethodPost, "/api/v1/knowledge-bases/kb-shared/folders", `{"name":"Reports","parent_id":""}`)
		require.Equal(t, http.StatusCreated, response.Code, "body=%s", response.Body.String())
	})
}

func TestKnowledgeFolderRoutesRejectCrossTenantAndOutOfScopeAPIKeys(t *testing.T) {
	foreignKBs := &stubWikiKBLookup{kbs: map[string]*types.KnowledgeBase{
		"kb-foreign": {ID: "kb-foreign", TenantID: 9, CreatorID: "source-owner"},
	}}
	engine := newKnowledgeFolderRouteTestEngine(t, 1, types.TenantRoleViewer, "viewer", foreignKBs, nil, nil)
	response := performKnowledgeFolderRouteRequest(t, engine, http.MethodGet, "/api/v1/knowledge-bases/kb-foreign/folders", "")
	require.Equal(t, http.StatusForbidden, response.Code, "body=%s", response.Body.String())

	ownedKBs := &stubWikiKBLookup{kbs: map[string]*types.KnowledgeBase{
		"kb-allowed": {ID: "kb-allowed", TenantID: 1},
		"kb-other":   {ID: "kb-other", TenantID: 1},
	}}
	scope := &types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
		Capabilities:     types.StringArray{string(types.APIKeyCapabilityIngest), string(types.APIKeyCapabilityRetrieve)},
	}
	engine = newKnowledgeFolderRouteTestEngine(t, 1, types.TenantRoleViewer, "", ownedKBs, nil, scope)
	for _, test := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/knowledge-bases/kb-other/folders", ""},
		{http.MethodPost, "/api/v1/knowledge-bases/kb-other/folders", `{"name":"Reports","parent_id":""}`},
		{http.MethodPut, "/api/v1/knowledge-bases/kb-other/knowledge/folder", `{"knowledge_ids":["doc-1"],"folder_id":""}`},
	} {
		response = performKnowledgeFolderRouteRequest(t, engine, test.method, test.path, test.body)
		require.Equal(t, http.StatusForbidden, response.Code, "%s %s body=%s", test.method, test.path, response.Body.String())
	}
}

func TestKnowledgeFolderRoutesDeclareAPIKeyCapabilities(t *testing.T) {
	gin.SetMode(gin.TestMode)
	guards := &rbacGuards{}
	RegisterKnowledgeFolderRoutes(
		gin.New().Group("/api/v1"),
		handler.NewKnowledgeFolderHandler(&folderRouteServiceStub{}),
		guards,
	)

	for _, path := range []string{
		"/api/v1/knowledge-bases/:id/folders",
		"/api/v1/knowledge-bases/:id/folders/:folder_id",
	} {
		policy := mustLookupAPIKeyPolicy(t, guards, http.MethodGet, path)
		require.True(t, policyHasCapability(policy, types.APIKeyCapabilityRetrieve))
	}

	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/knowledge-bases/:id/folders"},
		{http.MethodPost, "/api/v1/knowledge-bases/:id/folders/ensure-paths"},
		{http.MethodPut, "/api/v1/knowledge-bases/:id/folders/:folder_id"},
		{http.MethodDelete, "/api/v1/knowledge-bases/:id/folders/:folder_id"},
		{http.MethodPut, "/api/v1/knowledge-bases/:id/knowledge/folder"},
	} {
		policy := mustLookupAPIKeyPolicy(t, guards, route.method, route.path)
		require.True(t, policyHasCapability(policy, types.APIKeyCapabilityIngest))
	}
}
