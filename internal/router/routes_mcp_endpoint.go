package router

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/mcpserver"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// RegisterMCPEndpointRoutes registers the management API for workspace MCP
// endpoints. Endpoints are tenant-wide integration surfaces like embed
// channels: reads for every member, mutations for admins, and API keys need
// the manage_channels capability.
//
// Personal endpoints live under /mcp-personal-endpoints: any member manages
// their own, and the routes declare no API-key policy so the gate rejects API
// keys, which carry no member identity for the endpoint to act as.
func RegisterMCPEndpointRoutes(r *gin.RouterGroup, h *handler.MCPEndpointHandler, g *rbacGuards) {
	if h == nil {
		return
	}
	grp := g.apiKeyGroup(r.Group("/mcp-endpoints"), apiKeyManageChannels(apiKeyFullAccess()))
	grp.GET("", g.Viewer(), h.ListMCPEndpoints)
	grp.GET("/tools", g.Viewer(), h.ListToolCatalog)
	grp.GET("/settings", g.Viewer(), h.GetMCPEndpointSettings)
	grp.PUT("/settings", g.Admin(), h.UpdateMCPEndpointSettings)
	grp.POST("", g.Admin(), h.CreateMCPEndpoint)
	grp.GET("/:endpoint_id", g.Viewer(), h.GetMCPEndpoint)
	grp.PUT("/:endpoint_id", g.Admin(), h.UpdateMCPEndpoint)
	grp.DELETE("/:endpoint_id", g.Admin(), h.DeleteMCPEndpoint)
	grp.POST("/:endpoint_id/rotate-token", g.Admin(), h.RotateMCPEndpointToken)

	personal := r.Group("/mcp-personal-endpoints")
	personal.GET("", g.Viewer(), h.ListPersonalMCPEndpoints)
	personal.POST("", g.Viewer(), h.CreatePersonalMCPEndpoint)
	personal.GET("/:endpoint_id", g.Viewer(), h.GetPersonalMCPEndpoint)
	personal.PUT("/:endpoint_id", g.Viewer(), h.UpdatePersonalMCPEndpoint)
	personal.DELETE("/:endpoint_id", g.Viewer(), h.DeletePersonalMCPEndpoint)
	personal.POST("/:endpoint_id/rotate-token", g.Viewer(), h.RotatePersonalMCPEndpointToken)
}

// RegisterMCPServerRoutes mounts the public MCP server surface at
// /mcp/:endpoint_id. It carries its own bearer-token auth and is registered
// before the global Auth middleware, like the embed public routes.
func RegisterMCPServerRoutes(
	r *gin.Engine,
	srv *mcpserver.Server,
	endpointService interfaces.MCPEndpointService,
	tenantService interfaces.TenantService,
	memberService interfaces.TenantMemberService,
	userService interfaces.UserService,
) {
	if srv == nil || endpointService == nil {
		return
	}
	h := gin.WrapH(srv.Handler())
	auth := middleware.MCPEndpointAuth(endpointService, tenantService, memberService, userService)
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		r.Handle(method, handler.MCPEndpointPath+":endpoint_id", auth, h)
	}
}
