package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/modelcache"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type embeddingCacheRepository struct{ db *gorm.DB }

// NewEmbeddingCacheRepository creates the persistent embedding cache store.
func NewEmbeddingCacheRepository(db *gorm.DB) modelcache.Store {
	return &embeddingCacheRepository{db: db}
}

func (r *embeddingCacheRepository) GetEmbeddingCache(
	ctx context.Context,
	prefix modelcache.CachePrefix,
	hashes []string,
) (map[string]*types.EmbeddingCacheEntry, error) {
	result := make(map[string]*types.EmbeddingCacheEntry)
	if len(hashes) == 0 {
		return result, nil
	}
	if prefix.TenantID == 0 || prefix.ModelID == "" || prefix.ModelFingerprint == "" ||
		prefix.RequestOptionsSHA256 == "" {
		return nil, errors.New("get embedding cache: complete prefix is required")
	}
	now := time.Now().UTC()
	var entries []*types.EmbeddingCacheEntry
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND model_id = ? AND model_fingerprint = ? AND request_options_sha256 = ?",
			prefix.TenantID, prefix.ModelID, prefix.ModelFingerprint, prefix.RequestOptionsSHA256).
		Where("text_sha256 IN ? AND expires_at > ?", hashes, now).
		Find(&entries).Error
	if err != nil {
		return nil, fmt.Errorf("get embedding cache: %w", err)
	}
	for _, entry := range entries {
		result[entry.TextSHA256] = entry
	}
	if len(entries) > 0 {
		_ = r.db.WithContext(ctx).Model(&types.EmbeddingCacheEntry{}).
			Where(
				"tenant_id = ? AND model_id = ? AND model_fingerprint = ? "+
					"AND request_options_sha256 = ? AND text_sha256 IN ?",
				prefix.TenantID, prefix.ModelID, prefix.ModelFingerprint, prefix.RequestOptionsSHA256, hashes).
			Updates(map[string]any{"accessed_at": now, "updated_at": now}).Error
	}
	return result, nil
}

func (r *embeddingCacheRepository) PutEmbeddingCache(ctx context.Context, entries []*types.EmbeddingCacheEntry) error {
	if len(entries) == 0 {
		return nil
	}
	for _, entry := range entries {
		if entry == nil || entry.TenantID == 0 || entry.ModelID == "" || entry.ModelFingerprint == "" ||
			entry.RequestOptionsSHA256 == "" || entry.TextSHA256 == "" || len(entry.Embedding) == 0 ||
			entry.Dimension <= 0 || entry.ExpiresAt.IsZero() {
			return errors.New("put embedding cache: complete validated entry is required")
		}
	}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "model_id"},
			{Name: "model_fingerprint"},
			{Name: "request_options_sha256"},
			{Name: "text_sha256"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"embedding", "dimension", "expires_at", "accessed_at", "updated_at",
		}),
	}).Create(&entries).Error; err != nil {
		return fmt.Errorf("put embedding cache: %w", err)
	}
	return nil
}

func (r *embeddingCacheRepository) DeleteExpiredEmbeddingCache(ctx context.Context, limit int) (int64, error) {
	if limit <= 0 {
		return 0, errors.New("delete expired embedding cache: positive limit is required")
	}
	var entries []*types.EmbeddingCacheEntry
	if err := r.db.WithContext(ctx).Where("expires_at <= ?", time.Now().UTC()).
		Order("expires_at ASC").Limit(limit).Find(&entries).Error; err != nil {
		return 0, fmt.Errorf("list expired embedding cache: %w", err)
	}
	if len(entries) == 0 {
		return 0, nil
	}
	result := r.db.WithContext(ctx).Delete(&entries)
	if result.Error != nil {
		return 0, fmt.Errorf("delete expired embedding cache: %w", result.Error)
	}
	return result.RowsAffected, nil
}

func (r *embeddingCacheRepository) RecordEmbeddingCacheLookup(
	ctx context.Context,
	record *types.EmbeddingCacheLookupRecord,
) error {
	if record == nil || record.ID == "" || record.TenantID == 0 || record.ModelID == "" ||
		record.RequestedItems <= 0 || record.UniqueItems <= 0 || record.UniqueItems > record.RequestedItems ||
		record.HitItems < 0 || record.MissItems < 0 || record.BypassItems < 0 || record.DurationMs < 0 ||
		record.HitItems+record.MissItems+record.BypassItems != record.UniqueItems ||
		record.OccurredAt.IsZero() || !validEmbeddingCacheLookupStatus(record) {
		return errors.New("record embedding cache lookup: complete non-negative record is required")
	}
	record.OccurredAt = record.OccurredAt.UTC()
	if err := r.db.WithContext(ctx).Create(record).Error; err != nil {
		return fmt.Errorf("record embedding cache lookup: %w", err)
	}
	return nil
}

func validEmbeddingCacheLookupStatus(record *types.EmbeddingCacheLookupRecord) bool {
	switch record.Status {
	case types.EmbeddingCacheLookupStatusHit:
		return record.HitItems == record.UniqueItems && record.MissItems == 0 && record.BypassItems == 0
	case types.EmbeddingCacheLookupStatusMiss:
		return record.HitItems == 0 && record.MissItems == record.UniqueItems && record.BypassItems == 0
	case types.EmbeddingCacheLookupStatusPartial:
		return record.HitItems > 0 && record.MissItems > 0 && record.BypassItems == 0
	case types.EmbeddingCacheLookupStatusBypass:
		return record.HitItems == 0 && record.MissItems == 0 && record.BypassItems == record.UniqueItems
	default:
		return false
	}
}

var (
	_ modelcache.Store      = (*embeddingCacheRepository)(nil)
	_ modelcache.EventStore = (*embeddingCacheRepository)(nil)
)
