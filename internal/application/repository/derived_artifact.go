package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type derivedArtifactRepository struct{ db *gorm.DB }

func NewDerivedArtifactRepository(db *gorm.DB) interfaces.DerivedArtifactRepository {
	return &derivedArtifactRepository{db: db}
}

func (r *derivedArtifactRepository) GetSucceeded(ctx context.Context, tenantID uint64, key string) (*types.DerivedArtifact, error) {
	if tenantID == 0 {
		return nil, fmt.Errorf("get succeeded derived artifact: tenant ID must be non-zero")
	}
	if err := validateArtifactDigest("artifact key", key); err != nil {
		return nil, fmt.Errorf("get succeeded derived artifact: %w", err)
	}
	key = strings.ToLower(key)
	var artifact types.DerivedArtifact
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND artifact_key = ? AND status = ?", tenantID, key, types.DerivedArtifactSucceeded).First(&artifact).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, interfaces.ErrArtifactNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get succeeded derived artifact: %w", err)
	}
	if err := validateSucceededArtifact(&artifact); err != nil {
		return nil, err
	}
	return &artifact, nil
}

func (r *derivedArtifactRepository) Claim(ctx context.Context, input interfaces.ArtifactClaim) (*interfaces.ArtifactClaimResult, error) {
	if err := validateArtifactTenantKeyOwner(input.TenantID, input.ArtifactKey, input.OwnerToken); err != nil {
		return nil, fmt.Errorf("claim derived artifact: %w", err)
	}
	if input.ArtifactKind == "" || len(input.ArtifactKind) > maxArtifactKindLength {
		return nil, fmt.Errorf("claim derived artifact: artifact kind must contain 1 to %d bytes", maxArtifactKindLength)
	}
	if input.LeaseDuration <= 0 {
		return nil, fmt.Errorf("claim derived artifact: lease duration must be positive")
	}
	input.ArtifactKey = strings.ToLower(input.ArtifactKey)
	now := input.Now.UTC()
	if input.Now.IsZero() {
		now = time.Now().UTC()
	}
	lease := now.Add(input.LeaseDuration)
	var result *interfaces.ArtifactClaimResult
	err := r.withSQLiteRetry(ctx, func() error {
		return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			row := types.DerivedArtifact{TenantID: input.TenantID, ArtifactKey: input.ArtifactKey, ArtifactKind: input.ArtifactKind, InputDigest: input.InputDigest, ModelID: input.ModelID, ModelRevision: input.ModelRevision, PromptVersion: input.PromptVersion, ConfigDigest: input.ConfigDigest, ProducerVersion: input.ProducerVersion, Status: types.DerivedArtifactComputing, AttemptCount: 1, OwnerToken: input.OwnerToken, LeaseExpiresAt: &lease}
			created := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "artifact_key"}}, DoNothing: true}).Create(&row)
			if created.Error != nil {
				return created.Error
			}
			var current types.DerivedArtifact
			if created.RowsAffected == 1 {
				// MySQL may report a no-op duplicate-key update as one affected row
				// when CLIENT_FOUND_ROWS is enabled. Confirm the durable owner/status
				// instead of trusting RowsAffected as sole evidence of ownership.
				if err := tx.Where("tenant_id = ? AND artifact_key = ?", input.TenantID, input.ArtifactKey).First(&current).Error; err != nil {
					return err
				}
				if current.Status == types.DerivedArtifactComputing && current.OwnerToken == input.OwnerToken && current.AttemptCount == 1 {
					result = &interfaces.ArtifactClaimResult{Outcome: interfaces.ArtifactClaimClaimed, Artifact: &current}
					return nil
				}
			}

			if current.ID == 0 {
				if err := tx.Where("tenant_id = ? AND artifact_key = ?", input.TenantID, input.ArtifactKey).First(&current).Error; err != nil {
					return err
				}
			}
			if current.Status == types.DerivedArtifactSucceeded {
				if err := validateSucceededArtifact(&current); err != nil {
					return err
				}
				result = &interfaces.ArtifactClaimResult{Outcome: interfaces.ArtifactClaimHit, Artifact: &current}
				return nil
			}
			if current.Status == types.DerivedArtifactComputing && current.LeaseExpiresAt != nil && current.LeaseExpiresAt.After(now) {
				result = &interfaces.ArtifactClaimResult{Outcome: interfaces.ArtifactClaimBusy, Artifact: &current}
				return nil
			}
			if current.Status != types.DerivedArtifactPending && current.Status != types.DerivedArtifactFailed && current.Status != types.DerivedArtifactComputing {
				return interfaces.ErrArtifactInvalidTransition
			}

			takeover := current.Status == types.DerivedArtifactComputing
			q := tx.Model(&types.DerivedArtifact{}).Where("tenant_id = ? AND artifact_key = ? AND status = ?", input.TenantID, input.ArtifactKey, current.Status)
			if current.Status == types.DerivedArtifactComputing {
				q = q.Where("lease_expires_at IS NULL OR lease_expires_at <= ?", now)
			}
			updated := q.Updates(map[string]any{"status": types.DerivedArtifactComputing, "owner_token": input.OwnerToken, "lease_expires_at": lease, "attempt_count": gorm.Expr("attempt_count + 1"), "error_code": "", "error_message": "", "completed_at": nil, "updated_at": now})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected == 0 {
				result = &interfaces.ArtifactClaimResult{Outcome: interfaces.ArtifactClaimBusy}
				return nil
			}
			current.Status, current.OwnerToken, current.LeaseExpiresAt = types.DerivedArtifactComputing, input.OwnerToken, &lease
			current.AttemptCount++
			result = &interfaces.ArtifactClaimResult{Outcome: interfaces.ArtifactClaimClaimed, Artifact: &current, LeaseTakeover: takeover}
			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("claim derived artifact: %w", err)
	}
	return result, nil
}

