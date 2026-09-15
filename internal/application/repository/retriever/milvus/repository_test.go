package milvus

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
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

func TestScoredHitsKeepMilvusScores(t *testing.T) {
	sets := []*MilvusVectorEmbeddingWithScore{
		{MilvusVectorEmbedding: MilvusVectorEmbedding{ChunkID: "c1"}},
		{MilvusVectorEmbedding: MilvusVectorEmbedding{ChunkID: "c2"}},
	}

	hits := scoredHits(sets, []float64{12.5, 3.25}, types.MatchTypeKeywords)

	require.Len(t, hits, 2)
	require.Equal(t, "c1", hits[0].ChunkID)
	require.Equal(t, 12.5, hits[0].Score)
	require.Equal(t, 3.25, hits[1].Score)
	require.Equal(t, types.MatchTypeKeywords, hits[1].MatchType)
}

func TestTopKByScoreRanksAcrossCollectionsBeforeTruncating(t *testing.T) {
	// Two collections searched in turn: the best hit sits in the second, so
	// truncating the concatenation head-first would drop it.
	hits := []*types.IndexWithScore{
		{ChunkID: "a", Score: 2},
		{ChunkID: "b", Score: 1},
		{ChunkID: "c", Score: 5},
		{ChunkID: "d", Score: 2},
	}

	got := topKByScore(hits, 3)

	require.Equal(t, []string{"c", "a", "d"}, []string{got[0].ChunkID, got[1].ChunkID, got[2].ChunkID})
	require.Len(t, topKByScore(hits, 10), 4)
	require.Empty(t, topKByScore(hits, 0))
}
