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
)

type routerCollectionService struct {
	interfaces.AgentCollectionService
}

func (routerCollectionService) ListProfiles(
	context.Context, types.AgentCollectionProfileFilter,
) (*types.AgentCollectionProfilePage, error) {
	return &types.AgentCollectionProfilePage{Items: []*types.AgentCollectionProfile{}, Page: 1, PageSize: 20}, nil
}
func (routerCollectionService) SummarizeProfiles(
	context.Context, types.AgentCollectionProfileFilter,
) (*types.AgentCollectionSummary, error) {
	return &types.AgentCollectionSummary{}, nil
}

type routerCollectionAgents struct{ interfaces.CustomAgentService }

func collectionAdminRouter(isSystemAdmin bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.UserIDContextKey, "user-1")
		ctx = context.WithValue(ctx, types.SystemAdminContextKey, isSystemAdmin)
		c.Request = c.Request.WithContext(ctx)
	})
	collection := handler.NewSystemAgentCollectionHandler(routerCollectionService{}, routerCollectionAgents{}, nil)
	RegisterSystemAdminRoutes(
		engine.Group("/api/v1"), &handler.SystemHandler{}, nil, collection, &rbacGuards{},
	)
	return engine
}

func TestAgentCollectionRoutesAreSystemAdminOnly(t *testing.T) {
	routes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/system/admin/collection-profiles", ""},
		{http.MethodGet, "/api/v1/system/admin/collection-profiles/profile-1", ""},
		{http.MethodGet, "/api/v1/system/admin/collection-profiles/profile-1/history", ""},
		{http.MethodPut, "/api/v1/system/admin/collection-profiles/profile-1/fields/status", `{}`},
		{http.MethodDelete, "/api/v1/system/admin/collection-profiles/profile-1", `{}`},
		{http.MethodPost, "/api/v1/system/admin/collection-profiles/export", `{}`},
		{http.MethodGet, "/api/v1/system/admin/collection-exports/export-1", ""},
	}
	ownerRouter := collectionAdminRouter(false)
	for _, route := range routes {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
		request.Header.Set("Content-Type", "application/json")
		ownerRouter.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s %s status = %d, want 403", route.method, route.path, response.Code)
		}
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/system/admin/collection-profiles", nil)
	collectionAdminRouter(true).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("system admin list status = %d, body = %s", response.Code, response.Body.String())
	}
}
