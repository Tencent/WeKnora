package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type sharedAgentWebSearchRepo struct {
	byIDTenant      uint64
	byID            string
	defaultTenant   uint64
	explicit        *types.WebSearchProviderEntity
	defaultProvider *types.WebSearchProviderEntity
}

func (r *sharedAgentWebSearchRepo) Create(context.Context, *types.WebSearchProviderEntity) error {
	return nil
}

func (r *sharedAgentWebSearchRepo) GetByID(_ context.Context, tenantID uint64, id string) (*types.WebSearchProviderEntity, error) {
	r.byIDTenant = tenantID
	r.byID = id
	return r.explicit, nil
}

func (r *sharedAgentWebSearchRepo) GetDefault(_ context.Context, tenantID uint64) (*types.WebSearchProviderEntity, error) {
	r.defaultTenant = tenantID
	return r.defaultProvider, nil
}

func (r *sharedAgentWebSearchRepo) List(context.Context, uint64) ([]*types.WebSearchProviderEntity, error) {
	return nil, nil
}

func (r *sharedAgentWebSearchRepo) Update(context.Context, *types.WebSearchProviderEntity) error {
	return nil
}

func (r *sharedAgentWebSearchRepo) Delete(context.Context, uint64, string) error { return nil }

func (r *sharedAgentWebSearchRepo) ClearDefault(context.Context, uint64, string) error { return nil }

func TestSharedAgentWebSearchReadyUsesSourceWorkspace(t *testing.T) {
	repo := &sharedAgentWebSearchRepo{
		explicit: &types.WebSearchProviderEntity{ID: "source-provider", TenantID: 42},
	}
	svc := &agentShareService{webSearchProviderRepo: repo}
	agent := &types.CustomAgent{Config: types.CustomAgentConfig{
		WebSearchEnabled:    true,
		WebSearchProviderID: "source-provider",
	}}

	require.True(t, svc.isAgentWebSearchReady(context.Background(), agent, 42))
	require.Equal(t, uint64(42), repo.byIDTenant)
	require.Equal(t, "source-provider", repo.byID)
}

func TestSharedAgentWebSearchReadyUsesSourceDefault(t *testing.T) {
	repo := &sharedAgentWebSearchRepo{
		defaultProvider: &types.WebSearchProviderEntity{ID: "source-default", TenantID: 42, IsDefault: true},
	}
	svc := &agentShareService{webSearchProviderRepo: repo}
	agent := &types.CustomAgent{Config: types.CustomAgentConfig{WebSearchEnabled: true}}

	require.True(t, svc.isAgentWebSearchReady(context.Background(), agent, 42))
	require.Equal(t, uint64(42), repo.defaultTenant)
}

func TestFilterSharedAgentWriteTools(t *testing.T) {
	got := filterSharedAgentWriteTools([]string{
		tools.ToolWikiReadPage,
		tools.ToolWikiFlagIssue,
		tools.ToolWikiWritePage,
		tools.ToolWikiReplaceText,
		tools.ToolWikiRenamePage,
		tools.ToolWikiDeletePage,
		tools.ToolWikiReadIssue,
		tools.ToolWikiUpdateIssue,
		tools.ToolWebSearch,
	})

	require.Equal(t, []string{
		tools.ToolWikiReadPage,
		tools.ToolWikiReadIssue,
		tools.ToolWebSearch,
	}, got)
}
