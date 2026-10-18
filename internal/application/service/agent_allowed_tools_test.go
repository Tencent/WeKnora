package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// An agent's allowed_tools is read in three places: the session config, tool
// registration and the rerank requirement. These tests pin all three to the
// same meaning: an unset list selects the default tools and an empty list
// means no built-in tools.

func TestBuildAgentConfigSeparatesUnsetFromEmptyToolList(t *testing.T) {
	svc := &sessionService{cfg: &config.Config{}, webSearchProviderRepo: &sharedAgentWebSearchRepo{}}
	build := func(allowed []string) []string {
		req := &types.QARequest{
			Session: &types.Session{ID: "session", TenantID: 1},
			CustomAgent: &types.CustomAgent{TenantID: 1, Config: types.CustomAgentConfig{
				AgentMode: types.AgentModeSmartReasoning, AllowedTools: allowed,
			}},
		}
		cfg, err := svc.buildAgentConfig(t.Context(), req, &types.Tenant{ID: 1}, 1)
		require.NoError(t, err)
		return cfg.AllowedTools
	}

	require.Equal(t, tools.DefaultAllowedTools(), build(nil))
	empty := build([]string{})
	require.NotNil(t, empty, "the empty list must reach tool registration as empty, not unset")
	require.Empty(t, empty)
	require.Equal(t, []string{tools.ToolSearchKnowledge}, build([]string{tools.LegacyToolKnowledgeSearch}))
}

func TestRegisterToolsSeparatesUnsetFromEmptyToolList(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	offered := func(allowed []string) []string {
		chatModel := &fakeAgentChatModel{}
		engine, err := (&agentService{}).CreateAgentEngine(
			ctx, &types.AgentConfig{AllowedTools: allowed}, chatModel, nil, nil, "sess-1", "msg-1")
		require.NoError(t, err)
		_, err = engine.Execute(ctx, "sess-1", "msg-1", "hello", nil)
		require.NoError(t, err)
		return chatModel.lastToolNames
	}

	// Without a knowledge base the retrieval defaults are dropped, which
	// leaves search_conversations as the visible part of the defaults.
	require.Contains(t, offered(nil), tools.ToolSearchConversations)
	require.NotContains(t, offered([]string{}), tools.ToolSearchConversations)
	require.Empty(t, offered([]string{}), "an empty list registers no built-in tool")
}

func TestRerankRequirementSeparatesUnsetFromEmptyToolList(t *testing.T) {
	agent := func(allowed []string) *types.CustomAgent {
		return &types.CustomAgent{Config: types.CustomAgentConfig{KBSelectionMode: "all", AllowedTools: allowed}}
	}
	require.True(t, agentRequiresRerankModel(agent(nil)), "the default tools include search_knowledge")
	require.False(t, agentRequiresRerankModel(agent([]string{})), "no tool calls the reranker")
}
