package handler

import (
	stderrors "errors"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// resolvedKBAccess reuses only a grant for this caller and this resource. A
// Viewer grant cannot satisfy an Editor check after the tenant context switches.
func resolvedKBAccess(c *gin.Context, kbID string, required types.OrgMemberRole) (*access.KBAccess, bool) {
	grant, ok := middleware.KBAccessFromContext(c)
	return grant, ok && grant.KnowledgeBase != nil && grant.KnowledgeBase.ID == kbID &&
		grant.Caller == middleware.KBAccessRequest(c).Caller &&
		access.HasKBGrant(grant.WithGrant(c.Request.Context()), kbID, grant.EffectiveTenantID, required)
}

// resolveHandlerKBAccess is also used by body/query-based endpoints that cannot
// resolve a single KB in route middleware. It always enforces access, preserving
// the handler checks even when the middleware's RBAC rollout flag is disabled.
func resolveHandlerKBAccess(c *gin.Context, kbID string, kbService middleware.KBLookup,
	shares interfaces.KBShareService, agents interfaces.AgentShareService,
) (*access.KBAccess, error) {
	return resolveHandlerKBAccessFor(c, kbID, kbService, shares, agents, types.OrgRoleViewer)
}

func resolveHandlerKBAccessFor(c *gin.Context, kbID string, kbService middleware.KBLookup,
	shares interfaces.KBShareService, agents interfaces.AgentShareService, required types.OrgMemberRole,
) (*access.KBAccess, error) {
	ctx := c.Request.Context()
	request := middleware.KBAccessRequest(c)
	if request.Caller.TenantID == 0 {
		return nil, apperrors.NewUnauthorizedError("Unauthorized")
	}
	if kbID == "" {
		return nil, apperrors.NewBadRequestError("Knowledge base ID cannot be empty")
	}
	if err := requireTenantAPIKeyKnowledgeBase(ctx, kbID); err != nil {
		return nil, err
	}
	if grant, ok := resolvedKBAccess(c, kbID, required); ok {
		return grant, nil
	}
	kb, err := kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		if stderrors.Is(err, repository.ErrKnowledgeBaseNotFound) {
			return nil, apperrors.NewNotFoundError("knowledge base not found")
		}
		logger.ErrorWithFields(ctx, err, nil)
		return nil, apperrors.NewInternalServerError(err.Error())
	}
	grant, err := access.ResolveKB(ctx, request, kb, required, shares, agents)
	if err == nil {
		// Preserve authorization for body-based routes without changing the
		// execution tenant until the handler performs its resource operation.
		c.Request = c.Request.WithContext(grant.WithGrant(ctx))
	}
	return grant, kbAccessHTTPError(err)
}

func kbAccessHTTPError(err error) error {
	switch {
	case stderrors.Is(err, access.ErrUnauthorized):
		return apperrors.NewUnauthorizedError("Unauthorized")
	case stderrors.Is(err, access.ErrNotFound):
		return apperrors.NewNotFoundError("knowledge base not found")
	case stderrors.Is(err, access.ErrForbidden):
		return apperrors.NewForbiddenError("Permission denied to access this knowledge base")
	case stderrors.Is(err, access.ErrInvalidAgentSource):
		return apperrors.NewBadRequestError("invalid agent_source_tenant_id")
	default:
		return err
	}
}
