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

func setupEmbeddingCacheTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(embeddingCacheTestDDL).Error)
	return db
}

const embeddingCacheTestDDL = `
CREATE TABLE embedding_cache_entries (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    model_id TEXT NOT NULL,
    dimension INTEGER NOT NULL,
    text_hash TEXT NOT NULL,
    vector TEXT NOT NULL,
    hits INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE(tenant_id, model_id, dimension, text_hash)
);
`

func TestEmbeddingCacheRepository(t *testing.T) {
	db := setupEmbeddingCacheTestDB(t)
	repo := NewEmbeddingCacheRepository(db)
	key := &types.EmbeddingCacheKey{TenantID: 1, ModelID: "m1", Dimension: 8, TextHash: "abc"}

	_, ok, err := repo.Get(context.Background(), key)
	require.NoError(t, err)
	assert.False(t, ok)

	require.NoError(t, repo.Set(context.Background(), key, []float32{0.1, 0.2, 0.3}))
	vector, ok, err := repo.Get(context.Background(), key)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, []float32{0.1, 0.2, 0.3}, vector)

	require.NoError(t, repo.IncrementHit(context.Background(), key))
	require.NoError(t, repo.Set(context.Background(), key, []float32{0.9, 0.8}))
	vector, ok, err = repo.Get(context.Background(), key)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, []float32{0.9, 0.8}, vector)
}

func TestEmbeddingCacheRepositoryDifferentKeyMiss(t *testing.T) {
	db := setupEmbeddingCacheTestDB(t)
	repo := NewEmbeddingCacheRepository(db)
	key := &types.EmbeddingCacheKey{TenantID: 1, ModelID: "m1", Dimension: 8, TextHash: "abc"}
	require.NoError(t, repo.Set(context.Background(), key, []float32{0.1}))

	_, ok, err := repo.Get(context.Background(), &types.EmbeddingCacheKey{TenantID: 2, ModelID: "m1", Dimension: 8, TextHash: "abc"})
	require.NoError(t, err)
	assert.False(t, ok)
}
