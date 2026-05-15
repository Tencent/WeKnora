package middleware

import (
	"context"
	stderrors "errors"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// kb_access.go centralises the share-fallback that previously lived as
// near-identical 30-line helpers in five handler files (chunk.go,
// faq.go, tag.go, knowledge.go, knowledgebase.go). Each was a copy of
// the same three checks:
//
//   1. KB belongs to caller's tenant   -> grant own access
//   2. Org-shared KB                    -> grant min(share, role) cap
//   3. Shared agent carries the KB      -> grant Viewer (read-only)
//
// Putting the resolution in a route-level gin.HandlerFunc makes the
// route declaration the single source of truth for "what permission
// is required" and "where does the kb_id come from". Handlers no
// longer carry an effectiveCtxForKB / validateAndGetKnowledgeBase
// helper — the guard runs first, stashes the resolution under
// KBAccessContextKey and rewrites c.Request to carry the
// effective-tenant-ID context, then handlers just read TenantIDFromContext
// the way they always did.
//
// Plan 3's 3-D cap (tenant Viewer pinned to OrgRoleViewer) is enforced
// inside CheckTenantKBPermission itself, so the guard here just
// propagates the result.
//
// ⚠️  Tenant id has TWO surfaces on a gin.Context:
//
//   - c.Request.Context()         (read via types.TenantIDFromContext)
//   - c.Keys[TenantIDContextKey]  (read via c.Get / c.GetUint64)
//
// The guard rewrites ONLY the request context — c.Keys is intentionally
// left at the caller's own tenant so handlers that still run their own
// share resolution (currently knowledge.go / knowledgebase.go, which
// branch on the ?agent_id query) keep working unchanged. New handlers
// MUST read tenant from c.Request.Context() (the "effective" tenant
// for shared KBs); reading from c.Keys after this guard runs gives
// the caller's own tenant, which is almost always the wrong answer
// for KB-scoped routes. The FAQ / Tag / Chunk handlers in this PR
// were converted to the ctx-based read; the migration of knowledge.go
// / knowledgebase.go is a follow-up.

// KBAccess captures the result of a successful KB access resolution.
// Stashed on gin.Context under KBAccessContextKey so handlers that
// need the resolved KB / permission (e.g. to render
// my_permission in the response) can pull it without re-running the
// resolution.
type KBAccess struct {
	KnowledgeBase     *types.KnowledgeBase
	EffectiveTenantID uint64
	Permission        types.OrgMemberRole
}

// KBAccessContextKey is the gin.Context key under which a successful
// KB access resolution is stored.
const KBAccessContextKey = "rbac.kb_access"

// KBAccessFromContext returns the KBAccess stashed by the guard, if
// any. Handlers that don't care can just rely on the rewritten
// c.Request.Context() for tenant scoping.
func KBAccessFromContext(c *gin.Context) (*KBAccess, bool) {
	v, ok := c.Get(KBAccessContextKey)
	if !ok {
		return nil, false
	}
	a, ok := v.(*KBAccess)
	return a, ok
}

// KBLookup is the minimum surface ResolveKBAccess needs from the
// knowledge-base service: a single method that turns an ID into a
// KnowledgeBase pointer (or repo.ErrKnowledgeBaseNotFound). Defining
// it as a tiny dedicated interface keeps the guard testable without
// forcing test stubs to satisfy the full KnowledgeBaseService surface.
type KBLookup interface {
	GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error)
}

// KnowledgeLookup mirrors KBLookup but for resolving a knowledge id
// (document id) back to its parent KB. Used by the chunk routes whose
// URL param is a knowledge_id, not a kb_id.
type KnowledgeLookup interface {
	GetKnowledgeByIDOnly(ctx context.Context, id string) (*types.Knowledge, error)
}

// ChunkLookup mirrors KBLookup for resolving a chunk id back to its
// owning knowledge document, which then resolves to the parent KB.
// Used by the /chunks/by-id/:id routes that address chunks directly.
type ChunkLookup interface {
	GetChunkByIDOnly(ctx context.Context, id string) (*types.Chunk, error)
}

// KBIDResolver tells the guard how to find the kb_id for a given
// request. Built-in resolvers below cover the param shapes we use:
// :id, :kb_id, :kbId, :knowledge_id (-> parent KB).
//
// On error, resolvers MUST return either a 4xx apperror (bad request /
// not found) or a generic Go error for transient/internal failures;
// the guard maps the latter to 503.
type KBIDResolver func(c *gin.Context) (string, error)

