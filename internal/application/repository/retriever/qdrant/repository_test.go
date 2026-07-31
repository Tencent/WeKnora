package qdrant

import (
	"testing"

	qdrantclient "github.com/qdrant/go-client/qdrant"

	"github.com/stretchr/testify/require"
)

func TestNewCollectionKeywordPayloadIndexesPreserveMainAndAddFolder(t *testing.T) {
	require.ElementsMatch(t, []string{
		fieldChunkID,
		fieldKnowledgeID,
		fieldKnowledgeBaseID,
		fieldSourceID,
		fieldFolderID,
	}, newCollectionKeywordPayloadIndexFields())
}

func TestEmbeddingFromPayloadIncludesFolderID(t *testing.T) {
	payload := qdrantclient.NewValueMap(map[string]any{
		fieldContent:         "content",
		fieldSourceID:        "source",
		fieldSourceType:      int64(2),
		fieldChunkID:         "chunk",
		fieldKnowledgeID:     "knowledge",
		fieldKnowledgeBaseID: "knowledge-base",
		fieldTagID:           "tag",
		fieldFolderID:        "folder",
	})

	embedding := embeddingFromPayload(payload)

	require.Equal(t, "folder", embedding.FolderID)
	require.Equal(t, "knowledge", embedding.KnowledgeID)
	require.Equal(t, "chunk", embedding.ChunkID)
}
