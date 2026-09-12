package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// ImportDataset uses the dataset primary key to serialize concurrent imports,
// and publishes the identity, version, content and current pointer together.
func (r *evaluationDatasetRepository) ImportDataset(
	ctx context.Context, dataset *types.EvaluationDataset, version *types.EvaluationDatasetVersion,
	content *types.EvaluationDatasetVersionInput,
) (*types.EvaluationDatasetImportResult, error) {
	if dataset == nil || version == nil || content == nil || dataset.Scope != types.EvaluationDatasetScopeTenant ||
		dataset.OwnerTenantID == nil || *dataset.OwnerTenantID == 0 || version.DatasetID != dataset.ID ||
		version.VersionNumber != 1 || len(version.Manifest) == 0 {
		return nil, fmt.Errorf("import dataset: invalid identity or initial version: %w", ErrEvaluationDatasetInvalid)
	}
	if err := validateEvaluationDatasetVersionContent(version, content); err != nil {
		return nil, err
	}
	var result *types.EvaluationDatasetImportResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		repo := &evaluationDatasetRepository{db: tx}
		err := repo.CreateDataset(ctx, dataset)
		if errors.Is(err, ErrEvaluationDatasetAlreadyExists) {
			existing, readErr := repo.GetDataset(ctx, *dataset.OwnerTenantID, dataset.ID)
			if readErr != nil {
				return readErr
			}
			var initial types.EvaluationDatasetVersion
			readErr = tx.Where("dataset_id = ? AND version_number = 1", dataset.ID).First(&initial).Error
			if readErr != nil {
				return fmt.Errorf("import identity has no initial version: %w", ErrEvaluationDatasetVersionConflict)
			}
			var storedManifest, requestedManifest map[string]string
			if json.Unmarshal(initial.Manifest, &storedManifest) != nil ||
				json.Unmarshal(version.Manifest, &requestedManifest) != nil {
				return fmt.Errorf("invalid import manifest: %w", ErrEvaluationDatasetVersionConflict)
			}
			if existing.Scope != types.EvaluationDatasetScopeTenant ||
				!reflect.DeepEqual(storedManifest, requestedManifest) ||
				initial.ArtifactSHA256 != version.ArtifactSHA256 ||
				initial.ContentSHA256 != version.ContentSHA256 {
				return fmt.Errorf("request_id was used with different input: %w", ErrEvaluationDatasetVersionConflict)
			}
			initial.CreatedAt = initial.CreatedAt.UTC()
			result = &types.EvaluationDatasetImportResult{Dataset: existing, Version: &initial, Replayed: true}
			return nil
		}
		if err != nil {
			return err
		}
		if err := repo.CreateVersion(ctx, version, content); err != nil {
			return err
		}
		dataset.CurrentVersionID = version.ID
		dataset.UpdatedAt = version.CreatedAt
		result = &types.EvaluationDatasetImportResult{Dataset: dataset, Version: version}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