// KBIDFromParam returns a resolver that reads a fixed gin param.
func KBIDFromParam(param string) KBIDResolver {
	return func(c *gin.Context) (string, error) {
		v := c.Param(param)
		if v == "" {
			return "", apperrors.NewBadRequestError("missing " + param + " in path")
		}
		return v, nil
	}
}

// KBIDFromKnowledgeIDParam reads `:knowledge_id` from the URL, looks
// up the knowledge document, and returns its KB id. Used by the chunk
// routes that address a chunk via /chunks/:knowledge_id.
//
// A genuine "not found" maps to 404; transient errors (DB hiccup,
// service unavailable) are surfaced as a plain Go error so the guard
// can return 503 instead of pretending the resource doesn't exist
// (a 404 here would also short-circuit any retry / monitoring).
func KBIDFromKnowledgeIDParam(param string, kgService KnowledgeLookup) KBIDResolver {
	return func(c *gin.Context) (string, error) {
		v := c.Param(param)
		if v == "" {
			return "", apperrors.NewBadRequestError("missing " + param + " in path")
		}
		k, err := kgService.GetKnowledgeByIDOnly(c.Request.Context(), v)
		if err != nil {
			if isResourceNotFound(err) {
				return "", apperrors.NewNotFoundError("Knowledge not found")
			}
			return "", err
		}
		if k == nil {
			return "", apperrors.NewNotFoundError("Knowledge not found")
		}
		return k.KnowledgeBaseID, nil
	}
}

// KBIDFromChunkIDParam walks chunk_id -> knowledge_id -> kb_id.
// Used by /chunks/by-id/:id routes that address a chunk directly. The
// chunk's KnowledgeBaseID is denormalised on the row, so a single
// lookup is enough — no need to chain through GetKnowledgeByIDOnly.
//
// Not-found / transient split mirrors KBIDFromKnowledgeIDParam.
func KBIDFromChunkIDParam(param string, chunkService ChunkLookup) KBIDResolver {
	return func(c *gin.Context) (string, error) {
		v := c.Param(param)
		if v == "" {
			return "", apperrors.NewBadRequestError("missing " + param + " in path")
		}
		ch, err := chunkService.GetChunkByIDOnly(c.Request.Context(), v)
		if err != nil {
			if isResourceNotFound(err) {
				return "", apperrors.NewNotFoundError("Chunk not found")
			}
			return "", err
		}
		if ch == nil {
			return "", apperrors.NewNotFoundError("Chunk not found")
		}
		if ch.KnowledgeBaseID == "" {
			// Should-never-happen on a fresh schema; on legacy rows the
			// chunk effectively isn't resolvable to a KB so the client
			// gets the same 404 they'd get for a missing chunk rather
			// than a 500 that pollutes alerting.
			logger.Warnf(c.Request.Context(),
				"[kb_access] chunk %s has empty knowledge_base_id; treating as not-found", v)
			return "", apperrors.NewNotFoundError("Chunk not found")
		}
		return ch.KnowledgeBaseID, nil
	}
}

// isResourceNotFound recognises the various "not found" sentinels we
// might see from the underlying services. Keeps the resolvers above
// from forcing every service to standardise on a single error type
// before this refactor is useful.
func isResourceNotFound(err error) bool {
	return stderrors.Is(err, apprepo.ErrKnowledgeBaseNotFound) ||
		stderrors.Is(err, ErrResourceNotFound)
}

