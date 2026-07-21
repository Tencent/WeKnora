package session

import (
	"context"
	stderrors "errors"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

type wikiFixerKBLookup interface {
	GetKnowledgeBaseByIDOnly(ctx context.Context, id string) (*types.KnowledgeBase, error)
}

type wikiFixerKBSharePermission interface {
	CheckTenantKBPermission(ctx context.Context, kbID string, callerTenantID uint64, callerTenantRole types.TenantRole) (types.OrgMemberRole, bool, error)
}

func (h *Handler) resolveWikiFixerTenantScope(
	ctx context.Context,
	agent *types.CustomAgent,
	currentTenantID uint64,
	callerTenantRole types.TenantRole,
	kbIDs []string,
) (*types.CustomAgent, uint64, error) {
	return resolveBuiltinWikiFixerTenantScope(
		ctx,
		h.config,
		agent,
		currentTenantID,
		callerTenantRole,
		kbIDs,
		h.knowledgebaseService,
		h.kbShareService,
	)
}

func resolveBuiltinWikiFixerTenantScope(
	ctx context.Context,
	cfg *config.Config,
	agent *types.CustomAgent,
	currentTenantID uint64,
	callerTenantRole types.TenantRole,
	kbIDs []string,
	kbLookup wikiFixerKBLookup,
	kbShare wikiFixerKBSharePermission,
) (*types.CustomAgent, uint64, error) {
	if agent == nil || agent.ID != types.BuiltinWikiFixerID {
		return agent, 0, nil
	}
	if currentTenantID == 0 {
		return agent, 0, apperrors.NewUnauthorizedError("workspace context is required for wiki fixer")
	}
	if len(kbIDs) != 1 {
		return agent, 0, apperrors.NewBadRequestError("wiki fixer requires exactly one knowledge base")
	}
	if kbLookup == nil {
		return agent, 0, apperrors.NewServiceUnavailableError("cannot verify wiki knowledge base")
	}

	kbID := kbIDs[0]
	kb, err := kbLookup.GetKnowledgeBaseByIDOnly(ctx, kbID)
	if err != nil {
		logger.Warnf(ctx, "wiki fixer: failed to resolve KB %s for shared scope: %v", secutils.SanitizeForLog(kbID), err)
		if stderrors.Is(err, repository.ErrKnowledgeBaseNotFound) {
			return agent, 0, apperrors.NewNotFoundError("knowledge base not found")
		}
		return agent, 0, apperrors.NewServiceUnavailableError("cannot verify wiki knowledge base")
	}
	if kb == nil {
		logger.Warnf(ctx, "wiki fixer: KB %s not found for shared scope", secutils.SanitizeForLog(kbID))
		return agent, 0, apperrors.NewNotFoundError("knowledge base not found")
	}
	if !kb.IsWikiEnabled() {
		return agent, 0, apperrors.NewBadRequestError("wiki fixer requires a wiki-enabled knowledge base")
	}
	if scope, ok := types.TenantAPIKeyScopeFromContext(ctx); ok &&
		!scope.FullAccess && !scope.HasCapability(types.APIKeyCapabilityIngest) {
		return agent, 0, apperrors.NewForbiddenError("API key requires ingest capability for wiki fixer")
	}
	if kb.TenantID == currentTenantID {
		if err := middleware.EvaluateOwnershipOrRole(ctx, cfg, types.TenantRoleAdmin, kb.CreatorID, nil); err != nil {
			return agent, 0, mapWikiFixerAccessError(err)
		}
		return agent, 0, nil
	}
	if kb.TenantID == 0 || kbShare == nil {
		return agent, 0, apperrors.NewForbiddenError("no edit permission for wiki fixer knowledge base")
	}

	permission, isShared, err := kbShare.CheckTenantKBPermission(ctx, kb.ID, currentTenantID, callerTenantRole)
	if err != nil {
		logger.Warnf(ctx, "wiki fixer: failed to check shared KB %s permission: %v", secutils.SanitizeForLog(kb.ID), err)
		return agent, 0, apperrors.NewServiceUnavailableError("cannot verify wiki knowledge base permission")
	}
	if !isShared || !permission.HasPermission(types.OrgRoleEditor) {
		return agent, 0, apperrors.NewForbiddenError("no edit permission for wiki fixer knowledge base")
	}

	scopedAgent := *agent
	scopedAgent.TenantID = kb.TenantID
	logger.Infof(ctx, "wiki fixer: using shared KB source tenant %d for KB %s", kb.TenantID, secutils.SanitizeForLog(kb.ID))
	return &scopedAgent, kb.TenantID, nil
}

func mapWikiFixerAccessError(err error) error {
	switch {
	case stderrors.Is(err, middleware.ErrResourceNotFound):
		return apperrors.NewNotFoundError("knowledge base not found")
	case stderrors.Is(err, middleware.ErrOwnershipForbidden):
		return apperrors.NewForbiddenError("no edit permission for wiki fixer knowledge base")
	default:
		return apperrors.NewServiceUnavailableError("cannot verify wiki knowledge base permission")
	}
}
