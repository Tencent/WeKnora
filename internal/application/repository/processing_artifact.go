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

var ErrProcessingArtifactNotFound = errors.New("processing artifact not found")

type processingArtifactRepository struct {
	db *gorm.DB
}

// NewProcessingArtifactRepository creates an artifact repository.
func NewProcessingArtifactRepository(db *gorm.DB) interfaces.ProcessingArtifactRepository {
	return &processingArtifactRepository{db: db}
}

func (r *processingArtifactRepository) PutIfAbsent(
	ctx context.Context, artifact *types.ProcessingArtifact,
) (bool, error) {
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now()
	}
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(artifact)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func (r *processingArtifactRepository) Get(
	ctx context.Context, tenantID uint64, stage string, keyVersion int, artifactKey string,
) (*types.ProcessingArtifact, error) {
	var artifact types.ProcessingArtifact
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND stage = ? AND key_version = ? AND artifact_key = ?",
			tenantID, stage, keyVersion, artifactKey).
		Where("expires_at IS NULL OR expires_at > ?", time.Now()).
		First(&artifact).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrProcessingArtifactNotFound
		}
		return nil, err
	}
	now := time.Now()
	_ = r.db.WithContext(ctx).Model(&types.ProcessingArtifact{}).
		Where("id = ?", artifact.ID).
		Updates(map[string]interface{}{
			"hit_count":   gorm.Expr("hit_count + 1"),
			"last_hit_at": now,
		}).Error
	return &artifact, nil
}

func (r *processingArtifactRepository) DeleteObservedChecksum(
	ctx context.Context, tenantID uint64, id string, payloadChecksum string,
) (bool, error) {
	result := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ? AND payload_checksum = ?", tenantID, id, payloadChecksum).
		Delete(&types.ProcessingArtifact{})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (r *processingArtifactRepository) DeleteExpired(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}
	var ids []string
	if err := r.db.WithContext(ctx).Model(&types.ProcessingArtifact{}).
		Where("expires_at IS NOT NULL AND expires_at <= ?", before).
		Order("expires_at ASC").
		Limit(limit).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	result := r.db.WithContext(ctx).Where("id IN ?", ids).Delete(&types.ProcessingArtifact{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}
