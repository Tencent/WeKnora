package service

import (
	"context"
	"strings"
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

func TestFilterSharedAgentWriteToolsCoversAllWikiMutations(t *testing.T) {
	readOnlyWikiTools := map[string]bool{
		tools.ToolWikiReadPage:      true,
		tools.ToolWikiSearch:        true,
		tools.ToolWikiReadSourceDoc: true,
		tools.ToolWikiReadIssue:     true,
	}

	for _, definition := range tools.AvailableToolDefinitions() {
		if !strings.HasPrefix(definition.Name, "wiki_") {
			continue
		}
		filtered := filterSharedAgentWriteTools([]string{definition.Name})
		if readOnlyWikiTools[definition.Name] {
			require.Equal(t, []string{definition.Name}, filtered, "read-only wiki tool %q should remain available", definition.Name)
			continue
		}
		require.Empty(t, filtered, "wiki mutation tool %q must be filtered for shared agents", definition.Name)
	}
}

func TestSharedAgentInfoLocalizesBuiltinName(t *testing.T) {
	restore := types.OverrideBuiltinAgentEntriesForTest(map[string]*types.BuiltinAgentEntry{
		types.BuiltinQuickAnswerID: {
			ID: types.BuiltinQuickAnswerID,
			I18n: map[string]types.BuiltinAgentI18n{
				"default": {Name: "快速问答", Description: "中文 RAG"},
				"en-US":   {Name: "Quick Answer", Description: "English RAG"},
			},
		},
	})
	t.Cleanup(restore)

	agent := &types.CustomAgent{
		ID:          types.BuiltinQuickAnswerID,
		Name:        "frozen-zh",
		Description: "frozen-zh-desc",
		IsBuiltin:   true,
		TenantID:    1,
	}
	svc := &agentShareService{}
	ctx := context.WithValue(context.Background(), types.LanguageContextKey, "en-US")
	info := svc.sharedAgentInfo(ctx, &types.AgentShare{
		Agent:          agent,
		SourceTenantID: 1,
	}, types.OrgRoleViewer, map[string]bool{})

	require.Equal(t, "Quick Answer", info.Agent.Name)
	require.Equal(t, "English RAG", info.Agent.Description)
}

func TestSharedAgentInfoLeavesCustomAgentName(t *testing.T) {
	agent := &types.CustomAgent{ID: "custom-1", Name: "Mine", Description: "keep me"}
	svc := &agentShareService{}
	info := svc.sharedAgentInfo(context.Background(), &types.AgentShare{
		Agent:          agent,
		SourceTenantID: 1,
	}, types.OrgRoleViewer, map[string]bool{})
	require.Equal(t, "Mine", info.Agent.Name)
	require.Equal(t, "keep me", info.Agent.Description)
}
