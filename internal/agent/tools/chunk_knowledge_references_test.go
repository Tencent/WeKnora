package tools

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestChunkKnowledgeReferencePreservesFeedbackIdentityAndEnrichedContent(t *testing.T) {
	chunk := &types.Chunk{
		ID:              "chunk-source-1",
		KnowledgeID:     "document-1",
		KnowledgeBaseID: "kb-1",
		Content:         "plain content",
		ChunkIndex:      4,
		StartAt:         20,
		EndAt:           40,
		ChunkType:       types.ChunkTypeText,
		ParentChunkID:   "parent-1",
		RecallWeight:    0.5,
		Metadata:        types.JSON(`{"section":"intro"}`),
	}

	reference := chunkKnowledgeReference(chunk, "Document", "plain content\nOCR context")

	require.NotNil(t, reference)
	require.Equal(t, "chunk-source-1", reference.ID)
	require.Equal(t, "document-1", reference.KnowledgeID)
	require.Equal(t, "kb-1", reference.KnowledgeBaseID)
	require.Equal(t, "Document", reference.KnowledgeTitle)
	require.Equal(t, "plain content\nOCR context", reference.Content)
	require.Equal(t, reference.Content, reference.MatchedContent)
	require.Equal(t, types.MatchTypeKeywords, reference.MatchType)
	require.Equal(t, 0.5, reference.RecallWeight)
	require.Equal(t, "parent-1", reference.ParentChunkID)
	require.Equal(t, types.JSON(`{"section":"intro"}`), reference.ChunkMetadata)
	require.Nil(t, chunkKnowledgeReference(nil, "", ""))
}
