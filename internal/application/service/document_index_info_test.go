package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestNewDocumentIndexInfoCarriesAuthoritativeFolderID(t *testing.T) {
	chunk := &types.Chunk{
		ID:              "chunk-1",
		Content:         "content",
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
	}

	info := newDocumentIndexInfo(chunk, "folder-1")

	require.Equal(t, "folder-1", info.FolderID)
	require.Equal(t, chunk.ID, info.ChunkID)
	require.Equal(t, chunk.KnowledgeID, info.KnowledgeID)
	require.True(t, info.IsEnabled)
}
