package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupContentCacheTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.ContentCacheEntry{}))
	return db
}

func TestContentCacheRepository_SQLite_Miss(t *testing.T) {
	repo := NewContentCacheRepository(setupContentCacheTestDB(t))

	got, err := repo.GetByKey(context.Background(), 1, types.ContentCacheKindEmbedding, "missing")

	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestContentCacheRepository_SQLite_UpsertGetAndOverwrite(t *testing.T) {
	repo := NewContentCacheRepository(setupContentCacheTestDB(t))
	ctx := context.Background()

	entry := &types.ContentCacheEntry{
		TenantID:  1,
		CacheKind: types.ContentCacheKindEmbedding,
		CacheKey:  "embedding:key",
		Payload:   types.JSON(`{"value":1}`),
	}
	require.NoError(t, repo.Upsert(ctx, entry))

	got, err := repo.GetByKey(ctx, 1, types.ContentCacheKindEmbedding, "embedding:key")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, `{"value":1}`, got.Payload.ToString())

	require.NoError(t, repo.Upsert(ctx, &types.ContentCacheEntry{
		TenantID:  1,
		CacheKind: types.ContentCacheKindEmbedding,
		CacheKey:  "embedding:key",
		Payload:   types.JSON(`{"value":2}`),
	}))

	got, err = repo.GetByKey(ctx, 1, types.ContentCacheKindEmbedding, "embedding:key")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, `{"value":2}`, got.Payload.ToString())
}

func TestContentCacheRepository_SQLite_TenantIsolation(t *testing.T) {
	repo := NewContentCacheRepository(setupContentCacheTestDB(t))
	ctx := context.Background()

	require.NoError(t, repo.Upsert(ctx, &types.ContentCacheEntry{
		TenantID:  1,
		CacheKind: types.ContentCacheKindEmbedding,
		CacheKey:  "same",
		Payload:   types.JSON(`[1]`),
	}))
	require.NoError(t, repo.Upsert(ctx, &types.ContentCacheEntry{
		TenantID:  2,
		CacheKind: types.ContentCacheKindEmbedding,
		CacheKey:  "same",
		Payload:   types.JSON(`[2]`),
	}))

	got1, err := repo.GetByKey(ctx, 1, types.ContentCacheKindEmbedding, "same")
	require.NoError(t, err)
	require.NotNil(t, got1)
	assert.Equal(t, `[1]`, got1.Payload.ToString())

	got2, err := repo.GetByKey(ctx, 2, types.ContentCacheKindEmbedding, "same")
	require.NoError(t, err)
	require.NotNil(t, got2)
	assert.Equal(t, `[2]`, got2.Payload.ToString())
}
