package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type learningAgentSettings struct {
	interfaces.LearningService
	enabled bool
	calls   int
}

func (s *learningAgentSettings) GetSettings(context.Context) (*types.LearningSettings, error) {
	s.calls++
	return &types.LearningSettings{Enabled: s.enabled}, nil
}

type learningAgentKB struct {
	interfaces.KnowledgeBaseService
	tenant uint64
	wiki   bool
}

func (s learningAgentKB) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{
		ID:               "kb",
		TenantID:         s.tenant,
		IndexingStrategy: types.IndexingStrategy{WikiEnabled: s.wiki},
	}, nil
}

func TestLearningToolsOnlyRegisterForOptedInWebUsersWithWholeOwnedWiki(t *testing.T) {
	for _, test := range []struct {
		name                                                string
		enabled, wiki, shared, narrow, machine, unrequested bool
		tenant                                              uint64
		want                                                bool
	}{
		{name: "allowed", enabled: true, wiki: true, tenant: 7, want: true},
		{name: "no consent", wiki: true, tenant: 7},
		{name: "not wiki", enabled: true, tenant: 7},
		{name: "shared agent", enabled: true, wiki: true, shared: true, tenant: 7},
		{name: "shared KB", enabled: true, wiki: true, tenant: 8},
		{name: "narrow document", enabled: true, wiki: true, narrow: true, tenant: 7},
		{name: "machine", enabled: true, wiki: true, machine: true, tenant: 7},
		{name: "not selected", enabled: true, wiki: true, unrequested: true, tenant: 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(7))
			ctx = types.WithCaller(ctx, types.Caller{TenantID: 7, UserID: "alice"})
			kind := types.PrincipalWebUser
			if test.machine {
				kind = types.PrincipalAPITenant
			}
			ctx = types.WithPrincipal(ctx, types.Principal{Type: kind, ID: "alice"})
			scope := &types.SearchTarget{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb"}
			if test.narrow {
				scope.KnowledgeIDs = []string{"doc"}
			}
			cfg := &types.AgentConfig{
				KnowledgeBases:      []string{"kb"},
				SearchTargets:       types.SearchTargets{scope},
				SharedAgentReadOnly: test.shared,
				AllowedTools: []string{
					tools.ToolGetLearningProfile,
					tools.ToolRecommendLearningTopics,
					tools.ToolPrepareLearningQuiz,
				},
			}
			if test.unrequested {
				cfg.AllowedTools = []string{tools.ToolThinking}
			}
			settings := &learningAgentSettings{enabled: test.enabled}
			svc := &agentService{
				learningService:      settings,
				knowledgeBaseService: learningAgentKB{tenant: test.tenant, wiki: test.wiki},
			}
			registry := tools.NewToolRegistry()
			require.NoError(t, svc.registerTools(ctx, registry, cfg, nil, nil, "session"))
			for _, name := range []string{
				tools.ToolGetLearningProfile, tools.ToolRecommendLearningTopics, tools.ToolPrepareLearningQuiz,
			} {
				require.Equal(t, test.want, hasTool(registry, name))
			}
			if test.unrequested {
				require.Zero(t, settings.calls, "ordinary Wiki agents must not pay a learning settings query")
			}
		})
	}
}
