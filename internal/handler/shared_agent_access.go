package handler

import (
	stderrors "errors"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// resolveSharedAgentForRequest is the identity/share boundary for KB listing,
// document search and batch restoration. KB-scope enforcement follows lookup.
func resolveSharedAgentForRequest(
	c *gin.Context,
	agentID string,
	agents access.SharedAgentLookup,
) (*types.CustomAgent, error) {
	ctx := c.Request.Context()
	request := middleware.KBAccessRequest(c)
	userID := request.Caller.UserID
	if request.Caller.TenantID == 0 || userID == "" {
		return nil, apperrors.NewUnauthorizedError("Unauthorized")
	}
	source, err := types.ParseAgentSourceTenantID(request.AgentSourceTenantID)
	if err != nil {
		return nil, apperrors.NewBadRequestError(err.Error())
	}
	if agents == nil {
		return nil, apperrors.NewForbiddenError("no permission for this shared agent")
	}
	agent, err := agents.GetSharedAgentForTenant(ctx, request.Caller.TenantID, request.Caller.Role, agentID, source)
	if err != nil {
		if stderrors.Is(err, service.ErrAgentShareNotFound) || stderrors.Is(err, service.ErrAgentSharePermission) ||
			stderrors.Is(err, service.ErrAgentNotFoundForShare) {
			return nil, apperrors.NewForbiddenError("no permission for this shared agent")
		}
		logger.ErrorWithFields(ctx, err, nil)
		return nil, apperrors.NewInternalServerError("Failed to verify shared agent access")
	}
	if agent == nil || agent.TenantID == 0 || (source != 0 && agent.TenantID != source) {
		return nil, apperrors.NewForbiddenError("no permission for this shared agent")
	}
	return agent, nil
}

func filterKnowledgeByAgentScope(knowledges []*types.Knowledge, scope types.SharedAgentKBScope) []*types.Knowledge {
	filtered := make([]*types.Knowledge, 0, len(knowledges))
	for _, knowledge := range knowledges {
		if knowledge != nil && scope.Allows(knowledge.KnowledgeBaseID, knowledge.TenantID) {
			filtered = append(filtered, knowledge)
		}
	}
	return filtered
}

// Scope filtering is shared by @KB listing and @file search. Capability checks
// constrain dynamic "all" selections; explicit selections keep their existing
// behavior. This filter does not replace API-key scope checks at the endpoint.
func filterKnowledgeBasesForSharedAgent(kbs []*types.KnowledgeBase, agent *types.CustomAgent) []*types.KnowledgeBase {
	scope := types.NewSharedAgentKBScope(agent)
	filtered := make([]*types.KnowledgeBase, 0, len(kbs))
	if scope.IsEmpty() {
		return filtered
	}
	filter := tools.DeriveKBFilterForAgent(agent.Config.AgentMode, agent.Config.AllowedTools)
	for _, kb := range kbs {
		if kb == nil || !scope.Allows(kb.ID, kb.TenantID) {
			continue
		}
		if scope.IsAll() && !filter.IsEmpty() &&
			!tools.KBSatisfiesAgentRequirements(kb.Capabilities(), agent.Config.AgentMode, agent.Config.AllowedTools) {
			continue
		}
		filtered = append(filtered, kb)
	}
	return filtered
}