func (r *derivedArtifactRepository) Complete(ctx context.Context, input interfaces.ArtifactCompletion) error {
	if err := validateArtifactTenantKeyOwner(input.TenantID, input.ArtifactKey, input.OwnerToken); err != nil {
		return fmt.Errorf("complete derived artifact: %w", err)
	}
	digest, err := validateArtifactResult(input.Payload, input.ObjectURI, input.PayloadDigest)
	if err != nil {
		return err
	}
	input.ArtifactKey = strings.ToLower(input.ArtifactKey)
	now := input.CompletedAt.UTC()
	if input.CompletedAt.IsZero() {
		now = time.Now().UTC()
	}
	updates := map[string]any{"status": types.DerivedArtifactSucceeded, "payload": input.Payload, "payload_encoding": input.PayloadEncoding, "object_uri": input.ObjectURI, "payload_digest": digest, "error_code": "", "error_message": "", "owner_token": "", "lease_expires_at": nil, "completed_at": now, "updated_at": now}
	q := r.db.WithContext(ctx).Model(&types.DerivedArtifact{}).Where("tenant_id = ? AND artifact_key = ? AND status = ? AND owner_token = ? AND lease_expires_at > ?", input.TenantID, input.ArtifactKey, types.DerivedArtifactComputing, input.OwnerToken, now).Updates(updates)
	if q.Error != nil {
		return fmt.Errorf("complete derived artifact: %w", q.Error)
	}
	if q.RowsAffected == 1 {
		return nil
	}
	// Exact retries are idempotent; a different result or owner is a conflict.
	var current types.DerivedArtifact
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND artifact_key = ?", input.TenantID, input.ArtifactKey).First(&current).Error; err == nil && current.Status == types.DerivedArtifactSucceeded && current.PayloadDigest == digest && current.PayloadEncoding == input.PayloadEncoding && current.ObjectURI == input.ObjectURI && string(current.Payload) == string(input.Payload) {
		return nil
	}
	return interfaces.ErrArtifactLostOwnership
}

func (r *derivedArtifactRepository) Fail(ctx context.Context, input interfaces.ArtifactFailure) error {
	if err := validateArtifactTenantKeyOwner(input.TenantID, input.ArtifactKey, input.OwnerToken); err != nil {
		return fmt.Errorf("fail derived artifact: %w", err)
	}
	if len(input.ErrorCode) > maxArtifactErrorCode {
		return fmt.Errorf("fail derived artifact: error code exceeds %d bytes", maxArtifactErrorCode)
	}
	input.ArtifactKey = strings.ToLower(input.ArtifactKey)
	now := input.FailedAt.UTC()
	if input.FailedAt.IsZero() {
		now = time.Now().UTC()
	}
	message := truncateArtifactErrorMessage(input.ErrorMessage)
	q := r.db.WithContext(ctx).Model(&types.DerivedArtifact{}).Where("tenant_id = ? AND artifact_key = ? AND status = ? AND owner_token = ? AND lease_expires_at > ?", input.TenantID, input.ArtifactKey, types.DerivedArtifactComputing, input.OwnerToken, now).Updates(map[string]any{"status": types.DerivedArtifactFailed, "error_code": input.ErrorCode, "error_message": message, "owner_token": "", "lease_expires_at": nil, "updated_at": now})
	if q.Error != nil {
		return fmt.Errorf("fail derived artifact: %w", q.Error)
	}
	if q.RowsAffected == 0 {
		return interfaces.ErrArtifactLostOwnership
	}
	return nil
}

func (r *derivedArtifactRepository) RenewLease(ctx context.Context, tenantID uint64, key, owner string, now time.Time, duration time.Duration) error {
	if err := validateArtifactTenantKeyOwner(tenantID, key, owner); err != nil {
		return fmt.Errorf("renew derived artifact lease: %w", err)
	}
	key = strings.ToLower(key)
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if duration <= 0 {
		return fmt.Errorf("renew derived artifact lease: duration must be positive")
	}
	lease := now.Add(duration)
	q := r.db.WithContext(ctx).Model(&types.DerivedArtifact{}).Where("tenant_id = ? AND artifact_key = ? AND status = ? AND owner_token = ? AND lease_expires_at > ? AND lease_expires_at < ?", tenantID, key, types.DerivedArtifactComputing, owner, now, lease).Updates(map[string]any{"lease_expires_at": lease, "updated_at": now})
	if q.Error != nil {
		return fmt.Errorf("renew derived artifact lease: %w", q.Error)
	}
	if q.RowsAffected == 1 {
		return nil
	}
	// A requested renewal that would not extend the current lease is a safe no-op.
	var count int64
	err := r.db.WithContext(ctx).Model(&types.DerivedArtifact{}).Where("tenant_id = ? AND artifact_key = ? AND status = ? AND owner_token = ? AND lease_expires_at > ?", tenantID, key, types.DerivedArtifactComputing, owner, now).Count(&count).Error
	if err == nil && count == 1 {
		return nil
	}
	return interfaces.ErrArtifactLostOwnership
}

func (r *derivedArtifactRepository) withSQLiteRetry(ctx context.Context, fn func() error) error {
	if r.db.Dialector.Name() != "sqlite" {
		return fn()
	}
	var err error
	for i := 0; i < 8; i++ {
		err = fn()
		if err == nil || (!strings.Contains(strings.ToLower(err.Error()), "database is locked") && !strings.Contains(strings.ToLower(err.Error()), "database table is locked")) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(i+1) * 5 * time.Millisecond):
		}
	}
	return err
}
