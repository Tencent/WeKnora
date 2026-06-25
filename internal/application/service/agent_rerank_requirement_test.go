package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
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
				AllowedTools:    []string{tools.ToolKnowledgeSearch},
			}},
			want: true,
		},
		{
			name: "knowledge search with selected knowledge bases",
			agent: &types.CustomAgent{Config: types.CustomAgentConfig{
				KBSelectionMode: "selected",
				AllowedTools:    []string{tools.ToolKnowledgeSearch},
			}},
			want: true,
		},
		{
			name: "knowledge search with knowledge bases disabled",
			agent: &types.CustomAgent{Config: types.CustomAgentConfig{
				KBSelectionMode: "none",
				AllowedTools:    []string{tools.ToolKnowledgeSearch},
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
