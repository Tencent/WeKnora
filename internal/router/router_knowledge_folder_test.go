package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type knowledgeFolderEnsurePathsRouteServiceStub struct {
	interfaces.KnowledgeFolderService
	calls    int
	tenantID uint64
	kbID     string
}

func (s *knowledgeFolderEnsurePathsRouteServiceStub) EnsurePaths(
	ctx context.Context,
	kbID string,
	req *types.KnowledgeFolderEnsurePathsRequest,
) ([]types.KnowledgeFolderEnsurePathResult, error) {
	s.calls++
	s.tenantID, _ = types.TenantIDFromContext(ctx)
	s.kbID = kbID
	clientKey := "key"
	if req != nil && len(req.Paths) > 0 {
		clientKey = req.Paths[0].ClientKey
	}
	return []types.KnowledgeFolderEnsurePathResult{
		{
			ClientKey: clientKey,
			FolderID:  "10000000-0000-4000-8000-000000000001",
		},
	}, nil
}

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
		{method: http.MethodPost, path: "/api/v1/knowledge-bases/:id/folders/ensure-paths"},
		{method: http.MethodPost, path: "/api/v1/knowledge-bases/:id/folders/move-knowledge"},
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
		{method: http.MethodPost, path: "/api/v1/knowledge-bases/kb-other/folders/ensure-paths"},
		{method: http.MethodPost, path: "/api/v1/knowledge-bases/kb-other/folders/move-knowledge"},
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

func TestKnowledgeFolderEnsurePathsStaticRouteDispatchesToEnsurePathsHandler(t *testing.T) {
	serviceStub := &knowledgeFolderEnsurePathsRouteServiceStub{}
	scope := &types.TenantAPIKeyScope{FullAccess: true}
	engine := newKBRouteTestEngine(t, 1, tenantKBLookupFixture(), scope, func(
		r *gin.RouterGroup,
		guards *rbacGuards,
	) {
		RegisterKnowledgeFolderRoutes(
			r,
			handler.NewKnowledgeFolderHandler(serviceStub),
			guards,
		)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/knowledge-bases/kb-allowed/folders/ensure-paths",
		strings.NewReader(`{"paths":[{"client_key":"key","segments":["folder"]}]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, serviceStub.calls)
	require.Equal(t, uint64(1), serviceStub.tenantID)
	require.Equal(t, "kb-allowed", serviceStub.kbID)
}

func TestKnowledgeFolderEnsurePathsRouteEnforcesAPIKeyCapability(t *testing.T) {
	tests := []struct {
		name      string
		scope     *types.TenantAPIKeyScope
		wantCode  int
		wantCalls int
	}{
		{
			name: "ingest allowed",
			scope: &types.TenantAPIKeyScope{
				KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
				Capabilities:     types.StringArray{string(types.APIKeyCapabilityIngest)},
			},
			wantCode:  http.StatusOK,
			wantCalls: 1,
		},
		{
			name:      "full access allowed",
			scope:     &types.TenantAPIKeyScope{FullAccess: true},
			wantCode:  http.StatusOK,
			wantCalls: 1,
		},
		{
			name: "retrieve only denied",
			scope: &types.TenantAPIKeyScope{
				KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
				Capabilities:     types.StringArray{string(types.APIKeyCapabilityRetrieve)},
			},
			wantCode:  http.StatusForbidden,
			wantCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceStub := &knowledgeFolderEnsurePathsRouteServiceStub{}
			engine := newKBRouteTestEngine(t, 1, tenantKBLookupFixture(), tt.scope, func(
				r *gin.RouterGroup,
				guards *rbacGuards,
			) {
				r.Use(guards.ensureAPIKeyAuthorizer().Middleware())
				RegisterKnowledgeFolderRoutes(
					r,
					handler.NewKnowledgeFolderHandler(serviceStub),
					guards,
				)
			})

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/knowledge-bases/kb-allowed/folders/ensure-paths",
				strings.NewReader(`{"paths":[{"client_key":"key","segments":["folder"]}]}`),
			)
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, request)

			require.Equal(t, tt.wantCode, recorder.Code, recorder.Body.String())
			require.Equal(t, tt.wantCalls, serviceStub.calls)
		})
	}
}

func TestKnowledgeFolderEnsurePathsRouteDeniesViewerAndNonOwnerContributor(t *testing.T) {
	for _, role := range []types.TenantRole{
		types.TenantRoleViewer,
		types.TenantRoleContributor,
	} {
		t.Run(string(role), func(t *testing.T) {
			serviceStub := &knowledgeFolderEnsurePathsRouteServiceStub{}
			engine := newKBRouteTestEngine(t, 1, tenantKBLookupFixture(), nil, func(
				r *gin.RouterGroup,
				guards *rbacGuards,
			) {
				r.Use(func(c *gin.Context) {
					ctx := context.WithValue(c.Request.Context(), types.UserIDContextKey, "caller")
					ctx = context.WithValue(ctx, types.TenantRoleContextKey, role)
					c.Request = c.Request.WithContext(ctx)
					c.Next()
				})
				guards.kbCreator = func(_ *gin.Context) (string, error) {
					return "another-user", nil
				}
				RegisterKnowledgeFolderRoutes(
					r,
					handler.NewKnowledgeFolderHandler(serviceStub),
					guards,
				)
			})

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/knowledge-bases/kb-allowed/folders/ensure-paths",
				strings.NewReader(`{"paths":[{"client_key":"key","segments":["folder"]}]}`),
			)
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
			require.Zero(t, serviceStub.calls)
		})
	}
}

func TestKnowledgeFolderEnsurePathsRouteDeniesCrossTenantKnowledgeBase(t *testing.T) {
	kbLookup := &stubWikiKBLookup{
		kbs: map[string]*types.KnowledgeBase{
			"kb-victim": {ID: "kb-victim", TenantID: 999},
		},
	}
	serviceStub := &knowledgeFolderEnsurePathsRouteServiceStub{}
	scope := &types.TenantAPIKeyScope{FullAccess: true}
	engine := newKBRouteTestEngine(t, 1, kbLookup, scope, func(
		r *gin.RouterGroup,
		guards *rbacGuards,
	) {
		r.Use(guards.ensureAPIKeyAuthorizer().Middleware())
		RegisterKnowledgeFolderRoutes(
			r,
			handler.NewKnowledgeFolderHandler(serviceStub),
			guards,
		)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/knowledge-bases/kb-victim/folders/ensure-paths",
		strings.NewReader(`{"paths":[{"client_key":"key","segments":["folder"]}]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
	require.Zero(t, serviceStub.calls)
}

func TestKnowledgeFolderRoutesExposeEnsurePathsButNotRecursiveTreeAPIs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	guards := &rbacGuards{}
	RegisterKnowledgeFolderRoutes(
		engine.Group("/api/v1"),
		&handler.KnowledgeFolderHandler{},
		guards,
	)

	ensurePathsFound := false
	for _, route := range engine.Routes() {
		require.NotContains(t, route.Path, "descendants")
		require.NotContains(t, route.Path, "recursive")
		if route.Method == http.MethodPost &&
			route.Path == "/api/v1/knowledge-bases/:id/folders/ensure-paths" {
			ensurePathsFound = true
		}
	}
	require.True(t, ensurePathsFound)
}
