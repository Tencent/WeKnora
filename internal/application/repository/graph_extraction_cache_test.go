package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestGraphExtractionCacheRepository_UpsertAndGet(t *testing.T) {
	db := setupChunkTestDB(t)
	require.NoError(t, db.AutoMigrate(&types.GraphExtractionCache{}))
	repo := NewGraphExtractionCacheRepository(db)
	ctx := context.Background()

	cache := &types.GraphExtractionCache{
		TenantID:    1,
		CacheKey:    "cache-key",
		ContentHash: "content-hash",
		ModelID:     "chat-a",
		ConfigHash:  "config-hash",
		SchemaVer:   types.GraphExtractionCacheSchemaV1,
		Graph:       types.JSON(`{"node":[{"name":"A"}]}`),
	}
	require.NoError(t, repo.Upsert(ctx, cache))

	got, err := repo.GetByKey(ctx, 1, "cache-key")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.JSONEq(t, `{"node":[{"name":"A"}]}`, string(got.Graph))

	cache.Graph = types.JSON(`{"node":[{"name":"B"}]}`)
	require.NoError(t, repo.Upsert(ctx, cache))

	got, err = repo.GetByKey(ctx, 1, "cache-key")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.JSONEq(t, `{"node":[{"name":"B"}]}`, string(got.Graph))

	missing, err := repo.GetByKey(ctx, 2, "cache-key")
	require.NoError(t, err)
	require.Nil(t, missing)
}
