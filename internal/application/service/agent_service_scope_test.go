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

func TestAgentHasKnowledgeScope_AuthoritativeEmptyFolderScope(t *testing.T) {
	cfg := &types.AgentConfig{
		KnowledgeBases:          []string{"default-kb"},
		KnowledgeScopeSpecified: true,
	}
	assert.False(t, agentHasKnowledgeScope(cfg))
	assert.Empty(t, knowledgeBaseIDsForPrompt(cfg))
}

func TestAgentHasKnowledgeScope_AuthoritativeNonEmptyFolderScope(t *testing.T) {
	cfg := &types.AgentConfig{
		KnowledgeBases:          []string{"default-kb"},
		KnowledgeScopeSpecified: true,
		SearchTargets: types.SearchTargets{
			{KnowledgeBaseID: "folder-kb", Type: types.SearchTargetTypeKnowledge,
				KnowledgeIDs: []string{"doc-1"}},
		},
	}
	assert.True(t, agentHasKnowledgeScope(cfg))
	assert.Equal(t, []string{"folder-kb"}, knowledgeBaseIDsForPrompt(cfg))
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
