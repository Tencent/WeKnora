package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Covers the shared-agent read path of embed channel listing (issue #2957):
// tenants an agent was shared with may list its channels; publish tokens
// never appear in responses and mutations stay owner-only.

// sharedEmbedAgentService stubs the agent service: the agent lives in
// tenant 2, while the caller resolves agents under tenant 1.
type sharedEmbedAgentService struct {
	interfaces.CustomAgentService
	agent *types.CustomAgent
}

func (s *sharedEmbedAgentService) GetAgentByID(
	_ context.Context, _ string,
) (*types.CustomAgent, error) {
	// The caller's tenant (1) has no own copy of the agent; surface the
	// same error the real service produces in that case.
	if s.agent == nil {
		return nil, ErrAgentNotFound
	}
	return s.agent, nil
}

func (s *sharedEmbedAgentService) GetAgentByIDAndTenant(
	_ context.Context, _ string, tenantID uint64,
) (*types.CustomAgent, error) {
	if s.agent == nil || s.agent.TenantID != tenantID {
		return nil, ErrAgentNotFound
	}
	return s.agent, nil
}

// sharedEmbedShareRepo yields the tenant-1-visible share for the agent.
type sharedEmbedShareRepo struct {
	interfaces.AgentShareRepository
	share *types.AgentShare
}

func (r *sharedEmbedShareRepo) GetShareByAgentIDForTenant(
	_ context.Context, tenantID uint64, _ string, excludeTenantID uint64,
) (*types.AgentShare, error) {
	if r.share == nil ||
		r.share.SourceTenantID == tenantID ||
		r.share.SourceTenantID == excludeTenantID {
		return nil, ErrAgentNotFound
	}
	return r.share, nil
}

// sharedEmbedRepo records the tenant the channel listing ran under.
type sharedEmbedRepo struct {
	interfaces.EmbedChannelRepository
	listedTenant uint64
	listCalls    int
}

func (r *sharedEmbedRepo) ListByAgent(
	_ context.Context, tenantID uint64, _ string,
) ([]*types.EmbedChannel, error) {
	r.listedTenant = tenantID
	r.listCalls++
	return nil, nil
}

func sharedAgentFixture() *types.CustomAgent {
	return &types.CustomAgent{ID: "agent-shared", TenantID: 2}
}

func newSharedListService(
	t *testing.T,
	agent *types.CustomAgent,
	shareRepo interfaces.AgentShareRepository,
) (*embedChannelService, *sharedEmbedRepo) {
	t.Helper()
	repo := &sharedEmbedRepo{}
	svc := &embedChannelService{
		repo:           repo,
		agentService:   &sharedEmbedAgentService{agent: agent},
		agentShareRepo: shareRepo,
	}
	return svc, repo
}

func TestListByAgent_SharedAgentListsSourceTenantChannels(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	svc, repo := newSharedListService(t, sharedAgentFixture(),
		&sharedEmbedShareRepo{share: &types.AgentShare{SourceTenantID: 2}})

	rows, err := svc.ListByAgent(ctx, 1, "agent-shared")
	require.NoError(t, err)
	assert.Empty(t, rows)
	assert.Equal(t, 1, repo.listCalls)
	assert.Equal(t, uint64(2), repo.listedTenant, "channel listing must run under the source tenant")
}

func TestListByAgent_OwnedAgentKeepsCallerTenant(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	agent := sharedAgentFixture()
	agent.TenantID = 1
	svc, repo := newSharedListService(t, agent,
		&sharedEmbedShareRepo{share: &types.AgentShare{SourceTenantID: 2}})

	_, err := svc.ListByAgent(ctx, 1, "agent-shared")
	require.NoError(t, err)
	assert.Equal(t, uint64(1), repo.listedTenant)
}

func TestListByAgent_UnsharedAgentStaysNotFound(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	svc, repo := newSharedListService(t, sharedAgentFixture(), &sharedEmbedShareRepo{})

	_, err := svc.ListByAgent(ctx, 1, "agent-shared")
	require.Error(t, err)
	assert.Equal(t, 0, repo.listCalls, "no listing may run for agents the caller cannot see")
}

func TestListByAgent_NilShareRepoStaysNotFound(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	svc, repo := newSharedListService(t, sharedAgentFixture(), nil)

	_, err := svc.ListByAgent(ctx, 1, "agent-shared")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent not found", "unexpected error: %v", err)
	assert.Equal(t, 0, repo.listCalls)
}
