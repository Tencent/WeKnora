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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubWikiKBLookup struct {
	kbs map[string]*types.KnowledgeBase
}

func (s *stubWikiKBLookup) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if kb, ok := s.kbs[id]; ok {
		return kb, nil
	}
	return nil, apprepo.ErrKnowledgeBaseNotFound
}

func newWikiRouteTestEngine(t *testing.T, callerTenantID uint64, kbLookup *stubWikiKBLookup) *gin.Engine {
	return newKBRouteTestEngine(t, callerTenantID, kbLookup, nil, func(r *gin.RouterGroup, guards *rbacGuards) {
		RegisterWikiPageRoutes(r, &handler.WikiPageHandler{}, guards)
	})
}

func newInitializationRouteTestEngine(t *testing.T, callerTenantID uint64, kbLookup *stubWikiKBLookup) *gin.Engine {
	return newKBRouteTestEngine(t, callerTenantID, kbLookup, nil, func(r *gin.RouterGroup, guards *rbacGuards) {
		RegisterInitializationRoutes(r, &handler.InitializationHandler{}, guards)
	})
}

func newKBRouteTestEngine(
	t *testing.T,
	callerTenantID uint64,
	kbLookup *stubWikiKBLookup,
	apiKeyScope *types.TenantAPIKeyScope,
	register func(r *gin.RouterGroup, guards *rbacGuards),
) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	enabled := true
	cfg := &config.Config{
		Tenant: &config.TenantConfig{EnableRBAC: &enabled},
	}
	guards := &rbacGuards{
		cfg:       cfg,
		kbService: kbLookup,
	}

	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		ctx := c.Request.Context()
		ctx = context.WithValue(ctx, types.TenantIDContextKey, callerTenantID)
		role := types.TenantRoleViewer
		if apiKeyScope != nil {
			ctx = types.WithTenantAPIKeyScope(ctx, *apiKeyScope)
			role = apiKeyScope.Role
			if role == "" {
				role = types.TenantRoleViewer
			}
		}
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, role)
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), callerTenantID)
		c.Next()
	})

	register(r.Group("/api/v1"), guards)
	return r
}

func tenantKBLookupFixture() *stubWikiKBLookup {
	return &stubWikiKBLookup{
		kbs: map[string]*types.KnowledgeBase{
			"kb-allowed": {ID: "kb-allowed", TenantID: 1, Type: types.KnowledgeBaseTypeWiki},
			"kb-other":   {ID: "kb-other", TenantID: 1, Type: types.KnowledgeBaseTypeWiki},
		},
	}
}

func TestInitializationConfigRouteDenyCrossTenantKB(t *testing.T) {
	kbLookup := &stubWikiKBLookup{
		kbs: map[string]*types.KnowledgeBase{
			"kb-victim": {ID: "kb-victim", TenantID: 999},
		},
	}
	engine := newInitializationRouteTestEngine(t, 1, kbLookup)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/initialization/config/kb-victim", nil)
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
}

func TestWikiReadRoutesDenyCrossTenantKB(t *testing.T) {
	kbLookup := &stubWikiKBLookup{
		kbs: map[string]*types.KnowledgeBase{
			"kb-victim": {
				ID:       "kb-victim",
				TenantID: 999,
				Type:     types.KnowledgeBaseTypeWiki,
			},
		},
	}
	engine := newWikiRouteTestEngine(t, 1, kbLookup)

	paths := []string{
		"/api/v1/knowledgebase/kb-victim/wiki/pages",
		"/api/v1/knowledgebase/kb-victim/wiki/pages/secret-page",
		"/api/v1/knowledgebase/kb-victim/wiki/folders",
		"/api/v1/knowledgebase/kb-victim/wiki/index",
		"/api/v1/knowledgebase/kb-victim/wiki/log",
		"/api/v1/knowledgebase/kb-victim/wiki/graph",
		"/api/v1/knowledgebase/kb-victim/wiki/stats",
		"/api/v1/knowledgebase/kb-victim/wiki/search?q=test",
		"/api/v1/knowledgebase/kb-victim/wiki/lint",
		"/api/v1/knowledgebase/kb-victim/wiki/issues",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			engine.ServeHTTP(rec, req)
			require.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
		})
	}
}

func TestInitializationWriteRoutesDenyOutOfScopeAPIKeyKB(t *testing.T) {
	kbLookup := tenantKBLookupFixture()
	scope := &types.TenantAPIKeyScope{
		Role:             types.TenantRoleContributor,
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
	}
	engine := newKBRouteTestEngine(t, 1, kbLookup, scope, func(r *gin.RouterGroup, guards *rbacGuards) {
		RegisterInitializationRoutes(r, &handler.InitializationHandler{}, guards)
	})

	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPut, "/api/v1/initialization/config/kb-other", `{}`},
		{http.MethodPost, "/api/v1/initialization/initialize/kb-other", `{}`},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(rec, req)
			require.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
		})
	}
}

func TestWikiWriteRoutesDenyOutOfScopeAPIKeyKB(t *testing.T) {
	kbLookup := tenantKBLookupFixture()
	scope := &types.TenantAPIKeyScope{
		Role:             types.TenantRoleContributor,
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
	}
	engine := newKBRouteTestEngine(t, 1, kbLookup, scope, func(r *gin.RouterGroup, guards *rbacGuards) {
		RegisterWikiPageRoutes(r, &handler.WikiPageHandler{}, guards)
	})

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/knowledgebase/kb-other/wiki/pages"},
		{http.MethodPut, "/api/v1/knowledgebase/kb-other/wiki/pages/some-page"},
		{http.MethodDelete, "/api/v1/knowledgebase/kb-other/wiki/pages/some-page"},
		{http.MethodPost, "/api/v1/knowledgebase/kb-other/wiki/folders"},
		{http.MethodPost, "/api/v1/knowledgebase/kb-other/wiki/rebuild-links"},
		{http.MethodPost, "/api/v1/knowledgebase/kb-other/wiki/auto-fix"},
		{http.MethodPut, "/api/v1/knowledgebase/kb-other/wiki/issues/1/status"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(rec, req)
			require.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
		})
	}
}