// RequireKBAccess returns a gin.HandlerFunc that resolves KB access
// (own / org-shared / via shared agent), enforces the minimum required
// org-level permission, and on success stores the result under
// KBAccessContextKey AND rewrites c.Request.Context() to carry the
// effective tenant ID. Handlers downstream just read tenant from
// context as before.
//
// On failure the guard aborts with the appropriate HTTP status (400 /
// 401 / 404 / 403 / 503). Behaviour matches what each handler's
// effectiveCtxForKB helper used to do; the guard is what consolidates
// the repetition so a fix in the resolution order propagates to every
// gated route at once.
//
// Required permission semantics:
//   - OrgRoleViewer -> read-only routes (the agent-share fallback path
//     activates only at this level)
//   - OrgRoleEditor -> mutating routes (org-shared editor or own KB)
//   - OrgRoleAdmin  -> share-management routes (only the original
//     sharer / KB owner / Org admin should pass)
//
// When cfg.Tenant.EnableRBAC is false the guard mirrors the sibling
// role/ownership guards: it logs the would-be rejection and lets the
// request through. The point is to keep the rollout window safe — the
// guard runs full enforcement once the flag flips on, with no code
// changes elsewhere.
func RequireKBAccess(
	resolveKBID KBIDResolver,
	requiredPermission types.OrgMemberRole,
	kbService KBLookup,
	kbShareService interfaces.KBShareService,
	agentShareService interfaces.AgentShareService,
	cfg *config.Config,
) gin.HandlerFunc {
	warnOnNilConfig(cfg)
	return func(c *gin.Context) {
		kbID, err := resolveKBID(c)
		if err != nil {
			_ = c.Error(err)
			c.Abort()
			return
		}

		ctx := c.Request.Context()

		// Rollout window: enforcement off -> log the would-be check and
		// pass through. We still resolve the KB (best-effort) so the
		// effective-tenant context rewrite still happens for shared
		// KBs; that way embedding queries hit the right tenant
		// regardless of whether RBAC enforcement is active.
		enforcing := rbacEnforcementEnabled(cfg)

		access, err := resolveKBAccessOnce(ctx, c, kbID, requiredPermission, kbService, kbShareService, agentShareService)
		switch {
		case stderrors.Is(err, errKBAccessUnauthorized):
			if !enforcing {
				logger.Warnf(ctx, "[rbac] kb-access would 401 (enforcement off): kb=%s", kbID)
				c.Next()
				return
			}
			_ = c.Error(apperrors.NewUnauthorizedError("Unauthorized"))
			c.Abort()
			return
		case stderrors.Is(err, errKBAccessNotFound):
			// 404 still fires when enforcement is off — a missing KB is
			// not an authorisation event, the client genuinely asked
			// for nothing.
			_ = c.Error(apperrors.NewNotFoundError("knowledge base not found"))
			c.Abort()
			return
		case stderrors.Is(err, errKBAccessForbidden):
			if !enforcing {
				logger.Warnf(ctx, "[rbac] kb-access would 403 (enforcement off): kb=%s required=%s",
					kbID, requiredPermission)
				c.Next()
				return
			}
			_ = c.Error(apperrors.NewForbiddenError("Permission denied to access this knowledge base"))
			c.Abort()
			return
		case err != nil:
			logger.ErrorWithFields(ctx, err, nil)
			// Transient/internal -> 503 so monitoring catches the
			// underlying failure rather than a misleading 500.
			_ = c.Error(apperrors.NewServiceUnavailableError("cannot verify KB access right now"))
			c.Abort()
			return
		}

		// Stash the resolution and rewrite the request to carry the
		// effective tenant id. Handlers reading tenant from context now
		// see the source-tenant for shared KBs (so retrieval queries
		// hit the right embedding store) without having to know.
		c.Set(KBAccessContextKey, access)
		newCtx := context.WithValue(ctx, types.TenantIDContextKey, access.EffectiveTenantID)
		c.Request = c.Request.WithContext(newCtx)
		c.Next()
	}
}

