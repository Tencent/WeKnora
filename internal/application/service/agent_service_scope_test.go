package service

import (
	"testing"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
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

func TestFilterToolsWithoutKnowledgeScopeKeepsNonKnowledgeCapabilities(t *testing.T) {
	filtered := filterToolsWithoutKnowledgeScope(
		[]string{
			agenttools.ToolKnowledgeSearch,
			agenttools.ToolWikiReadPage,
			agenttools.ToolThinking,
			agenttools.ToolReadSkill,
			agenttools.ToolWebSearch,
			agenttools.ToolTodoWrite,
		},
		true,
	)

	assert.Equal(
		t,
		[]string{
			agenttools.ToolThinking,
			agenttools.ToolReadSkill,
			agenttools.ToolWebSearch,
			agenttools.ToolTodoWrite,
		},
		filtered,
	)
}

func TestFilterToolsWithoutKnowledgeScopeDropsTodoWithoutWeb(t *testing.T) {
	filtered := filterToolsWithoutKnowledgeScope(
		[]string{
			agenttools.ToolKnowledgeSearch,
			agenttools.ToolThinking,
			agenttools.ToolWebSearch,
			agenttools.ToolWebFetch,
			agenttools.ToolTodoWrite,
		},
		false,
	)

	assert.Equal(t, []string{agenttools.ToolThinking}, filtered)
}
