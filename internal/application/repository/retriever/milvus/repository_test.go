package milvus

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestBuildMilvusIndexResultsPreservesSearchScores(t *testing.T) {
	documents := []*MilvusVectorEmbeddingWithScore{
		{MilvusVectorEmbedding: MilvusVectorEmbedding{ID: "id-1", ChunkID: "chunk-1"}},
		{MilvusVectorEmbedding: MilvusVectorEmbedding{ID: "id-2", ChunkID: "chunk-2"}},
	}

	results, err := buildMilvusIndexResults(
		documents,
		[]float64{3.25, 0.75},
		types.MatchTypeKeywords,
	)

	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, types.MatchTypeKeywords, results[0].MatchType)
	require.Equal(t, 3.25, results[0].Score)
	require.Equal(t, 0.75, results[1].Score)
}

func TestBuildMilvusIndexResultsRejectsScoreCountMismatch(t *testing.T) {
	_, err := buildMilvusIndexResults(
		[]*MilvusVectorEmbeddingWithScore{
			{MilvusVectorEmbedding: MilvusVectorEmbedding{ID: "id-1"}},
		},
		nil,
		types.MatchTypeKeywords,
	)

	require.Error(t, err)
}

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

func TestUpdateChunkEnabledStatusInCollectionsPropagatesFailure(t *testing.T) {
	wantErr := errors.New("upsert failed")
	err := updateChunkEnabledStatusInCollections(
		context.Background(),
		[]string{"other_collection", "weknora_embeddings_1024"},
		"weknora_embeddings",
		nil,
		[]string{"chunk-1"},
		func(_ context.Context, collection string, _ []string, enabled bool) error {
			if collection == "weknora_embeddings_1024" && !enabled {
				return wantErr
			}
			return nil
		},
	)
	require.ErrorIs(t, err, wantErr)
}

func TestUpdateChunkEnabledStatusInCollectionsIgnoresExtendedPrefix(t *testing.T) {
	var seen []string
	seenSet := map[string]bool{}
	err := updateChunkEnabledStatusInCollections(
		context.Background(),
		[]string{
			"other_collection",
			"weknora_embeddings_1024",
			"weknora_embeddings_multilingual_1024",
			"weknora_embeddings_1024_backup",
		},
		"weknora_embeddings",
		[]string{"chunk-1"},
		nil,
		func(_ context.Context, collection string, _ []string, _ bool) error {
			if !seenSet[collection] {
				seenSet[collection] = true
				seen = append(seen, collection)
			}
			return nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, []string{"weknora_embeddings_1024"}, seen)
}
