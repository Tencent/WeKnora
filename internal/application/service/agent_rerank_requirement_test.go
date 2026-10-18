package service

import (
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentRequiresRerankModel(t *testing.T) {
	tests := []struct {
		name  string
		agent *types.CustomAgent
		want  bool
	}{
		{
			name: "knowledge search with all knowledge bases",
			agent: &types.CustomAgent{Config: types.CustomAgentConfig{
				KBSelectionMode: "all",
				AllowedTools:    []string{tools.ToolSearchKnowledge},
			}},
			want: true,
		},
		{
			name: "knowledge search with selected knowledge bases",
			agent: &types.CustomAgent{Config: types.CustomAgentConfig{
				KBSelectionMode: "selected",
				AllowedTools:    []string{tools.ToolSearchKnowledge},
			}},
			want: true,
		},
		{
			name: "knowledge search with knowledge bases disabled",
			agent: &types.CustomAgent{Config: types.CustomAgentConfig{
				KBSelectionMode: "none",
				AllowedTools:    []string{tools.ToolSearchKnowledge},
			}},
			want: false,
		},
		{
			name: "default tools with knowledge bases disabled",
			agent: &types.CustomAgent{Config: types.CustomAgentConfig{
				KBSelectionMode: "none",
			}},
			want: false,
		},
		{
			name: "wiki tools do not use reranker",
			agent: &types.CustomAgent{Config: types.CustomAgentConfig{
				KBSelectionMode: "all",
				AllowedTools:    []string{"wiki_search", "wiki_read_page"},
			}},
			want: false,
		},
		{
			name:  "nil agent",
			agent: nil,
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, agentRequiresRerankModel(tt.agent))
		})
	}
}

// The assistant searches knowledge bases but cannot be given a rerank model,
// so it must take the workspace's instead of failing every run.
func TestWeKnoraAssistantTakesWorkspaceRerankModel(t *testing.T) {
	require.NoError(t, types.LoadBuiltinAgentsConfig(filepath.Join("..", "..", "..", "config")))
	agent := types.GetBuiltinAgent(types.BuiltinWeKnoraAssistantID, 1)
	require.NotNil(t, agent)
	assert.True(t, agentRequiresRerankModel(agent))
	assert.Empty(t, agent.Config.RerankModelID)
	assert.True(t, types.UsesWorkspaceModelFallback(agent))
}

func TestPickWorkspaceRerankModelID(t *testing.T) {
	model := func(id string, typ types.ModelType, status types.ModelStatus, isDefault bool) *types.Model {
		return &types.Model{ID: id, Type: typ, Status: status, IsDefault: isDefault}
	}
	active := types.ModelStatusActive
	tests := []struct {
		name   string
		models []*types.Model
		want   string
	}{
		{name: "no models", want: ""},
		{
			name: "default rerank model wins over an earlier one",
			models: []*types.Model{
				model("first", types.ModelTypeRerank, active, false),
				model("default", types.ModelTypeRerank, active, true),
			},
			want: "default",
		},
		{
			name: "first active rerank model without a default",
			models: []*types.Model{
				nil,
				model("chat", types.ModelTypeKnowledgeQA, active, true),
				model("first", types.ModelTypeRerank, active, false),
				model("second", types.ModelTypeRerank, active, false),
			},
			want: "first",
		},
		{
			name: "inactive rerank models are skipped, even the default",
			models: []*types.Model{
				model("broken-default", types.ModelTypeRerank, types.ModelStatusDownloadFailed, true),
				model("usable", types.ModelTypeRerank, active, false),
			},
			want: "usable",
		},
		{
			name:   "no rerank model",
			models: []*types.Model{model("chat", types.ModelTypeKnowledgeQA, active, true)},
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, pickWorkspaceRerankModelID(tt.models))
		})
	}
}
