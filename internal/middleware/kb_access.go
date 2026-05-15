package middleware

import (
	"context"
	stderrors "errors"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
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
func KBIDFromKnowledgeIDParam(param string, kgService KnowledgeLookup) KBIDResolver {
	return func(c *gin.Context) (string, error) {
		v := c.Param(param)
		if v == "" {
			return "", apperrors.NewBadRequestError("missing " + param + " in path")
		}
		k, err := kgService.GetKnowledgeByIDOnly(c.Request.Context(), v)
		if err != nil || k == nil {
			return "", apperrors.NewNotFoundError("Knowledge not found")
		}
		return k.KnowledgeBaseID, nil
	}
}

// KBIDFromChunkIDParam walks chunk_id -> knowledge_id -> kb_id.
// Used by /chunks/by-id/:id routes that address a chunk directly. The
// chunk's KnowledgeBaseID is denormalised on the row, so a single
// lookup is enough — no need to chain through GetKnowledgeByIDOnly.
func KBIDFromChunkIDParam(param string, chunkService ChunkLookup) KBIDResolver {
	return func(c *gin.Context) (string, error) {
		v := c.Param(param)
		if v == "" {
			return "", apperrors.NewBadRequestError("missing " + param + " in path")
		}
		ch, err := chunkService.GetChunkByIDOnly(c.Request.Context(), v)
		if err != nil || ch == nil {
			return "", apperrors.NewNotFoundError("Chunk not found")
		}
		if ch.KnowledgeBaseID == "" {
			// Some old chunks may not carry the denormalised KB id;
			// this is a should-never-happen branch on a fresh schema.
			return "", apperrors.NewInternalServerError("chunk missing knowledge_base_id")
		}
		return ch.KnowledgeBaseID, nil
	}
}

// RequireKBAccess returns a gin.HandlerFunc that resolves KB access
// (own / org-shared / via shared agent), enforces the minimum required
// org-level permission, and on success stores the result under
// KBAccessContextKey AND rewrites c.Request.Context() to carry the
// effective tenant ID. Handlers downstream just read tenant from
// context as before.
//
// On failure the guard aborts with the appropriate HTTP status (400 /
// 401 / 404 / 403 / 500). Behaviour matches what each handler's
// effectiveCtxForKB helper used to do; the guard is what consolidates
// the repetition so a fix in the resolution order propagates to every
// gated route at once.
//
// Required permission semantics:
//   - OrgRoleViewer -> read-only routes (the agent-share fallback path
//                       activates only at this level)
//   - OrgRoleEditor -> mutating routes (org-shared editor or own KB)
//   - OrgRoleAdmin  -> share-management routes (only the original
//                       sharer / KB owner / Org admin should pass)
func RequireKBAccess(
	resolveKBID KBIDResolver,
	requiredPermission types.OrgMemberRole,
	kbService KBLookup,
	kbShareService interfaces.KBShareService,
	agentShareService interfaces.AgentShareService,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		kbID, err := resolveKBID(c)
		if err != nil {
			_ = c.Error(err)
			c.Abort()
			return
		}

		ctx := c.Request.Context()
		access, err := resolveKBAccessOnce(ctx, kbID, requiredPermission, kbService, kbShareService, agentShareService)
		switch {
		case stderrors.Is(err, errKBAccessUnauthorized):
			_ = c.Error(apperrors.NewUnauthorizedError("Unauthorized"))
			c.Abort()
			return
		case stderrors.Is(err, errKBAccessNotFound):
			_ = c.Error(apperrors.NewNotFoundError("knowledge base not found"))
			c.Abort()
			return
		case stderrors.Is(err, errKBAccessForbidden):
			_ = c.Error(apperrors.NewForbiddenError("Permission denied to access this knowledge base"))
			c.Abort()
			return
		case err != nil:
			logger.ErrorWithFields(ctx, err, nil)
			_ = c.Error(apperrors.NewInternalServerError(err.Error()))
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
func resolveKBAccessOnce(
	ctx context.Context,
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
		can, err := agentShareService.TenantCanAccessKBViaSomeSharedAgent(ctx, tenantID, callerTenantRole, kb)
		if err == nil && can {
			logger.Infof(ctx, "[kb_access] tenant %d -> KB %s via shared agent", tenantID, kbID)
			return &KBAccess{
				KnowledgeBase:     kb,
				EffectiveTenantID: kb.TenantID,
				Permission:        types.OrgRoleViewer,
			}, nil
		}
	}

	logger.Warnf(ctx, "[kb_access] tenant %d -> KB %s denied (required=%s)", tenantID, kbID, requiredPermission)
	return nil, errKBAccessForbidden
}

var (
	errKBAccessUnauthorized = stderrors.New("kb_access: unauthorized")
	errKBAccessNotFound     = stderrors.New("kb_access: not found")
	errKBAccessForbidden    = stderrors.New("kb_access: forbidden")
)
