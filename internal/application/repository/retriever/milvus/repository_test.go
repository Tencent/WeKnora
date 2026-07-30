package milvus

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/vectorstoreid"
	"github.com/stretchr/testify/require"
)

func TestUpdateChunkEnabledStatusInCollectionSkipsEmptyChunkIDs(t *testing.T) {
	repo := &milvusRepository{}

	require.NoError(t, repo.updateChunkEnabledStatusInCollection(
		context.Background(),
		"weknora_embeddings_1024",
		nil,
		false,
	))
	require.NoError(t, repo.updateChunkEnabledStatusInCollection(
		context.Background(),
		"weknora_embeddings_1024",
		[]string{},
		true,
	))
}

func TestStablePointIDAndLegacyCleanupExpression(t *testing.T) {
	t.Parallel()

	embedding := &MilvusVectorEmbedding{
		ID:         vectorstoreid.StablePointID("source-1"),
		SourceID:   "source-1",
		SourceType: int(types.ChunkSourceType),
	}
	require.Equal(t,
		`source_id == "source-1" and id != "`+embedding.ID+`"`,
		milvusLegacyPointExpr(embedding),
	)
}
