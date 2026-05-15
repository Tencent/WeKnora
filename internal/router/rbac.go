package router

import (
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// CHOOSING THE RIGHT GUARD (read this before adding a new route)
// ==============================================================
//
// The four role-only guards (Viewer / Contributor / Admin / Owner) ask
// "what is the caller's role in this tenant?". The two ownership
// guards (OwnedKBOrAdmin / OwnedAgentOrAdmin and the per-sub-resource
// variants) ask "is the caller the creator of THIS resource OR at
// least Admin+?".
//
// Picking the wrong one is the single most common source of RBAC
// bugs in this repo (we caught FAQ/Tag, agent share, KB share, and
// shared-agents/disabled all wired against the wrong axis). Two
// questions decide it:
//
// Q1. Does the resource have a creator?
//
//	YES — KB, Agent, Knowledge document, Chunk, WikiPage, FAQ entry,
//	      KB tag, anything stamped with creator_id / created_by.
//	      => Mutating routes use OwnedXxxOrAdmin.
//	      The creator passes regardless of role; everyone else needs
//	      Admin+. This is what makes "Contributor in my own KB acts
//	      like Owner; Contributor in someone else's KB acts like
//	      Viewer" hold uniformly.
//
//	NO  — Tenant-wide infrastructure: Model, VectorStore, IM channel,
//	      WebSearchProvider, DataSource, MCPService, WeKnoraCloud
//	      credentials.
//	      => Mutating routes use Admin().
//	      There is no "creator-of-the-vector-store" concept; configuring
//	      it affects everyone, so only Admin+ may touch it.
//
//	ENTRY POINT — Routes that CREATE a new owned resource (POST
//	      /knowledge-bases, POST /agents).
//	      => Use Contributor() (or whatever the floor is).
//	      No resource exists yet, so we can only gate on role. Once
//	      created, future mutations on /:id flip to OwnedXxxOrAdmin.
//
// Q2. Is the side effect "private to me" or "visible to others"?
//
//	PRIVATE — Action only affects the caller's own state (e.g.
//	      POST /agents/:id/copy creates a copy that belongs to the
//	      caller; the source agent is untouched).
//	      => Contributor() is fine.
//
//	PUBLIC — Action exposes a resource beyond its current scope or
//	      changes state visible to other tenants/users (sharing a KB
//	      to an org, disabling an agent for the whole tenant,
//	      transferring ownership).
//	      => OwnedXxxOrAdmin (when the action targets a specific
//	      owned resource) or Admin (when it's tenant-wide).
//	      Contributor is wrong here even though the role floor passes:
//	      "I am a Contributor in this tenant" does not mean "I may
//	      expose my colleague's KB to the world".
//
// User experience this matrix produces
// ------------------------------------
// The user never sees the guard names. They see this:
//
//   - As Owner / Admin: I can manage everything in my tenant.
//   - As Contributor: I can manage what I created. Other people's
//     resources behave like read-only, regardless of which UI tab.
//   - As Viewer: read everything, mutate nothing.
//   - Creating new resources (KB, agent, chat session) requires being
//     at least Contributor.
//   - Configuring tenant infrastructure (models, vector stores, IM,
//     etc.) requires Admin+.
//
// If a route makes a Contributor surprised that they CAN'T do
// something they own, the gate is too tight (probably Admin where it
// should be OwnedXxxOrAdmin). If a route makes a Contributor surprised
// they CAN do something to someone else's resource, the gate is too
// loose (Contributor where it should be OwnedXxxOrAdmin). Both
// surprises are bugs.
//
// Sub-resources must align with their parent
// ------------------------------------------
// Chunks/wiki pages/FAQ entries/tags inherit their parent KB's gate.
// The KBCreatorLookupFromKnowledgeID / KBCreatorLookupFromKBPath /
// etc. lookups walk the URL param up to the KB and reuse its
// creator_id. Don't add a new sub-resource with a freshly-invented
// gate (a recurring source of "Contributor everywhere" drift).
//
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
	// kbCreatorFromKbIDParam reads :kbId (not :id) for the
	// /initialization/* routes whose KB is addressed by :kbId.
	kbCreatorFromKbIDParam middleware.CreatorLookup
	// Per-KB-ownership lookups for knowledge / chunk / wiki page routes
	// (PR 5, #1303). They walk the URL param back to KB.CreatorID so a
	// Contributor who owns the KB can edit/delete its sub-resources
	// (documents, chunks, wiki pages); a Contributor who merely belongs
	// to the tenant gets 403 unless they're also Admin+.
	knowledgeKBCreator    middleware.CreatorLookup
	chunkKBCreator        middleware.CreatorLookup
	chunkKBCreatorFromID  middleware.CreatorLookup // chunk routes that address chunks by :id (no knowledge id in URL)
	wikiKBCreator         middleware.CreatorLookup

	// Services for the KB-access guard (own / org-shared / via shared
	// agent). Captured here so route lines can reference g.KBAccess()
	// without having to plumb the services through every Register*
	// function.
	kbService         middleware.KBLookup
	knowledgeService  middleware.KnowledgeLookup
	chunkService      middleware.ChunkLookup
	kbShareService    interfaces.KBShareService
	agentShareService interfaces.AgentShareService
}

