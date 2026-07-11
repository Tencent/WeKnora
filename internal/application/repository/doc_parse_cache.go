package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type docParseCacheRepository struct {
	db *gorm.DB
}

func NewDocParseCacheRepository(db *gorm.DB) interfaces.DocParseCacheRepository {
	return &docParseCacheRepository{db: db}
}

func (r *docParseCacheRepository) GetByKey(ctx context.Context, tenantID uint64, cacheKey string) (*types.DocParseCache, error) {
	if cacheKey == "" {
		return nil, nil
	}
	var cache types.DocParseCache
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

func (r *docParseCacheRepository) Upsert(ctx context.Context, cache *types.DocParseCache) error {
	if cache == nil || cache.CacheKey == "" || len(cache.Payload) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "cache_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"content_hash",
			"parser",
			"config_hash",
			"schema_ver",
			"payload",
			"updated_at",
		}),
	}).Create(cache).Error
}
