package router

import (
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// rbacGuards is the centralised role-matrix bundle for tenant-level RBAC
// (issue #1303 PR 2). NewRouter constructs it once and threads it into
// each Register* function that registers gated routes.
//
// Each method returns a fresh gin.HandlerFunc; routes call the method
// and inline the guard, so a glance at a route line tells you what
// authority it requires:
//
//	kb.PUT("/:id", g.OwnedKBOrAdmin(), handler.UpdateKnowledgeBase)
//
// All guards honour cfg.Tenant.EnableRBAC: when the flag is off they log
// the would-be rejection and let the request through, preserving today's
// "anyone in the tenant can edit anything" behaviour during the rollout
// window. When the flag flips to true, the same code paths start
// rejecting unauthorised callers.
type rbacGuards struct {
	cfg *config.Config

	// Lookup closures resolve a request's :id into the resource's creator
	// user ID. Captured up front so the handler-level methods don't have
	// to be exported into every Register* function as well.
	kbCreator    middleware.CreatorLookup
	agentCreator middleware.CreatorLookup
}

// newRBACGuards wires the guards from the live configuration and the
// already-built handlers. Called once from NewRouter.
func newRBACGuards(cfg *config.Config, kbHandler *handler.KnowledgeBaseHandler, agentHandler *handler.CustomAgentHandler) *rbacGuards {
	g := &rbacGuards{cfg: cfg}
	if kbHandler != nil {
		g.kbCreator = kbHandler.KBCreatorLookup
	}
	if agentHandler != nil {
		g.agentCreator = agentHandler.AgentCreatorLookup
	}
	return g
}

// Role-only guards — pure RequireRole convenience wrappers, named after
// the matrix entries so route lines stay readable.

func (g *rbacGuards) Viewer() gin.HandlerFunc {
	return middleware.RequireRole(types.TenantRoleViewer, g.cfg)
}

func (g *rbacGuards) Contributor() gin.HandlerFunc {
	return middleware.RequireRole(types.TenantRoleContributor, g.cfg)
}

func (g *rbacGuards) Admin() gin.HandlerFunc {
	return middleware.RequireRole(types.TenantRoleAdmin, g.cfg)
}

func (g *rbacGuards) Owner() gin.HandlerFunc {
	return middleware.RequireRole(types.TenantRoleOwner, g.cfg)
}

// Ownership-or-role guards. Required role here is the privilege level
// that bypasses the ownership check; Contributors ALWAYS pass when they
// own the resource.

// OwnedKBOrAdmin: KB mutations (update/delete/pin/copy). The original
// creator may proceed; otherwise Admin+ is required. Contributors who
// did not create the KB get 403 (when enforcement is on).
func (g *rbacGuards) OwnedKBOrAdmin() gin.HandlerFunc {
	return middleware.RequireOwnershipOrRole(types.TenantRoleAdmin, g.kbCreator, g.cfg)
}

// OwnedAgentOrAdmin: same shape as OwnedKBOrAdmin but for CustomAgent.
// Built-in agents (IsBuiltin=true) are tenant-owned; their creator
// lookup returns "" and only Admin+ may mutate them.
func (g *rbacGuards) OwnedAgentOrAdmin() gin.HandlerFunc {
	return middleware.RequireOwnershipOrRole(types.TenantRoleAdmin, g.agentCreator, g.cfg)
}

// Tenant-access guards. Distinct from the role guards above: these
// answer the orthogonal question "may this caller touch this tenant
// at all", before role membership inside the tenant is even
// considered. Both delegate to middleware/access.go which centralises
// the cross-tenant rules so the router stays declarative.

// CrossTenant gates a route on the caller being an org-level
// superuser (CanAccessAllTenants AND EnableCrossTenantAccess). Used by
// /tenants/all, /tenants/search, POST /tenants, GET /tenants — the
// endpoints that operate across tenants. Replaces the if-blocks that
// used to live inside ListAllTenants/SearchTenants/CreateTenant.
func (g *rbacGuards) CrossTenant() gin.HandlerFunc {
	return middleware.RequireCrossTenantAccess(g.cfg)
}

// PathTenantMatch enforces that the URL :id matches the caller's
// active tenant context (cross-tenant superusers bypass). Routes apply
// it at the /tenants/:id group level so every per-tenant endpoint —
// GetTenant / UpdateTenant / DeleteTenant / ResetAPIKey / member
// management / leave — shares the same check. Replaces the
// authorizeTenantAccess helper that used to live inside the tenant
// handler.
func (g *rbacGuards) PathTenantMatch() gin.HandlerFunc {
	return middleware.RequirePathTenantMatch(g.cfg)
}