// newRBACGuards wires the guards from the live configuration and the
// already-built handlers. Called once from NewRouter.
func newRBACGuards(
	cfg *config.Config,
	kbHandler *handler.KnowledgeBaseHandler,
	agentHandler *handler.CustomAgentHandler,
	knowledgeHandler *handler.KnowledgeHandler,
	chunkHandler *handler.ChunkHandler,
	wikiHandler *handler.WikiPageHandler,
	kbService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	chunkService interfaces.ChunkService,
	kbShareService interfaces.KBShareService,
	agentShareService interfaces.AgentShareService,
) *rbacGuards {
	g := &rbacGuards{cfg: cfg}
	if kbHandler != nil {
		g.kbCreator = kbHandler.KBCreatorLookup
		g.kbCreatorFromKbIDParam = kbHandler.KBCreatorLookupFromKbIDParam
	}
	if agentHandler != nil {
		g.agentCreator = agentHandler.AgentCreatorLookup
	}
	if knowledgeHandler != nil {
		g.knowledgeKBCreator = knowledgeHandler.KBCreatorLookupFromKnowledgeID
	}
	if chunkHandler != nil {
		g.chunkKBCreator = chunkHandler.KBCreatorLookupFromKnowledgeIDParam
		g.chunkKBCreatorFromID = chunkHandler.KBCreatorLookupFromChunkIDParam
	}
	if wikiHandler != nil {
		g.wikiKBCreator = wikiHandler.KBCreatorLookupFromKBPath
	}
	g.kbService = kbService
	g.knowledgeService = knowledgeService
	g.chunkService = chunkService
	g.kbShareService = kbShareService
	g.agentShareService = agentShareService
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

// OwnedKBOrAdminFromKbIDParam is the same matrix as OwnedKBOrAdmin but
// addresses the KB via :kbId (used by /initialization/* routes). KB
// configuration changes — picking the embedding/parser/storage
// engine, materialising indexes — are at least as sensitive as
// updating the KB itself, so they share the "creator OR Admin+" rule.
func (g *rbacGuards) OwnedKBOrAdminFromKbIDParam() gin.HandlerFunc {
	return middleware.RequireOwnershipOrRole(types.TenantRoleAdmin, g.kbCreatorFromKbIDParam, g.cfg)
}

// OwnedAgentOrAdmin: same shape as OwnedKBOrAdmin but for CustomAgent.
// Built-in agents (IsBuiltin=true) are tenant-owned; their creator
// lookup returns "" and only Admin+ may mutate them.
func (g *rbacGuards) OwnedAgentOrAdmin() gin.HandlerFunc {
	return middleware.RequireOwnershipOrRole(types.TenantRoleAdmin, g.agentCreator, g.cfg)
}

// OwnedKnowledgeKBOrAdmin: per-knowledge mutations (update / delete /
// reparse / image edit) — the URL :id is a knowledge id, the lookup
// walks it back to the owning KB's CreatorID. Same "creator OR Admin+"
// rule as OwnedKBOrAdmin, just one chain hop deeper. PR 5 (#1303).
func (g *rbacGuards) OwnedKnowledgeKBOrAdmin() gin.HandlerFunc {
	return middleware.RequireOwnershipOrRole(types.TenantRoleAdmin, g.knowledgeKBCreator, g.cfg)
}

// OwnedChunkKBOrAdmin: chunk mutations addressed via :knowledge_id.
// Reuses the same chain helper as OwnedKnowledgeKBOrAdmin so a
// Contributor with KB ownership can manage all chunks under any of
// their documents. For chunk routes addressed via :id (no knowledge
// id in the URL — only chunks.DELETE("/by-id/:id/questions") today),
// see OwnedChunkKBOrAdminFromChunkID below: same matrix, walks one
// extra hop (chunk_id -> knowledge_id) before reusing this chain.
func (g *rbacGuards) OwnedChunkKBOrAdmin() gin.HandlerFunc {
	return middleware.RequireOwnershipOrRole(types.TenantRoleAdmin, g.chunkKBCreator, g.cfg)
}

// OwnedChunkKBOrAdminFromChunkID: chunk mutations addressed via :id
// (the chunk's own id, no knowledge id in the URL). Used by
// chunks.DELETE("/by-id/:id/questions"). Same OwnedKBOrAdmin matrix
// as the rest of the chunk routes — earlier this endpoint stayed at
// flat Contributor because the chunk-id -> knowledge-id -> kb chain
// wasn't wired; that's now plumbed through KBCreatorLookupFromChunkIDParam.
func (g *rbacGuards) OwnedChunkKBOrAdminFromChunkID() gin.HandlerFunc {
	return middleware.RequireOwnershipOrRole(types.TenantRoleAdmin, g.chunkKBCreatorFromID, g.cfg)
}

// OwnedWikiKBOrAdmin: wiki page CRUD and maintenance ops. Wiki routes
// use :kb_id directly so the lookup is a single hop into the KB
// service — no knowledge chain. Same matrix as OwnedKBOrAdmin.
func (g *rbacGuards) OwnedWikiKBOrAdmin() gin.HandlerFunc {
	return middleware.RequireOwnershipOrRole(types.TenantRoleAdmin, g.wikiKBCreator, g.cfg)
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

// KB-access guards — orthogonal to the role-and-ownership matrix
// above. They answer "can the caller's tenant operate on THIS KB?"
// taking into account three paths:
//
//   1. Own KB                         — full access (Admin)
//   2. Org-shared KB (Plan 3)         — capped permission
//   3. Visible via shared agent       — read-only
//
// On success the resolved (KB + effective tenant id + permission)
// tuple is stashed on c.Keys under middleware.KBAccessContextKey AND
// the request context's tenant ID is rewritten to the effective tenant
// — so handlers downstream just read tenant the way they always did
// (types.MustTenantIDFromContext) without knowing whether the KB is
// owned or shared.
//
// These guards replace the per-handler effectiveCtxForKB /
// validateAndGetKnowledgeBase helpers that used to be re-implemented
// in chunk.go, faq.go, tag.go, knowledge.go and knowledgebase.go;
// the share-fallback logic now lives in exactly one place
// (middleware/kb_access.go).

// KBAccessRead gates a KB-scoped read route on the caller having at
// least Viewer-level access. The agent-share fallback only activates
// at this level — Editor/Admin reads never go through "I just see it
// because someone shared an agent". The kbID is read from the gin
// param named in `param` (typically "id" for /knowledge-bases/:id/...).
func (g *rbacGuards) KBAccessRead(param string) gin.HandlerFunc {
	return middleware.RequireKBAccess(
		middleware.KBIDFromParam(param),
		types.OrgRoleViewer,
		g.kbService,
		g.kbShareService,
		g.agentShareService,
		g.cfg,
	)
}

// KBAccessWrite gates a KB-scoped mutating route on the caller having
// at least Editor-level access (own KB or org-shared with editor).
// Used by FAQ upsert, tag CRUD, chunk update/delete, etc.
func (g *rbacGuards) KBAccessWrite(param string) gin.HandlerFunc {
	return middleware.RequireKBAccess(
		middleware.KBIDFromParam(param),
		types.OrgRoleEditor,
		g.kbService,
		g.kbShareService,
		g.agentShareService,
		g.cfg,
	)
}

// KBAccessReadFromKnowledgeIDParam is like KBAccessRead but resolves
// the kb_id by walking a knowledge document (URL `:knowledge_id`)
// back to its parent KB. Used by the chunk routes whose URL addresses
// the chunk via /chunks/:knowledge_id rather than /knowledge-bases/:id.
func (g *rbacGuards) KBAccessReadFromKnowledgeIDParam(param string) gin.HandlerFunc {
	return middleware.RequireKBAccess(
		middleware.KBIDFromKnowledgeIDParam(param, g.knowledgeService),
		types.OrgRoleViewer,
		g.kbService,
		g.kbShareService,
		g.agentShareService,
		g.cfg,
	)
}

// KBAccessWriteFromKnowledgeIDParam mirrors KBAccessReadFromKnowledgeIDParam
// for mutating routes (Editor minimum).
func (g *rbacGuards) KBAccessWriteFromKnowledgeIDParam(param string) gin.HandlerFunc {
	return middleware.RequireKBAccess(
		middleware.KBIDFromKnowledgeIDParam(param, g.knowledgeService),
		types.OrgRoleEditor,
		g.kbService,
		g.kbShareService,
		g.agentShareService,
		g.cfg,
	)
}

// KBAccessReadFromChunkIDParam walks chunk_id -> kb_id (using the
// chunk's denormalised KnowledgeBaseID column). Used by
// /chunks/by-id/:id read routes.
func (g *rbacGuards) KBAccessReadFromChunkIDParam(param string) gin.HandlerFunc {
	return middleware.RequireKBAccess(
		middleware.KBIDFromChunkIDParam(param, g.chunkService),
		types.OrgRoleViewer,
		g.kbService,
		g.kbShareService,
		g.agentShareService,
		g.cfg,
	)
}

// KBAccessWriteFromChunkIDParam — same as KBAccessReadFromChunkIDParam
// but requires Editor minimum. Used by chunk write routes that
// address the chunk via /chunks/by-id/:id.
func (g *rbacGuards) KBAccessWriteFromChunkIDParam(param string) gin.HandlerFunc {
	return middleware.RequireKBAccess(
		middleware.KBIDFromChunkIDParam(param, g.chunkService),
		types.OrgRoleEditor,
		g.kbService,
		g.kbShareService,
		g.agentShareService,
		g.cfg,
	)
}
