package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestAgentHasKnowledgeScope_TagOnlySearchTargets(t *testing.T) {
	cfg := &types.AgentConfig{
		SearchTargets: types.SearchTargets{
			{
				Type:            types.SearchTargetTypeKnowledgeBase,
				KnowledgeBaseID: "kb-1",
				TagIDs:          []string{"tag-a"},
			},
		},
	}
	assert.True(t, agentHasKnowledgeScope(cfg))
}

func TestAgentHasKnowledgeScope_Empty(t *testing.T) {
	assert.False(t, agentHasKnowledgeScope(&types.AgentConfig{}))
	assert.False(t, agentHasKnowledgeScope(nil))
}

func TestKnowledgeBaseIDsForPrompt_FromSearchTargets(t *testing.T) {
	cfg := &types.AgentConfig{
		SearchTargets: types.SearchTargets{
			{KnowledgeBaseID: "kb-1", TagIDs: []string{"tag-a"}},
			{KnowledgeBaseID: "kb-1", TagIDs: []string{"tag-b"}},
			{KnowledgeBaseID: "kb-2", TagIDs: []string{"tag-c"}},
		},
	}
	assert.Equal(t, []string{"kb-1", "kb-2"}, knowledgeBaseIDsForPrompt(cfg))
}