// resolveKBAccessOnce performs the actual three-step resolution. Kept
// unexported and using package-private sentinel errors so the guard's
// error mapping is the only public surface.
//
// The shared-agent step honours the ?agent_id query parameter when
// present, mirroring the in-handler resolution in
// knowledgebase.go's validateAndGetKnowledgeBase: a request with a
// specific agent_id is validated against THAT agent's KB scope (mode =
// all / selected / none), not against "any shared agent". Falling
// back to "any shared agent" when agent_id is empty preserves the
// "通过智能体可见" KB list entry point.
func resolveKBAccessOnce(
	ctx context.Context,
	c *gin.Context,
	kbID string,
	requiredPermission types.OrgMemberRole,
	kbService KBLookup,
	kbShareService interfaces.KBShareService,
	agentShareService interfaces.AgentShareService,
) (*KBAccess, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, errKBAccessUnauthorized
	}
	callerTenantRole := types.TenantRoleFromContext(ctx)

	kb, err := kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		if stderrors.Is(err, apprepo.ErrKnowledgeBaseNotFound) {
			return nil, errKBAccessNotFound
		}
		return nil, err
	}
	if kb == nil {
		return nil, errKBAccessNotFound
	}

	// 1. Own KB.
	if kb.TenantID == tenantID {
		return &KBAccess{
			KnowledgeBase:     kb,
			EffectiveTenantID: tenantID,
			Permission:        types.OrgRoleAdmin,
		}, nil
	}

	// 2. Org-shared KB. Plan 3's 3-D cap is applied inside
	//    CheckTenantKBPermission; we just check the result satisfies
	//    the minimum requirement.
	if kbShareService != nil {
		permission, isShared, permErr := kbShareService.CheckTenantKBPermission(ctx, kbID, tenantID, callerTenantRole)
		if permErr == nil && isShared && permission.HasPermission(requiredPermission) {
			source, srcErr := kbShareService.GetKBSourceTenant(ctx, kbID)
			if srcErr == nil {
				logger.Infof(ctx, "[kb_access] tenant %d -> shared KB %s perm=%s source=%d",
					tenantID, kbID, permission, source)
				return &KBAccess{
					KnowledgeBase:     kb,
					EffectiveTenantID: source,
					Permission:        permission,
				}, nil
			}
		}
	}

	// 3. Shared agent that carries this KB — only ever grants read.
	if requiredPermission == types.OrgRoleViewer && agentShareService != nil {
		if access := resolveSharedAgentAccess(ctx, c, tenantID, callerTenantRole, kb, agentShareService); access != nil {
			return access, nil
		}
	}

	logger.Warnf(ctx, "[kb_access] tenant %d -> KB %s denied (required=%s)", tenantID, kbID, requiredPermission)
	return nil, errKBAccessForbidden
}

// resolveSharedAgentAccess implements the agent-share fallback,
// mirroring knowledgebase.go's in-handler logic so the guard and the
// (still-present) handler resolver agree on which requests pass.
//
//   - If ?agent_id=X is provided, validate against THAT agent's
//     KBSelectionMode (all / selected / none). A mismatch means deny;
//     we do NOT fall back to "any shared agent" because the client
//     explicitly named one (typically from a @-mention or a deep link
//     scoped to that agent).
//   - If ?agent_id is empty, allow when any shared agent reachable by
//     the caller can access this KB ("通过智能体可见" KB list entry).
func resolveSharedAgentAccess(
	ctx context.Context,
	c *gin.Context,
	tenantID uint64,
	callerTenantRole types.TenantRole,
	kb *types.KnowledgeBase,
	agentShareService interfaces.AgentShareService,
) *KBAccess {
	agentID := c.Query("agent_id")
	if agentID != "" {
		agent, err := agentShareService.GetSharedAgentForTenant(ctx, tenantID, callerTenantRole, agentID)
		if err != nil || agent == nil {
			return nil
		}
		if kb.TenantID != agent.TenantID {
			logger.Warnf(ctx, "[kb_access] shared agent tenant mismatch: kb=%s kb.tenant=%d agent.tenant=%d",
				kb.ID, kb.TenantID, agent.TenantID)
			return nil
		}
		switch agent.Config.KBSelectionMode {
		case "all":
			logger.Infof(ctx, "[kb_access] tenant %d -> KB %s via shared agent %s (mode=all)",
				tenantID, kb.ID, agentID)
			return &KBAccess{
				KnowledgeBase:     kb,
				EffectiveTenantID: kb.TenantID,
				Permission:        types.OrgRoleViewer,
			}
		case "selected":
			for _, allowedID := range agent.Config.KnowledgeBases {
				if allowedID == kb.ID {
					logger.Infof(ctx, "[kb_access] tenant %d -> KB %s via shared agent %s (mode=selected)",
						tenantID, kb.ID, agentID)
					return &KBAccess{
						KnowledgeBase:     kb,
						EffectiveTenantID: kb.TenantID,
						Permission:        types.OrgRoleViewer,
					}
				}
			}
		}
		return nil
	}

	can, err := agentShareService.TenantCanAccessKBViaSomeSharedAgent(ctx, tenantID, callerTenantRole, kb)
	if err == nil && can {
		logger.Infof(ctx, "[kb_access] tenant %d -> KB %s via some shared agent", tenantID, kb.ID)
		return &KBAccess{
			KnowledgeBase:     kb,
			EffectiveTenantID: kb.TenantID,
			Permission:        types.OrgRoleViewer,
		}
	}
	return nil
}

var (
	errKBAccessUnauthorized = stderrors.New("kb_access: unauthorized")
	errKBAccessNotFound     = stderrors.New("kb_access: not found")
	errKBAccessForbidden    = stderrors.New("kb_access: forbidden")
)
