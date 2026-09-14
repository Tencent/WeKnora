package handler

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAddKnowledgeBaseAgentSourcePreservesDirectShareSemantics(t *testing.T) {
	item := &types.OrganizationSharedKnowledgeBaseItem{
		SharedKnowledgeBaseInfo: types.SharedKnowledgeBaseInfo{ShareID: "share-1"},
	}
	source := types.SourceFromAgentInfo{AgentID: "agent-1", AgentName: "Research"}

	addKnowledgeBaseAgentSource(item, source)
	addKnowledgeBaseAgentSource(item, source)

	require.Nil(t, item.SourceFromAgent)
	require.Equal(t, []types.SourceFromAgentInfo{source}, item.SourceFromAgents)
}

func TestAddKnowledgeBaseAgentSourceMarksAgentOnlyRow(t *testing.T) {
	item := &types.OrganizationSharedKnowledgeBaseItem{}
	first := types.SourceFromAgentInfo{AgentID: "agent-1", AgentName: "Research"}
	second := types.SourceFromAgentInfo{AgentID: "agent-2", AgentName: "Support"}

	addKnowledgeBaseAgentSource(item, first)
	addKnowledgeBaseAgentSource(item, second)

	require.Equal(t, &first, item.SourceFromAgent)
	require.Equal(t, []types.SourceFromAgentInfo{first, second}, item.SourceFromAgents)
}
