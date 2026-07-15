package repository

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	processingArtifactStagePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	processingArtifactHashPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type processingArtifactRepository struct {
	db *gorm.DB
}

func NewProcessingArtifactRepository(db *gorm.DB) interfaces.ProcessingArtifactRepository {
	return &processingArtifactRepository{db: db}
}

func (r *processingArtifactRepository) Get(
	ctx context.Context,
	key types.ProcessingArtifactKey,
) (*types.ProcessingArtifact, error) {
	var artifact types.ProcessingArtifact
	err := r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND stage = ? AND key_version = ? AND input_fingerprint = ?",
			key.TenantID, key.Stage, key.KeyVersion, key.InputFingerprint,
		).
		First(&artifact).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, types.ErrProcessingArtifactNotFound
	}
	if err != nil {
		return nil, err
	}
	return &artifact, nil
}

func (r *processingArtifactRepository) PutIfAbsent(
	ctx context.Context,
	artifact *types.ProcessingArtifact,
) (bool, error) {
	if err := validateProcessingArtifact(artifact); err != nil {
		return false, err
	}

	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "stage"},
			{Name: "key_version"},
			{Name: "input_fingerprint"},
		},
		DoNothing: true,
	}).Create(artifact)
	return result.RowsAffected == 1, result.Error
}

func (r *processingArtifactRepository) DeleteByID(ctx context.Context, tenantID, id uint64) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Delete(&types.ProcessingArtifact{}).Error
}

func validateProcessingArtifact(artifact *types.ProcessingArtifact) error {
	if artifact == nil {
		return errors.New("processing artifact must not be nil")
	}
	if artifact.TenantID == 0 {
		return errors.New("processing artifact tenant ID must not be zero")
	}
	if !processingArtifactStagePattern.MatchString(artifact.Stage) {
		return fmt.Errorf("invalid processing artifact stage %q", artifact.Stage)
	}
	if artifact.KeyVersion == 0 {
		return errors.New("processing artifact key version must not be zero")
	}
	if !processingArtifactHashPattern.MatchString(artifact.InputFingerprint) {
		return errors.New("processing artifact input fingerprint must be 64 lowercase hex characters")
	}
	if !processingArtifactHashPattern.MatchString(artifact.ContentSHA256) {
		return errors.New("processing artifact content SHA-256 must be 64 lowercase hex characters")
	}
	if artifact.SizeBytes < 0 {
		return errors.New("processing artifact size must not be negative")
	}
	if artifact.ObjectPath == "" && artifact.Payload == nil {
		return errors.New("inline processing artifact payload must not be nil")
	}
	if artifact.ObjectPath != "" && artifact.Payload != nil {
		return errors.New("object processing artifact payload must be nil")
	}
	return nil
}
