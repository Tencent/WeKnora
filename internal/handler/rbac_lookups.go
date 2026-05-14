package handler

import (
	"errors"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// Per-resource creator-id resolvers used by middleware.RequireOwnershipOrRole.
// The route registration wires these as closures so they capture the handler's
// service dependencies; the middleware just calls them with the gin.Context
// and gets back one of:
//
//   - (creatorID, nil)            : ownership match check decides
//   - ("",        nil)            : tenant-owned (legacy or built-in)
//   - ("", ErrResourceNotFound)   : :id doesn't resolve in this tenant;
//                                   middleware passes through so the
//                                   handler can issue a real 404
//   - ("", <other err>)           : transient/unexpected failure;
//                                   middleware will 503 the request
//
// Tenant scoping is enforced INSIDE the lookup: even if the underlying
// `Get*ByID` repo call is unscoped, the lookup re-checks `tenant_id`
// against the caller's context. This stops a creator-match shortcut
// from leaking access to a same-ID resource in a different tenant.

// KBCreatorLookup resolves :id -> KnowledgeBase.CreatorID, scoped to
// the caller's tenant. Used by all per-KB mutating routes.
func (h *KnowledgeBaseHandler) KBCreatorLookup(c *gin.Context) (string, error) {
	id := c.Param("id")
	if id == "" {
		return "", errors.New("missing :id param for KB creator lookup")
	}
	ctx := c.Request.Context()
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		// 没有租户上下文意味着 auth 中间件未完成；当作 lookup 失败让上层 503，
		// 而不是 silently 走 fail-open 给一个不该有的访问。
		return "", errors.New("tenant context missing")
	}
	kb, err := h.service.GetKnowledgeBaseByID(ctx, id)
	if err != nil {
		if errors.Is(err, apprepo.ErrKnowledgeBaseNotFound) {
			return "", middleware.ErrResourceNotFound
		}
		return "", err
	}
	if kb == nil {
		return "", middleware.ErrResourceNotFound
	}
	// 显式重校验租户：repo.GetKnowledgeBaseByID 不带 tenant 过滤，
	// 万一未来 :id 被攻击者从他人租户 UUID 试探到，也不会借由
	// "ownership match" 通过中间件。
	if kb.TenantID != tenantID {
		return "", middleware.ErrResourceNotFound
	}
	return kb.CreatorID, nil
}

// AgentCreatorLookup resolves :id -> CustomAgent.CreatedBy. Built-in
// agents (IsBuiltin == true) are tenant-owned across the board: they
// belong to the tenant rather than to any one user, so we return
// ("", nil) and let the role check decide. The same holds for legacy
// rows whose CreatedBy was never populated.
//
// The underlying GetAgentByID already scopes to the caller's tenant,
// but we keep an explicit defence-in-depth check here in case future
// refactors loosen the service-layer scope.
func (h *CustomAgentHandler) AgentCreatorLookup(c *gin.Context) (string, error) {
	id := c.Param("id")
	if id == "" {
		return "", errors.New("missing :id param for agent creator lookup")
	}
	ctx := c.Request.Context()
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return "", errors.New("tenant context missing")
	}
	agent, err := h.service.GetAgentByID(ctx, id)
	if err != nil {
		if errors.Is(err, service.ErrAgentNotFound) ||
			errors.Is(err, apprepo.ErrCustomAgentNotFound) {
			return "", middleware.ErrResourceNotFound
		}
		return "", err
	}
	if agent == nil {
		return "", middleware.ErrResourceNotFound
	}
	if agent.TenantID != tenantID {
		return "", middleware.ErrResourceNotFound
	}
	if agent.IsBuiltin {
		return "", nil
	}
	return agent.CreatedBy, nil
}

// Compile-time guards: the methods must satisfy middleware.CreatorLookup
// so route wiring stays type-safe even if a signature drifts.
var (
	_ middleware.CreatorLookup = (*KnowledgeBaseHandler)(nil).KBCreatorLookup
	_ middleware.CreatorLookup = (*CustomAgentHandler)(nil).AgentCreatorLookup
	_ middleware.CreatorLookup = (*KnowledgeHandler)(nil).KBCreatorLookupFromKnowledgeID
	_ middleware.CreatorLookup = (*ChunkHandler)(nil).KBCreatorLookupFromKnowledgeIDParam
	_ middleware.CreatorLookup = (*WikiPageHandler)(nil).KBCreatorLookupFromKBPath
)

