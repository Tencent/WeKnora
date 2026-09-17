package router

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
)

// TestMCPEndpointRoutesDeclareValidAPIKeyPolicies guards the startup
// self-check for the MCP endpoint management routes: every declared API-key
// policy must resolve to a registered route.
func TestMCPEndpointRoutesDeclareValidAPIKeyPolicies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	g := &rbacGuards{}
	RegisterMCPEndpointRoutes(v1, handler.NewMCPEndpointHandler(nil), g)
	g.assertAPIKeyPoliciesMatchRoutes(engine)

	routes := map[string]bool{}
	for _, r := range engine.Routes() {
		routes[r.Method+" "+r.Path] = true
	}
	for _, want := range []string{
		"GET /api/v1/mcp-endpoints",
		"GET /api/v1/mcp-endpoints/tools",
		"POST /api/v1/mcp-endpoints",
		"PUT /api/v1/mcp-endpoints/:endpoint_id",
		"DELETE /api/v1/mcp-endpoints/:endpoint_id",
		"POST /api/v1/mcp-endpoints/:endpoint_id/rotate-token",
	} {
		if !routes[want] {
			t.Errorf("route %s not registered", want)
		}
	}
}
