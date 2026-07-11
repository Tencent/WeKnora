package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type multimodalResultCacheRepository struct {
	db *gorm.DB
}

func NewMultimodalResultCacheRepository(db *gorm.DB) interfaces.MultimodalResultCacheRepository {
	return &multimodalResultCacheRepository{db: db}
}

func (r *multimodalResultCacheRepository) GetByKey(
	ctx context.Context,
	tenantID uint64,
	cacheKey string,
) (*types.MultimodalResultCache, error) {
	if cacheKey == "" {
		return nil, nil
	}
	var cache types.MultimodalResultCache
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

func (r *multimodalResultCacheRepository) Upsert(ctx context.Context, cache *types.MultimodalResultCache) error {
	if cache == nil || cache.CacheKey == "" || cache.Content == "" {
		return nil
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "cache_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"image_hash",
			"model_id",
			"prompt_hash",
			"output_type",
			"schema_ver",
			"content",
			"updated_at",
		}),
	}).Create(cache).Error
}
