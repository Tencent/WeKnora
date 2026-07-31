package repository

import (
	"context"
	"strings"
	"testing"
	"time"

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
	require.NoError(t, db.AutoMigrate(&types.ContentCache{}))
	return db
}

func TestContentCache_SetGetAndUpsert(t *testing.T) {
	db := setupContentCacheTestDB(t)
	repo := NewContentCacheRepository(db)
	ctx := context.Background()

	// Missing key -> found=false, no error.
	_, found, err := repo.Get(ctx, "missing")
	require.NoError(t, err)
	assert.False(t, found)

	require.NoError(t, repo.Set(ctx, "k1", types.ContentCacheKindEmbedding, []byte(`[1,2,3]`)))

	payload, found, err := repo.Get(ctx, "k1")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, `[1,2,3]`, string(payload))

	// Upsert replaces the payload in place.
	require.NoError(t, repo.Set(ctx, "k1", types.ContentCacheKindEmbedding, []byte(`[4,5]`)))
	payload, found, _ = repo.Get(ctx, "k1")
	require.True(t, found)
	assert.Equal(t, `[4,5]`, string(payload))

	// Kind is persisted (used for observability / future sweep tooling).
	var row types.ContentCache
	require.NoError(t, db.Where("cache_key = ?", "k1").First(&row).Error)
	assert.Equal(t, types.ContentCacheKindEmbedding, row.Kind)
}

func TestContentCache_SetSkipsOversizedPayload(t *testing.T) {
	db := setupContentCacheTestDB(t)
	repo := NewContentCacheRepository(db)
	ctx := context.Background()

	big := strings.Repeat("x", types.ContentCachePayloadMaxBytes+1)
	require.NoError(t, repo.Set(ctx, "big", types.ContentCacheKindVLM, []byte(big)))
	_, found, err := repo.Get(ctx, "big")
	require.NoError(t, err)
	assert.False(t, found, "oversized payloads must not be persisted")
}

func TestContentCache_PruneBefore(t *testing.T) {
	db := setupContentCacheTestDB(t)
	repo := NewContentCacheRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Set(ctx, "old", types.ContentCacheKindVLM, []byte(`a`)))
	require.NoError(t, repo.Set(ctx, "new", types.ContentCacheKindVLM, []byte(`b`)))
	// Age only the "old" row.
	require.NoError(t, db.Model(&types.ContentCache{}).
		Where("cache_key = ?", "old").
		Update("updated_at", time.Now().Add(-48*time.Hour)).Error)

	n, err := repo.PruneBefore(ctx, time.Now().Add(-24*time.Hour), 10)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	_, found, _ := repo.Get(ctx, "old")
	assert.False(t, found)
	_, found, _ = repo.Get(ctx, "new")
	assert.True(t, found)
}

func TestContentCache_Delete(t *testing.T) {
	db := setupContentCacheTestDB(t)
	repo := NewContentCacheRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Set(ctx, "k", types.ContentCacheKindQuestion, []byte(`["q"]`)))
	require.NoError(t, repo.Delete(ctx, "k"))
	_, found, err := repo.Get(ctx, "k")
	require.NoError(t, err)
	assert.False(t, found)
}
