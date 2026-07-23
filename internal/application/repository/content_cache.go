package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type contentCacheRepository struct {
	db *gorm.DB
}

func NewContentCacheRepository(db *gorm.DB) interfaces.ContentCacheRepository {
	return &contentCacheRepository{db: db}
}

func (r *contentCacheRepository) GetByKey(
	ctx context.Context,
	tenantID uint64,
	cacheKind, cacheKey string,
) (*types.ContentCacheEntry, error) {
	var entry types.ContentCacheEntry
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND cache_kind = ? AND cache_key = ?", tenantID, cacheKind, cacheKey).
		First(&entry).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &entry, nil
}

func (r *contentCacheRepository) Upsert(ctx context.Context, entry *types.ContentCacheEntry) error {
	if entry == nil {
		return nil
	}
	entry.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "cache_kind"},
			{Name: "cache_key"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"payload", "updated_at"}),
	}).Create(entry).Error
}
