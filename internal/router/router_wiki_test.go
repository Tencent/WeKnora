package router

import (
	"context"
	"net/http"
	"net/http/httptest"
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
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleViewer)
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), callerTenantID)
		c.Next()
	})

	RegisterWikiPageRoutes(r.Group("/api/v1"), &handler.WikiPageHandler{}, guards)
	return r
}

func newInitializationRouteTestEngine(t *testing.T, callerTenantID uint64, kbLookup *stubWikiKBLookup) *gin.Engine {
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
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleViewer)
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), callerTenantID)
		c.Next()
	})

	RegisterInitializationRoutes(r.Group("/api/v1"), &handler.InitializationHandler{}, guards)
	return r
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
