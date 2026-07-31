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

// contentCacheRepository implements the ContentCacheRepository interface.
type contentCacheRepository struct {
	db *gorm.DB
}

// NewContentCacheRepository creates a new content cache repository.
func NewContentCacheRepository(db *gorm.DB) interfaces.ContentCacheRepository {
	return &contentCacheRepository{db: db}
}

// Get retrieves a cached payload by key.
func (r *contentCacheRepository) Get(ctx context.Context, cacheKey string) ([]byte, bool, error) {
	var row types.ContentCache
	err := r.db.WithContext(ctx).
		Where("cache_key = ?", cacheKey).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return []byte(row.Payload), true, nil
}

// Set upserts a payload for a key. Upsert (rather than insert) keeps the
// operation race-safe: two workers computing the same key concurrently end
// with one row holding an equivalent payload.
func (r *contentCacheRepository) Set(ctx context.Context, cacheKey, kind string, payload []byte) error {
	if len(payload) > types.ContentCachePayloadMaxBytes {
		return nil
	}
	now := time.Now()
	row := types.ContentCache{
		CacheKey:  cacheKey,
		Kind:      kind,
		Payload:   string(payload),
		CreatedAt: now,
		UpdatedAt: now,
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "cache_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"kind", "payload", "updated_at"}),
	}).Create(&row).Error
}

// Delete removes a single cache row.
func (r *contentCacheRepository) Delete(ctx context.Context, cacheKey string) error {
	return r.db.WithContext(ctx).
		Where("cache_key = ?", cacheKey).
		Delete(&types.ContentCache{}).Error
}

// PruneBefore deletes rows updated before the given time, in bounded batches.
func (r *contentCacheRepository) PruneBefore(ctx context.Context, before time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 1000
	}
	var keys []string
	if err := r.db.WithContext(ctx).
		Model(&types.ContentCache{}).
		Where("updated_at < ?", before).
		Limit(limit).
		Pluck("cache_key", &keys).Error; err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		return 0, nil
	}
	if err := r.db.WithContext(ctx).
		Where("cache_key IN ?", keys).
		Delete(&types.ContentCache{}).Error; err != nil {
		return 0, err
	}
	return len(keys), nil
}
