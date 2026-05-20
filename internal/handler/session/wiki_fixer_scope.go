package session

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
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
) (*types.CustomAgent, uint64) {
	return resolveBuiltinWikiFixerTenantScope(
		ctx,
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
	agent *types.CustomAgent,
	currentTenantID uint64,
	callerTenantRole types.TenantRole,
	kbIDs []string,
	kbLookup wikiFixerKBLookup,
	kbShare wikiFixerKBSharePermission,
) (*types.CustomAgent, uint64) {
	if agent == nil || agent.ID != types.BuiltinWikiFixerID {
		return agent, 0
	}
	if currentTenantID == 0 || len(kbIDs) != 1 || kbLookup == nil || kbShare == nil {
		return agent, 0
	}

	kbID := kbIDs[0]
	kb, err := kbLookup.GetKnowledgeBaseByIDOnly(ctx, kbID)
	if err != nil {
		logger.Warnf(ctx, "wiki fixer: failed to resolve KB %s for shared scope: %v", secutils.SanitizeForLog(kbID), err)
		return agent, 0
	}
	if kb == nil {
		logger.Warnf(ctx, "wiki fixer: KB %s not found for shared scope", secutils.SanitizeForLog(kbID))
		return agent, 0
	}
	if kb.TenantID == 0 || kb.TenantID == currentTenantID {
		return agent, 0
	}

	permission, isShared, err := kbShare.CheckTenantKBPermission(ctx, kb.ID, currentTenantID, callerTenantRole)
	if err != nil {
		logger.Warnf(ctx, "wiki fixer: failed to check shared KB %s permission: %v", secutils.SanitizeForLog(kb.ID), err)
		return agent, 0
	}
	if !isShared || !permission.HasPermission(types.OrgRoleEditor) {
		return agent, 0
	}

	scopedAgent := *agent
	scopedAgent.TenantID = kb.TenantID
	logger.Infof(ctx, "wiki fixer: using shared KB source tenant %d for KB %s", kb.TenantID, secutils.SanitizeForLog(kb.ID))
	return &scopedAgent, kb.TenantID
}
