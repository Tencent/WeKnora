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

func TestKnowledgeBaseScopesForPrompt_FromSearchTargets(t *testing.T) {
	cfg := &types.AgentConfig{
		SearchTargets: types.SearchTargets{
			{KnowledgeBaseID: "kb-1", TagIDs: []string{"tag-a"}},
			{KnowledgeBaseID: "kb-1", TagIDs: []string{"tag-b"}},
			{KnowledgeBaseID: "kb-2", TagIDs: []string{"tag-c"}},
		},
	}
	ids, _ := knowledgeBaseScopesForPrompt(cfg)
	assert.Equal(t, []string{"kb-1", "kb-2"}, ids)
}

func TestKnowledgeBaseScopesForPrompt_CarriesSharedKBSourceTenant(t *testing.T) {
	cfg := &types.AgentConfig{
		SearchTargets: types.SearchTargets{
			{KnowledgeBaseID: "shared-kb", TenantID: 42},
			{KnowledgeBaseID: "own-kb", TenantID: 0},
		},
	}
	ids, tenantMap := knowledgeBaseScopesForPrompt(cfg)
	assert.Equal(t, []string{"shared-kb", "own-kb"}, ids)
	assert.Equal(t, uint64(42), tenantMap["shared-kb"])
	assert.Zero(t, tenantMap["own-kb"])
}

// KnowledgeBases carries shared KB IDs too (KBSelectionMode="all" and @mentions
// both write into it), so the source tenant must still come from SearchTargets.
func TestKnowledgeBaseScopesForPrompt_ExplicitKnowledgeBasesKeepSourceTenant(t *testing.T) {
	cfg := &types.AgentConfig{
		KnowledgeBases: []string{"own-kb", "shared-kb"},
		SearchTargets: types.SearchTargets{
			{KnowledgeBaseID: "own-kb", TenantID: 7},
			{KnowledgeBaseID: "shared-kb", TenantID: 42},
		},
	}
	ids, tenantMap := knowledgeBaseScopesForPrompt(cfg)
	assert.Equal(t, []string{"own-kb", "shared-kb"}, ids)
	assert.Equal(t, uint64(42), tenantMap["shared-kb"])
}

func TestKnowledgeBaseScopesForPrompt_NilConfig(t *testing.T) {
	ids, tenantMap := knowledgeBaseScopesForPrompt(nil)
	assert.Nil(t, ids)
	assert.Empty(t, tenantMap)
}
