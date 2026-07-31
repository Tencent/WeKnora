package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type documentParseCacheRepository struct {
	db *gorm.DB
}

func NewDocumentParseCacheRepository(db *gorm.DB) interfaces.DocumentParseCacheRepository {
	return &documentParseCacheRepository{db: db}
}

func (r *documentParseCacheRepository) GetByKey(
	ctx context.Context,
	tenantID uint64,
	knowledgeID string,
	cacheKey string,
) (*types.DocumentParseCache, error) {
	if knowledgeID == "" || cacheKey == "" {
		return nil, nil
	}
	var cache types.DocumentParseCache
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_id = ? AND cache_key = ?", tenantID, knowledgeID, cacheKey).
		First(&cache).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cache, nil
}

func (r *documentParseCacheRepository) Upsert(ctx context.Context, cache *types.DocumentParseCache) error {
	if cache == nil || cache.KnowledgeID == "" || cache.CacheKey == "" {
		return nil
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "knowledge_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"cache_key",
			"content_key",
			"config_hash",
			"schema_ver",
			"payload",
			"updated_at",
		}),
	}).Create(cache).Error
}
