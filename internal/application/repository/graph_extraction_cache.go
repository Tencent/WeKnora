package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type graphExtractionCacheRepository struct {
	db *gorm.DB
}

func NewGraphExtractionCacheRepository(db *gorm.DB) interfaces.GraphExtractionCacheRepository {
	return &graphExtractionCacheRepository{db: db}
}

func (r *graphExtractionCacheRepository) GetByKey(
	ctx context.Context,
	tenantID uint64,
	cacheKey string,
) (*types.GraphExtractionCache, error) {
	if cacheKey == "" {
		return nil, nil
	}
	var cache types.GraphExtractionCache
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

func (r *graphExtractionCacheRepository) Upsert(ctx context.Context, cache *types.GraphExtractionCache) error {
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
			"graph",
			"updated_at",
		}),
	}).Create(cache).Error
}
