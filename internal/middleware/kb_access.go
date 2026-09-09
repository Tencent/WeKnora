package middleware

import (
	"context"
	stderrors "errors"

	"github.com/Tencent/WeKnora/internal/application/access"
	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// KBAccess aliases the shared policy result. This adapter resolves route
// parameters, applies the RBAC rollout switch, and scopes downstream services
// to the resource tenant. Gin's tenant key retains the authenticated caller.
type KBAccess = access.KBAccess

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
	return a, ok && a != nil
}

// KBLookup is the minimum surface the guard needs from the
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
	// ErrChunkNotFound is defined in the repository layer and aliased by the
	// service; match the canonical repo sentinel so this predicate depends
	// only on the repository package (KB / Knowledge / Chunk are all here).
	return stderrors.Is(err, apprepo.ErrKnowledgeBaseNotFound) ||
		stderrors.Is(err, apprepo.ErrKnowledgeNotFound) ||
		stderrors.Is(err, apprepo.ErrChunkNotFound) ||
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
		if err := types.AuthorizeTenantAPIKeyKnowledgeBases(ctx, kbID); err != nil {
			_ = c.Error(err)
			c.Abort()
			return
		}

		// Rollout window: enforcement off -> log the would-be check and
		// pass through. We still resolve the KB (best-effort) so the
		// effective-tenant context rewrite still happens for shared
		// KBs; that way embedding queries hit the right tenant
		// regardless of whether RBAC enforcement is active.
		enforcing := rbacEnforcementEnabled(cfg)

		grant, err := resolveKBAccess(ctx, c, kbID, requiredPermission, kbService, kbShareService, agentShareService)
		switch {
		case stderrors.Is(err, access.ErrUnauthorized):
			if !enforcing {
				logger.Warnf(ctx, "[rbac] kb-access would 401 (enforcement off): kb=%s", kbID)
				c.Next()
				return
			}
			_ = c.Error(apperrors.NewUnauthorizedError("Unauthorized"))
			c.Abort()
			return
		case stderrors.Is(err, access.ErrNotFound):
			// 404 still fires when enforcement is off — a missing KB is
			// not an authorisation event, the client genuinely asked
			// for nothing.
			_ = c.Error(apperrors.NewNotFoundError("knowledge base not found"))
			c.Abort()
			return
		case stderrors.Is(err, access.ErrForbidden):
			if !enforcing {
				logger.Warnf(ctx, "[rbac] kb-access would 403 (enforcement off): kb=%s required=%s",
					kbID, requiredPermission)
				c.Next()
				return
			}
			_ = c.Error(apperrors.NewForbiddenError("Permission denied to access this knowledge base"))
			c.Abort()
			return
		case stderrors.Is(err, access.ErrInvalidAgentSource):
			_ = c.Error(apperrors.NewBadRequestError("invalid agent_source_tenant_id"))
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
		c.Set(KBAccessContextKey, grant)
		newCtx := grant.Context(ctx)
		c.Request = c.Request.WithContext(newCtx)
		c.Next()
	}
}

// KBAccessRequest captures caller identity before a guard scopes the request
// context to a shared resource. The stored grant also supports nested guards.
func KBAccessRequest(c *gin.Context) access.KBRequest {
	ctx := c.Request.Context()
	caller := types.CallerFromContext(ctx)
	if _, captured := ctx.Value(types.CallerContextKey).(types.Caller); !captured {
		// Compatibility for direct handler invocations that only seed Gin.
		if tenantID, ok := c.Get(types.TenantIDContextKey.String()); ok {
			caller.TenantID, _ = tenantID.(uint64)
		} else if grant, found := KBAccessFromContext(c); found {
			caller = grant.Caller
		}
		if userID, ok := c.Get(types.UserIDContextKey.String()); ok {
			caller.UserID, _ = userID.(string)
		}
	}
	return access.KBRequest{
		Caller:              caller,
		AgentID:             c.Query("agent_id"),
		AgentSourceTenantID: c.Query(types.AgentSourceTenantIDParam),
	}
}

func resolveKBAccess(
	ctx context.Context,
	c *gin.Context,
	kbID string,
	requiredPermission types.OrgMemberRole,
	kbService KBLookup,
	kbShareService interfaces.KBShareService,
	agentShareService interfaces.AgentShareService,
) (*KBAccess, error) {
	request := KBAccessRequest(c)
	if request.Caller.TenantID == 0 {
		return nil, access.ErrUnauthorized
	}
	kb, err := kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		if stderrors.Is(err, apprepo.ErrKnowledgeBaseNotFound) {
			return nil, access.ErrNotFound
		}
		return nil, err
	}
	return access.ResolveKB(ctx, request, kb, requiredPermission, kbShareService, agentShareService)
}
