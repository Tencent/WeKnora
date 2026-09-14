package milvus

import (
	"context"
	"errors"
	"testing"

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
