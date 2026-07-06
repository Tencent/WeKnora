package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type imageMultimodalCacheRepository struct {
	db *gorm.DB
}

func NewImageMultimodalCacheRepository(db *gorm.DB) interfaces.ImageMultimodalCacheRepository {
	return &imageMultimodalCacheRepository{db: db}
}

func (r *imageMultimodalCacheRepository) GetByKey(
	ctx context.Context,
	tenantID uint64,
	cacheKey string,
) (*types.ImageMultimodalCache, error) {
	if cacheKey == "" {
		return nil, nil
	}
	var cache types.ImageMultimodalCache
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND cache_key = ?", tenantID, cacheKey).
		First(&cache).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cache, nil
}

func (r *imageMultimodalCacheRepository) Upsert(ctx context.Context, cache *types.ImageMultimodalCache) error {
	if cache == nil || cache.CacheKey == "" {
		return nil
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "cache_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"content_key",
			"model_id",
			"config_hash",
			"schema_ver",
			"payload",
			"updated_at",
		}),
	}).Create(cache).Error
}
