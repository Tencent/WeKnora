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

const processingArtifactBatchSize = 500

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

func (r *processingArtifactRepository) GetMany(
	ctx context.Context,
	keys []types.ProcessingArtifactKey,
) (map[types.ProcessingArtifactKey]*types.ProcessingArtifact, error) {
	result := make(map[types.ProcessingArtifactKey]*types.ProcessingArtifact, len(keys))
	type keyGroup struct {
		tenantID   uint64
		stage      string
		keyVersion uint16
	}
	groups := make(map[keyGroup]map[string]struct{})
	for _, key := range keys {
		group := keyGroup{tenantID: key.TenantID, stage: key.Stage, keyVersion: key.KeyVersion}
		if groups[group] == nil {
			groups[group] = make(map[string]struct{})
		}
		groups[group][key.InputFingerprint] = struct{}{}
	}

	for group, fingerprints := range groups {
		values := make([]string, 0, len(fingerprints))
		for fingerprint := range fingerprints {
			values = append(values, fingerprint)
		}
		for start := 0; start < len(values); start += processingArtifactBatchSize {
			end := min(start+processingArtifactBatchSize, len(values))
			var artifacts []*types.ProcessingArtifact
			if err := r.db.WithContext(ctx).
				Where("tenant_id = ? AND stage = ? AND key_version = ?", group.tenantID, group.stage, group.keyVersion).
				Where("input_fingerprint IN ?", values[start:end]).
				Find(&artifacts).Error; err != nil {
				return nil, err
			}
			for _, artifact := range artifacts {
				key := types.ProcessingArtifactKey{
					TenantID:         artifact.TenantID,
					Stage:            artifact.Stage,
					KeyVersion:       artifact.KeyVersion,
					InputFingerprint: artifact.InputFingerprint,
				}
				result[key] = artifact
			}
		}
	}
	return result, nil
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

func (r *processingArtifactRepository) PutManyIfAbsent(
	ctx context.Context,
	artifacts []*types.ProcessingArtifact,
) error {
	if len(artifacts) == 0 {
		return nil
	}
	for _, artifact := range artifacts {
		if err := validateProcessingArtifact(artifact); err != nil {
			return err
		}
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "stage"},
			{Name: "key_version"},
			{Name: "input_fingerprint"},
		},
		DoNothing: true,
	}).CreateInBatches(artifacts, processingArtifactBatchSize).Error
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
