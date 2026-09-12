package milvus

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestBaseFilterIncludesDisabledOnlyWhenRequested(t *testing.T) {
	repo := &milvusRepository{}

	defaultFilter, _, err := repo.getBaseFilterForQuery(types.RetrieveParams{})
	require.NoError(t, err)
	require.Contains(t, defaultFilter, fieldIsEnabled)

	adminFilter, _, err := repo.getBaseFilterForQuery(types.RetrieveParams{IncludeDisabled: true})
	require.NoError(t, err)
	require.False(t, strings.Contains(adminFilter, fieldIsEnabled))
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