// KBCreatorLookupFromKnowledgeID resolves :id (a Knowledge ID) to the
// CreatorID of the *owning KB*, scoped to the caller's tenant. Used by
// per-knowledge mutating routes (PR 5, #1303) so a Contributor who owns
// the KB can edit/delete any of its documents while a Contributor who
// merely belongs to the tenant cannot. Built-in / legacy KBs without a
// CreatorID surface as ("", nil) and stay Admin-gated.
//
// The chain (knowledge_id -> kb_id -> KB.CreatorID) lives in the
// service layer (KnowledgeService.GetOwningKBCreatorID) so this lookup
// stays a pure adapter — same shape as KBCreatorLookup.
func (h *KnowledgeHandler) KBCreatorLookupFromKnowledgeID(c *gin.Context) (string, error) {
	knowledgeID := c.Param("id")
	if knowledgeID == "" {
		return "", errors.New("missing :id param for knowledge owner lookup")
	}
	return resolveKBCreatorByKnowledgeID(c, h.kgService, knowledgeID)
}

// KBCreatorLookupFromKnowledgeIDParam mirrors KBCreatorLookupFromKnowledgeID
// for chunk routes, which use :knowledge_id rather than :id. The
// chunks.DELETE("/by-id/:id/questions") route addresses a chunk by its
// own id (not a knowledge id), so it stays Contributor-gated and is
// not wired through this lookup — see router.go for the carve-out.
func (h *ChunkHandler) KBCreatorLookupFromKnowledgeIDParam(c *gin.Context) (string, error) {
	knowledgeID := c.Param("knowledge_id")
	if knowledgeID == "" {
		return "", errors.New("missing :knowledge_id param for chunk owner lookup")
	}
	return resolveKBCreatorByKnowledgeID(c, h.kgService, knowledgeID)
}

// KBCreatorLookupFromKBPath resolves :kb_id (used by wiki routes) to
// KnowledgeBase.CreatorID. No chain hop — the wiki page URL already
// carries the owning KB id, so we go straight to the KB service. The
// tenant defence-in-depth re-check stays the same as KBCreatorLookup:
// repo.GetKnowledgeBaseByID is unscoped, so we explicitly compare
// against the context tenant.
func (h *WikiPageHandler) KBCreatorLookupFromKBPath(c *gin.Context) (string, error) {
	kbID := c.Param("kb_id")
	if kbID == "" {
		return "", errors.New("missing :kb_id param for wiki owner lookup")
	}
	ctx := c.Request.Context()
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return "", errors.New("tenant context missing")
	}
	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		if errors.Is(err, apprepo.ErrKnowledgeBaseNotFound) {
			return "", middleware.ErrResourceNotFound
		}
		return "", err
	}
	if kb == nil {
		return "", middleware.ErrResourceNotFound
	}
	if kb.TenantID != tenantID {
		return "", middleware.ErrResourceNotFound
	}
	return kb.CreatorID, nil
}

// resolveKBCreatorByKnowledgeID is the shared body for the knowledge /
// chunk lookups. The service-layer chain helper does the tenant-scoped
// fetch (knowledge -> KB) and returns repository sentinel errors;
// translating those to middleware.ErrResourceNotFound is the lookup's
// job, mirroring what KBCreatorLookup does for plain :id routes.
func resolveKBCreatorByKnowledgeID(
	c *gin.Context,
	kgService interfaces.KnowledgeService,
	knowledgeID string,
) (string, error) {
	ctx := c.Request.Context()
	if _, ok := types.TenantIDFromContext(ctx); !ok {
		// Same fail-closed reasoning as KBCreatorLookup: no tenant
		// context means auth didn't complete, and we'd rather have the
		// caller see a 503 than a silent fail-open.
		return "", errors.New("tenant context missing")
	}
	creatorID, err := kgService.GetOwningKBCreatorID(ctx, knowledgeID)
	if err != nil {
		if errors.Is(err, apprepo.ErrKnowledgeNotFound) ||
			errors.Is(err, apprepo.ErrKnowledgeBaseNotFound) {
			return "", middleware.ErrResourceNotFound
		}
		return "", err
	}
	return creatorID, nil
}
