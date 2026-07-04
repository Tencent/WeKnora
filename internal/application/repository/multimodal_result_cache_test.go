package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestMultimodalResultCacheRepository_UpsertAndGet(t *testing.T) {
	db := setupChunkTestDB(t)
	require.NoError(t, db.AutoMigrate(&types.MultimodalResultCache{}))
	repo := NewMultimodalResultCacheRepository(db)
	ctx := context.Background()

	cache := &types.MultimodalResultCache{
		TenantID:   1,
		CacheKey:   "cache-key",
		ImageHash:  "image-hash",
		ModelID:    "vlm-a",
		PromptHash: "prompt-hash",
		OutputType: types.MultimodalOutputOCR,
		SchemaVer:  types.MultimodalResultCacheSchemaV1,
		Content:    "first OCR",
	}
	require.NoError(t, repo.Upsert(ctx, cache))

	got, err := repo.GetByKey(ctx, 1, "cache-key")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "first OCR", got.Content)

	cache.Content = "updated OCR"
	require.NoError(t, repo.Upsert(ctx, cache))

	got, err = repo.GetByKey(ctx, 1, "cache-key")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "updated OCR", got.Content)

	missing, err := repo.GetByKey(ctx, 2, "cache-key")
	require.NoError(t, err)
	require.Nil(t, missing)
}
