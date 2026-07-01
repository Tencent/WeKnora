package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type vlmImageResultCacheRepository struct {
	db *gorm.DB
}

func NewVLMImageResultCacheRepository(db *gorm.DB) interfaces.VLMImageResultCacheRepository {
	return &vlmImageResultCacheRepository{db: db}
}

func (r *vlmImageResultCacheRepository) GetByKey(
	ctx context.Context,
	tenantID uint64,
	cacheKey string,
) (*types.VLMImageResultCache, error) {
	var entry types.VLMImageResultCache
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND cache_key = ?", tenantID, cacheKey).
		First(&entry).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

func (r *vlmImageResultCacheRepository) PutIfAbsent(
	ctx context.Context,
	entry *types.VLMImageResultCache,
) (bool, error) {
	result := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "cache_key"}},
			DoNothing: true,
		}).
		Create(entry)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}
