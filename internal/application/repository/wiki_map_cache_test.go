package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestWikiMapCacheRepository_UpsertAndGet(t *testing.T) {
	db := setupChunkTestDB(t)
	require.NoError(t, db.AutoMigrate(&types.WikiMapCache{}))
	repo := NewWikiMapCacheRepository(db)
	ctx := context.Background()

	cache := &types.WikiMapCache{
		TenantID:    1,
		CacheKey:    "cache-key",
		ContentHash: "content-hash",
		ModelID:     "chat-a",
		ConfigHash:  "config-hash",
		SchemaVer:   types.WikiMapCacheSchemaV1,
		Payload:     types.JSON(`{"summary_content":"first"}`),
	}
	require.NoError(t, repo.Upsert(ctx, cache))

	got, err := repo.GetByKey(ctx, 1, "cache-key")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.JSONEq(t, `{"summary_content":"first"}`, string(got.Payload))

	cache.Payload = types.JSON(`{"summary_content":"updated"}`)
	require.NoError(t, repo.Upsert(ctx, cache))

	got, err = repo.GetByKey(ctx, 1, "cache-key")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.JSONEq(t, `{"summary_content":"updated"}`, string(got.Payload))

	missing, err := repo.GetByKey(ctx, 2, "cache-key")
	require.NoError(t, err)
	require.Nil(t, missing)
}
