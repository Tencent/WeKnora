package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeFolderRoutesDeclareAPIKeyCapabilities(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	guards := &rbacGuards{}
	RegisterKnowledgeFolderRoutes(
		engine.Group("/api/v1"),
		&handler.KnowledgeFolderHandler{},
		guards,
	)

	readRoutes := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/v1/knowledge-bases/:id/folders"},
		{method: http.MethodGet, path: "/api/v1/knowledge-bases/:id/folders/:folder_id"},
		{method: http.MethodGet, path: "/api/v1/knowledge-bases/:id/folders/:folder_id/breadcrumb"},
	}
	for _, route := range readRoutes {
		policy := mustLookupAPIKeyPolicy(t, guards, route.method, route.path)
		assert.True(t, policy.RequireFullAccess, route.path)
		assert.True(t, policyHasCapability(policy, types.APIKeyCapabilityRetrieve), route.path)
		assert.False(t, policyHasCapability(policy, types.APIKeyCapabilityIngest), route.path)
	}

	writeRoutes := []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/v1/knowledge-bases/:id/folders"},
		{method: http.MethodPatch, path: "/api/v1/knowledge-bases/:id/folders/:folder_id"},
		{method: http.MethodDelete, path: "/api/v1/knowledge-bases/:id/folders/:folder_id"},
	}
	for _, route := range writeRoutes {
		policy := mustLookupAPIKeyPolicy(t, guards, route.method, route.path)
		assert.True(t, policy.RequireFullAccess, route.path)
		assert.True(t, policyHasCapability(policy, types.APIKeyCapabilityIngest), route.path)
		assert.False(t, policyHasCapability(policy, types.APIKeyCapabilityRetrieve), route.path)
	}
	guards.assertAPIKeyPoliciesMatchRoutes(engine)
}

func TestKnowledgeFolderReadRoutesDenyCrossTenantKnowledgeBase(t *testing.T) {
	kbLookup := &stubWikiKBLookup{
		kbs: map[string]*types.KnowledgeBase{
			"kb-victim": {ID: "kb-victim", TenantID: 999},
		},
	}
	engine := newKBRouteTestEngine(t, 1, kbLookup, nil, func(
		r *gin.RouterGroup,
		guards *rbacGuards,
	) {
		RegisterKnowledgeFolderRoutes(r, &handler.KnowledgeFolderHandler{}, guards)
	})

	for _, path := range []string{
		"/api/v1/knowledge-bases/kb-victim/folders",
		"/api/v1/knowledge-bases/kb-victim/folders/folder-1",
		"/api/v1/knowledge-bases/kb-victim/folders/folder-1/breadcrumb",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		engine.ServeHTTP(recorder, request)
		assert.Equal(t, http.StatusForbidden, recorder.Code, path)
	}
}

func TestKnowledgeFolderWriteRoutesDenyOutOfScopeAPIKeyKnowledgeBase(t *testing.T) {
	kbLookup := tenantKBLookupFixture()
	scope := &types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
		Capabilities:     types.StringArray{string(types.APIKeyCapabilityIngest)},
	}
	engine := newKBRouteTestEngine(t, 1, kbLookup, scope, func(
		r *gin.RouterGroup,
		guards *rbacGuards,
	) {
		RegisterKnowledgeFolderRoutes(r, &handler.KnowledgeFolderHandler{}, guards)
	})

	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/v1/knowledge-bases/kb-other/folders"},
		{method: http.MethodPatch, path: "/api/v1/knowledge-bases/kb-other/folders/folder-1"},
		{method: http.MethodDelete, path: "/api/v1/knowledge-bases/kb-other/folders/folder-1"},
	}
	for _, tt := range tests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(tt.method, tt.path, strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(recorder, request)
		assert.Equal(t, http.StatusForbidden, recorder.Code, tt.method+" "+tt.path)
	}
}

func TestKnowledgeFolderRoutesDoNotExposeDescendantsOrEnsurePaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	guards := &rbacGuards{}
	RegisterKnowledgeFolderRoutes(
		engine.Group("/api/v1"),
		&handler.KnowledgeFolderHandler{},
		guards,
	)

	for _, route := range engine.Routes() {
		require.NotContains(t, route.Path, "descendants")
		require.NotContains(t, route.Path, "ensure-paths")
	}
}
