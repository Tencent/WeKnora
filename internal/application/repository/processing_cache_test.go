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

func setupProcessingCacheTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.ProcessingCache{}))
	return db
}

func TestProcessingCacheRepository_UpsertGetAndHitTimestamp(t *testing.T) {
	db := setupProcessingCacheTestDB(t)
	repo := NewProcessingCacheRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Upsert(ctx, &types.ProcessingCache{
		TenantID: 1,
		Stage:    types.ProcessingCacheStageVLMOCR,
		CacheKey: "cache-key-1",
		Payload:  types.JSON([]byte(`{"text":"ocr"}`)),
		Metadata: types.JSON([]byte(`{"model_id":"vlm-a"}`)),
	}))

	row, err := repo.Get(ctx, 1, types.ProcessingCacheStageVLMOCR, "cache-key-1")
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.JSONEq(t, `{"text":"ocr"}`, row.Payload.ToString())
	assert.JSONEq(t, `{"model_id":"vlm-a"}`, row.Metadata.ToString())

	var saved types.ProcessingCache
	require.NoError(t, db.First(&saved, "id = ?", row.ID).Error)
	require.NotNil(t, saved.LastHitAt)
}

func TestProcessingCacheRepository_UpsertUpdatesExistingKey(t *testing.T) {
	db := setupProcessingCacheTestDB(t)
	repo := NewProcessingCacheRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Upsert(ctx, &types.ProcessingCache{
		TenantID: 1,
		Stage:    types.ProcessingCacheStageVLMCaption,
		CacheKey: "same-key",
		Payload:  types.JSON([]byte(`{"text":"old"}`)),
		Metadata: types.JSON([]byte(`{"model_id":"vlm-a"}`)),
	}))
	require.NoError(t, repo.Upsert(ctx, &types.ProcessingCache{
		TenantID: 1,
		Stage:    types.ProcessingCacheStageVLMCaption,
		CacheKey: "same-key",
		Payload:  types.JSON([]byte(`{"text":"new"}`)),
		Metadata: types.JSON([]byte(`{"model_id":"vlm-b"}`)),
	}))

	var rows []types.ProcessingCache
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.JSONEq(t, `{"text":"new"}`, rows[0].Payload.ToString())
	assert.JSONEq(t, `{"model_id":"vlm-b"}`, rows[0].Metadata.ToString())
}

func TestProcessingCacheRepository_UpsertRevivesSoftDeletedKey(t *testing.T) {
	db := setupProcessingCacheTestDB(t)
	repo := NewProcessingCacheRepository(db)
	ctx := context.Background()

	cache := &types.ProcessingCache{
		TenantID: 1,
		Stage:    types.ProcessingCacheStageVLMOCR,
		CacheKey: "revive-key",
		Payload:  types.JSON([]byte(`{"text":"old"}`)),
		Metadata: types.JSON([]byte(`{"model_id":"vlm-a"}`)),
	}
	require.NoError(t, repo.Upsert(ctx, cache))
	require.NoError(t, db.Delete(&types.ProcessingCache{}, "id = ?", cache.ID).Error)

	missing, err := repo.Get(ctx, 1, types.ProcessingCacheStageVLMOCR, "revive-key")
	require.NoError(t, err)
	require.Nil(t, missing)

	require.NoError(t, repo.Upsert(ctx, &types.ProcessingCache{
		TenantID: 1,
		Stage:    types.ProcessingCacheStageVLMOCR,
		CacheKey: "revive-key",
		Payload:  types.JSON([]byte(`{"text":"new"}`)),
		Metadata: types.JSON([]byte(`{"model_id":"vlm-b"}`)),
	}))

	row, err := repo.Get(ctx, 1, types.ProcessingCacheStageVLMOCR, "revive-key")
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.JSONEq(t, `{"text":"new"}`, row.Payload.ToString())
	assert.False(t, row.DeletedAt.Valid)
}
