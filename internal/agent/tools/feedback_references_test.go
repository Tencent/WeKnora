package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestExtractFeedbackReferencesUsesOnlyFinalDisplayedKBCandidates(t *testing.T) {
	raw := []types.AgentFeedbackReference{
		{
			TenantID: 11,
			Result: &types.SearchResult{
				ID: "chunk-a", KnowledgeBaseID: "kb-a", KnowledgeID: "doc-a",
				ChunkType: string(types.ChunkTypeText),
			},
		},
		{
			TenantID: 11,
			Result: &types.SearchResult{
				ID: "chunk-a", KnowledgeBaseID: "kb-a", KnowledgeID: "doc-a",
				ChunkType: string(types.ChunkTypeText),
			},
		},
		{
			TenantID: 11,
			Result: &types.SearchResult{
				ID: "web-a", KnowledgeBaseID: "kb-a",
				ChunkType: string(types.ChunkTypeWebSearch),
			},
		},
		{
			TenantID: 12,
			Result: &types.SearchResult{
				ID: "history-a", KnowledgeBaseID: "kb-b", MatchType: types.MatchTypeHistory,
			},
		},
	}

	references, scopes := ExtractFeedbackReferences(ToolKnowledgeSearch, raw)
	require.Len(t, references, 1)
	require.Len(t, scopes, 1)
	assert.Equal(t, "chunk-a", references[0].ID)
	assert.Equal(t, types.ChunkFeedbackScope{
		TenantID: 11, KnowledgeBaseID: "kb-a", ChunkID: "chunk-a",
	}, scopes[0])

	webReferences, webScopes := ExtractFeedbackReferences(ToolWebSearch, raw)
	assert.Empty(t, webReferences)
	assert.Empty(t, webScopes)
}
