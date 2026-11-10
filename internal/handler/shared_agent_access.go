package handler

import (
	stderrors "errors"
	"strings"

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

// sharedAgentPickerScope resolves the shared agent a chat-input picker is
// filling for, or (nil, nil) when the request names none.
//
// The @Skill and @MCP pickers offer what the SELECTED agent can invoke, and a
// shared agent invokes its owner's skills and MCP services — its sandbox config
// and service ids do not exist in the caller's own workspace, so listing them
// there returns nothing. A request that names no source workspace is an
// ordinary own-agent request and keeps reading the caller's own resources.
//
// Only an explicit source workspace enters the share path. Built-in agent ids
// exist in every workspace, so falling back to a share lookup without one would
// let an org member take over the caller's default agent, exactly as
// session.Handler.resolveAgent documents.
func sharedAgentPickerScope(
	c *gin.Context,
	agents access.SharedAgentLookup,
) (*types.CustomAgent, error) {
	agentID := strings.TrimSpace(c.Query("agent_id"))
	source, err := types.ParseAgentSourceTenantID(c.Query(types.AgentSourceTenantIDParam))
	if err != nil {
		return nil, apperrors.NewBadRequestError(err.Error())
	}
	if agentID == "" || source == 0 {
		return nil, nil
	}
	return resolveSharedAgentForRequest(c, agentID, agents)
}

// sharedAgentSkillScope reports which installed skills an already-authorized
// shared agent may invoke. It mirrors sessionService.configureSkillsFromAgent:
// an unknown or empty mode disables skills, so an agent whose owner turned them
// off never exposes the workspace's skill inventory to a borrower.
//
// A nil allowed set means "every installed skill"; enabled is false when no
// skill can be offered at all, which is not the same thing.
func sharedAgentSkillScope(agent *types.CustomAgent) (allowed map[string]bool, enabled bool) {
	if agent == nil {
		return nil, false
	}
	switch agent.Config.SkillsSelectionMode {
	case "all":
		return nil, true
	case "selected":
		if len(agent.Config.SelectedSkills) == 0 {
			return nil, false
		}
		allowed = make(map[string]bool, len(agent.Config.SelectedSkills))
		for _, name := range agent.Config.SelectedSkills {
			if name != "" {
				allowed[name] = true
			}
		}
		return allowed, len(allowed) > 0
	default:
		return nil, false
	}
}

// sharedAgentMCPScope returns the MCP services a borrower may @mention on an
// already-authorized shared agent.
//
// It is deliberately the agent's explicit preset and nothing else, because that
// is exactly what resolvePerRequestMCPScope accepts for a shared agent: it
// intersects the mention against Config.MCPServices, which is empty under the
// "all" and "none" modes. Offering the owner's whole inventory under "all"
// would put services in the picker that the backend silently drops.
func sharedAgentMCPScope(agent *types.CustomAgent) []string {
	if agent == nil || agent.Config.MCPSelectionMode != "selected" {
		return nil
	}
	ids := make([]string, 0, len(agent.Config.MCPServices))
	for _, id := range agent.Config.MCPServices {
		if strings.TrimSpace(id) != "" {
			ids = append(ids, id)
		}
	}
	return ids
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
