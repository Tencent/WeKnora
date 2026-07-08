package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type processingCacheRepository struct {
	db *gorm.DB
}

func NewProcessingCacheRepository(db *gorm.DB) interfaces.ProcessingCacheRepository {
	return &processingCacheRepository{db: db}
}

func (r *processingCacheRepository) Get(ctx context.Context, tenantID uint64, stage, cacheKey string) (*types.ProcessingCache, error) {
	var row types.ProcessingCache
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND stage = ? AND cache_key = ?", tenantID, stage, cacheKey).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	now := time.Now()
	_ = r.db.WithContext(ctx).Model(&types.ProcessingCache{}).
		Where("id = ?", row.ID).
		Update("last_hit_at", now).Error
	return &row, nil
}

func (r *processingCacheRepository) Upsert(ctx context.Context, cache *types.ProcessingCache) error {
	if cache == nil {
		return errors.New("processing cache: nil cache")
	}
	if cache.ID == "" {
		cache.ID = uuid.New().String()
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "stage"},
			{Name: "cache_key"},
		},
		DoUpdates: append(clause.AssignmentColumns([]string{
			"payload",
			"metadata",
			"updated_at",
		}), clause.Assignment{
			Column: clause.Column{Name: "deleted_at"},
			Value:  nil,
		}),
	}).Create(cache).Error
}
