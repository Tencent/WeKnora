package agent

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectKnowledgeRefsFromStep(t *testing.T) {
	step := types.AgentStep{
		ToolCalls: []types.ToolCall{{
			Result: &types.ToolResult{
				Success: true,
				Data: map[string]interface{}{
					"results": []map[string]interface{}{{
						"chunk_id":          "chunk-1",
						"content":           "content",
						"knowledge_id":      "knowledge-1",
						"knowledge_base_id": "kb-1",
						"knowledge_title":   "doc.pdf",
						"chunk_index":       2,
						"chunk_type":        "text",
						"score":             0.9,
					}},
				},
			},
		}},
	}

	refs := collectKnowledgeRefsFromStep(step)
	require.Len(t, refs, 1)
	assert.Equal(t, "chunk-1", refs[0].ID)
	assert.Equal(t, "knowledge-1", refs[0].KnowledgeID)
	assert.Equal(t, "kb-1", refs[0].KnowledgeBaseID)
	assert.Equal(t, "doc.pdf", refs[0].KnowledgeTitle)
	assert.Equal(t, 2, refs[0].ChunkIndex)
	assert.Equal(t, "text", refs[0].ChunkType)
}

func TestMergeKnowledgeRefsDeduplicates(t *testing.T) {
	merged := mergeKnowledgeRefs(
		[]*types.SearchResult{{ID: "chunk-1"}, {ID: "chunk-2"}},
		[]*types.SearchResult{{ID: "chunk-2"}, {ID: "chunk-3"}},
	)

	require.Len(t, merged, 3)
	assert.Equal(t, "chunk-1", merged[0].ID)
	assert.Equal(t, "chunk-2", merged[1].ID)
	assert.Equal(t, "chunk-3", merged[2].ID)
}
