package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/browserskill"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type browserSourceKBService struct{ fakeAgentKnowledgeBaseService }

func (s *browserSourceKBService) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func TestBrowserSourceKeepsOtherConfiguredTools(t *testing.T) {
	t.Setenv("BROWSERSKILL_BINARY", "/unused/browser-skill")
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "alice")
	kb := &types.KnowledgeBase{ID: "kb"}
	kb.IndexingStrategy.VectorEnabled = true
	svc := &agentService{
		browserSkill:         browserskill.NewManager(),
		knowledgeBaseService: &browserSourceKBService{fakeAgentKnowledgeBaseService{kb: kb}},
		knowledgeService:     &fakeAgentKnowledgeService{},
		sandboxResolver: stubSandboxResolver{mgr: &capableManager{
			typ: sandbox.SandboxTypeCube, shell: &stubShellExecutor{}, files: stubSessionFileStore{},
		}},
	}
	var offered [][]string
	for _, selected := range []bool{false, true} {
		model := &fakeAgentChatModel{}
		cfg := &types.AgentConfig{
			LocalBrowserEnabled: selected, WebSearchEnabled: true, SkillsEnabled: true,
			SandboxConfigID: "sandbox", MCPSelectionMode: "all", KnowledgeBases: []string{"kb"},
			SearchTargets: types.SearchTargets{&types.SearchTarget{KnowledgeBaseID: "kb", TenantID: 7}},
			SkillDirs:     []string{t.TempDir()},
			AllowedTools:  []string{tools.ToolKnowledgeSearch, tools.ToolShellExec, tools.ToolThinking},
		}
		engine, err := svc.CreateAgentEngine(ctx, cfg, model, nil, nil, "session", "message")
		require.NoError(t, err)
		require.NotNil(t, engine.(*agent.AgentEngine).GetSkillsManager())
		_, err = engine.Execute(ctx, "session", "message", "use my browser and other sources", nil)
		require.NoError(t, err)
		for _, name := range []string{
			"local_browser", tools.ToolKnowledgeSearch, tools.ToolWebSearch, tools.ToolWebFetch, tools.ToolShellExec,
		} {
			require.Contains(t, model.lastToolNames, name)
		}
		offered = append(offered, model.lastToolNames)
	}
	require.ElementsMatch(t, offered[0], offered[1], "selecting the browser must not shrink other tool scopes")
}

func TestBrowserSourceUnavailableDoesNotCreateFallbackEngine(t *testing.T) {
	svc := &agentService{}
	engine, err := svc.CreateAgentEngine(
		t.Context(), &types.AgentConfig{LocalBrowserEnabled: true},
		&fakeAgentChatModel{}, nil, nil, "session", "message",
	)
	require.ErrorContains(t, err, "local browser is unavailable")
	require.Nil(t, engine)
}

func TestBrowserSourceConfigPreservesOtherSelectionsAndResetsNextTurn(t *testing.T) {
	svc := &sessionService{cfg: &config.Config{}, webSearchProviderRepo: &sharedAgentWebSearchRepo{}}
	req := &types.QARequest{
		LocalBrowserEnabled: true, WebSearchEnabled: true,
		Session: &types.Session{ID: "session", TenantID: 1},
		CustomAgent: &types.CustomAgent{TenantID: 1, Config: types.CustomAgentConfig{
			WebSearchEnabled: true, SkillsSelectionMode: "all", MCPSelectionMode: "all",
			SandboxConfigID: "sandbox", SystemPrompt: "custom instructions",
		}},
	}
	cfg, err := svc.buildAgentConfig(t.Context(), req, &types.Tenant{ID: 1}, 1)
	require.NoError(t, err)
	require.True(t, cfg.LocalBrowserEnabled)
	require.True(t, cfg.WebSearchEnabled)
	require.True(t, cfg.SkillsEnabled)
	require.Equal(t, "all", cfg.MCPSelectionMode)
	require.Empty(t, cfg.KnowledgeBases)
	require.Equal(t, "sandbox", cfg.SandboxConfigID)
	require.Equal(t, "custom instructions", cfg.SystemPrompt)
	require.True(t, req.CustomAgent.Config.WebSearchEnabled)
	req.LocalBrowserEnabled = false
	cfg, err = svc.buildAgentConfig(t.Context(), req, &types.Tenant{ID: 1}, 1)
	require.NoError(t, err)
	require.False(t, cfg.LocalBrowserEnabled)
	require.True(t, cfg.WebSearchEnabled)
	require.True(t, cfg.SkillsEnabled)
	require.Equal(t, "all", cfg.MCPSelectionMode)
}
