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

type processingArtifactRepository struct {
	db *gorm.DB
}

func NewProcessingArtifactRepository(db *gorm.DB) interfaces.ProcessingArtifactRepository {
	return &processingArtifactRepository{db: db}
}

func (r *processingArtifactRepository) GetByCacheKey(
	ctx context.Context,
	tenantID uint64,
	kind types.ProcessingArtifactKind,
	cacheKey string,
) (*types.ProcessingArtifact, error) {
	var artifact types.ProcessingArtifact
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND kind = ? AND cache_key = ?", tenantID, kind, cacheKey).
		First(&artifact).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &artifact, err
}

func (r *processingArtifactRepository) Acquire(
	ctx context.Context,
	candidate *types.ProcessingArtifact,
	leaseOwner string,
	leaseUntil time.Time,
) (*types.ProcessingArtifact, bool, error) {
	now := time.Now().UTC()
	candidate.Status = types.ProcessingArtifactComputing
	candidate.LeaseOwner = leaseOwner
	candidate.LeaseExpiresAt = &leaseUntil
	candidate.LastAccessedAt = now

	result := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(candidate)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 1 {
		return candidate, true, nil
	}

	existing, err := r.GetByCacheKey(ctx, candidate.TenantID, candidate.Kind, candidate.CacheKey)
	if err != nil || existing == nil {
		return existing, false, err
	}
	if existing.Status == types.ProcessingArtifactReady {
		return existing, false, nil
	}
	if existing.Status == types.ProcessingArtifactComputing &&
		existing.LeaseExpiresAt != nil && existing.LeaseExpiresAt.After(now) {
		return existing, false, nil
	}

	updates := map[string]interface{}{
		"status":           types.ProcessingArtifactComputing,
		"lease_owner":      leaseOwner,
		"lease_expires_at": leaseUntil,
		"error_detail":     "",
		"last_accessed_at": now,
		"updated_at":       now,
	}
	result = r.db.WithContext(ctx).Model(&types.ProcessingArtifact{}).
		Where("id = ?", existing.ID).
		Where("status <> ? OR lease_expires_at IS NULL OR lease_expires_at <= ?",
			types.ProcessingArtifactComputing, now).
		Updates(updates)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 0 {
		current, getErr := r.GetByCacheKey(ctx, candidate.TenantID, candidate.Kind, candidate.CacheKey)
		return current, false, getErr
	}

	existing.Status = types.ProcessingArtifactComputing
	existing.LeaseOwner = leaseOwner
	existing.LeaseExpiresAt = &leaseUntil
	return existing, true, nil
}

func (r *processingArtifactRepository) MarkReady(
	ctx context.Context,
	artifact *types.ProcessingArtifact,
) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&types.ProcessingArtifact{}).
		Where("id = ? AND lease_owner = ?", artifact.ID, artifact.LeaseOwner).
		Updates(map[string]interface{}{
			"status":           types.ProcessingArtifactReady,
			"result_json":      artifact.ResultJSON,
			"result_size":      artifact.ResultSize,
			"error_detail":     "",
			"lease_owner":      "",
			"lease_expires_at": nil,
			"last_accessed_at": now,
			"updated_at":       now,
		}).Error
}

func (r *processingArtifactRepository) MarkFailed(
	ctx context.Context,
	artifactID string,
	leaseOwner string,
	detail string,
) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&types.ProcessingArtifact{}).
		Where("id = ? AND lease_owner = ?", artifactID, leaseOwner).
		Updates(map[string]interface{}{
			"status":           types.ProcessingArtifactFailed,
			"error_detail":     detail,
			"lease_owner":      "",
			"lease_expires_at": nil,
			"updated_at":       now,
		}).Error
}

func (r *processingArtifactRepository) TouchHit(
	ctx context.Context,
	artifactID string,
	accessedAt time.Time,
) error {
	return r.db.WithContext(ctx).Model(&types.ProcessingArtifact{}).
		Where("id = ?", artifactID).
		Updates(map[string]interface{}{
			"hit_count":        gorm.Expr("hit_count + 1"),
			"last_accessed_at": accessedAt,
			"updated_at":       accessedAt,
		}).Error
}
