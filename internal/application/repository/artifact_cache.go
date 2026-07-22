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

// artifactCacheRepository implements interfaces.ArtifactCacheRepository.
type artifactCacheRepository struct {
	db *gorm.DB
}

// NewArtifactCacheRepository creates the persistence adapter for the
// content-addressed artifact cache.
func NewArtifactCacheRepository(db *gorm.DB) interfaces.ArtifactCacheRepository {
	return &artifactCacheRepository{db: db}
}

// GetByKey retrieves a single cached entry by its compound unique key.
// Returns nil, nil when no matching row exists or the row is soft-deleted.
func (r *artifactCacheRepository) GetByKey(
	ctx context.Context,
	tenantID uint64,
	cacheKey, cacheType, inputHash, configHash string,
) (*types.ArtifactCache, error) {
	var entry types.ArtifactCache
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND cache_key = ? AND cache_type = ? "+
			"AND input_hash = ? AND config_hash = ?",
			tenantID, cacheKey, cacheType, inputHash, configHash).
		First(&entry).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

// Upsert writes a cache entry.  On conflict (same compound unique key)
// the output, computed_at, and updated_at are refreshed to reflect the
// most recent successful computation.  Safe for concurrent initial fills.
func (r *artifactCacheRepository) Upsert(ctx context.Context, cache *types.ArtifactCache) error {
	if cache.ComputedAt.IsZero() {
		cache.ComputedAt = time.Now()
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"},
				{Name: "cache_key"},
				{Name: "cache_type"},
				{Name: "input_hash"},
				{Name: "config_hash"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"output_json", "output_text", "output_size",
				"computed_at", "expires_at", "updated_at",
			}),
		}).
		Create(cache).Error
}

// DeleteByKeyPrefix permanently removes entries whose cache_key starts with
// the given prefix. Cache payloads are disposable, so hard deletion both
// releases storage and allows the same content address to be filled again.
func (r *artifactCacheRepository) DeleteByKeyPrefix(
	ctx context.Context, tenantID uint64, cacheKeyPrefix string,
) error {
	return r.db.WithContext(ctx).Unscoped().
		Where("tenant_id = ? AND cache_key LIKE ?", tenantID, cacheKeyPrefix+"%").
		Delete(&types.ArtifactCache{}).Error
}

// Validate interface compliance.
var _ interfaces.ArtifactCacheRepository = (*artifactCacheRepository)(nil)
