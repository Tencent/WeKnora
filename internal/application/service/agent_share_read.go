package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// resolveAgentForSharedRead resolves agentID for callerTenantID through an
// organization share and returns the agent as seen by the source tenant. It
// exists for read paths (e.g. starter questions, embed channel listing) that
// must work for tenants an agent has been shared with, while mutating paths
// keep enforcing ownership.
//
// Every miss — no share, dangling share, or agent gone from the source
// tenant — collapses to ErrAgentNotFound: callers only reach this helper
// after their own-tenant lookup already missed, so surfacing share-internals
// as distinct errors would leak existence information without helping the
// caller. Read paths are best-effort; a transient share-repo failure reads
// as "not shared with you".
func resolveAgentForSharedRead(
	ctx context.Context,
	shareRepo interfaces.AgentShareRepository,
	fetchAgent func(ctx context.Context, agentID string, tenantID uint64) (*types.CustomAgent, error),
	callerTenantID uint64,
	agentID string,
) (*types.CustomAgent, error) {
	if shareRepo == nil || fetchAgent == nil || callerTenantID == 0 || agentID == "" {
		return nil, ErrAgentNotFound
	}
	share, err := shareRepo.GetShareByAgentIDForTenant(ctx, callerTenantID, agentID, callerTenantID)
	if err != nil {
		if err != repository.ErrAgentShareNotFound {
			logger.Warnf(ctx, "[shared-agent-read] share lookup failed for agent %s: %v", agentID, err)
		}
		return nil, ErrAgentNotFound
	}
	if share == nil || share.SourceTenantID == 0 || share.SourceTenantID == callerTenantID {
		return nil, ErrAgentNotFound
	}
	agent, err := fetchAgent(ctx, agentID, share.SourceTenantID)
	if err != nil {
		return nil, ErrAgentNotFound
	}
	if agent == nil {
		return nil, ErrAgentNotFound
	}
	agent.EnsureDefaults()
	return agent, nil
}
