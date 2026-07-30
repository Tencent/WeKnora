package router

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

func TestFeedbackRoutesRequireFullAccessAPIKeyPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	RegisterFeedbackRoutes(v1, &handler.FeedbackHandler{}, g)

	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/api/v1/sessions/:session_id/messages/:message_id/feedback"},
		{http.MethodGet, "/api/v1/chunks/by-id/:id/feedback"},
		{http.MethodPost, "/api/v1/knowledge-bases/:id/chunks/:chunk_id/feedback/reset"},
	} {
		policy := mustLookupAPIKeyPolicy(t, g, route.method, route.path)
		if !policy.RequireFullAccess {
			t.Fatalf("%s %s must require full API-key access", route.method, route.path)
		}
	}

	governanceRoutes := map[string]bool{
		http.MethodGet + " /api/v1/knowledge-bases/:id/chunk-feedback":                   false,
		http.MethodGet + " /api/v1/knowledge-bases/:id/chunk-feedback/:chunk_id":         false,
		http.MethodGet + " /api/v1/knowledge-bases/:id/chunk-feedback/:chunk_id/history": false,
		http.MethodPost + " /api/v1/knowledge-bases/:id/chunk-feedback/:chunk_id/reset":  false,
	}
	for _, route := range engine.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := governanceRoutes[key]; ok {
			governanceRoutes[key] = true
			if _, declared := g.apiKeyAuthorizer.Lookup(route.Method, route.Path); declared {
				t.Fatalf("governance route must remain API-key default-deny: %s", key)
			}
		}
	}
	for route, found := range governanceRoutes {
		if !found {
			t.Fatalf("governance route not registered: %s", route)
		}
	}
}
