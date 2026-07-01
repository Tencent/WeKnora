package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type wikiMapCacheRepository struct {
	db *gorm.DB
}

func NewWikiMapCacheRepository(db *gorm.DB) interfaces.WikiMapCacheRepository {
	return &wikiMapCacheRepository{db: db}
}

func (r *wikiMapCacheRepository) GetByKey(ctx context.Context, tenantID uint64, cacheKey string) (*types.WikiMapCache, error) {
	if cacheKey == "" {
		return nil, nil
	}
	var cache types.WikiMapCache
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

func (r *wikiMapCacheRepository) Upsert(ctx context.Context, cache *types.WikiMapCache) error {
	if cache == nil || cache.CacheKey == "" {
		return nil
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "cache_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"kind",
			"content_key",
			"model_id",
			"config_hash",
			"schema_ver",
			"payload",
			"updated_at",
		}),
	}).Create(cache).Error
}
