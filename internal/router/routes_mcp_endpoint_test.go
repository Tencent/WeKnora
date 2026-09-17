package router

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/mcpserver"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
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

type stubMCPEndpointServiceForRoutes struct {
	interfaces.MCPEndpointService
}

// TestMCPServerRoutesAreMountedAndReported guards the public MCP surface:
// the transport must be reachable on every Streamable HTTP method and the
// deployment capability must flip on only when all three collaborators are
// injected, mirroring RegisterMCPServerRoutes' nil guard.
func TestMCPServerRoutesAreMountedAndReported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	srv := mcpserver.NewServer(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	RegisterMCPServerRoutes(engine, srv, &stubMCPEndpointServiceForRoutes{}, nil)

	routes := map[string]bool{}
	for _, r := range engine.Routes() {
		routes[r.Method+" "+r.Path] = true
	}
	for _, method := range []string{"POST", "GET", "DELETE"} {
		if !routes[method+" /mcp/:endpoint_id"] {
			t.Errorf("%s /mcp/:endpoint_id not registered", method)
		}
	}

	params := RouterParams{
		MCPEndpointHandler: handler.NewMCPEndpointHandler(nil),
		MCPEndpointService: &stubMCPEndpointServiceForRoutes{},
		MCPServer:          srv,
	}
	if !deploymentCapabilitiesFromRouter(params).Capabilities["integrations.mcpserver"].Supported {
		t.Fatal("integrations.mcpserver must be reported when the MCP server is wired")
	}
	params.MCPServer = nil
	if deploymentCapabilitiesFromRouter(params).Capabilities["integrations.mcpserver"].Supported {
		t.Fatal("integrations.mcpserver must be off without the MCP server")
	}
}
