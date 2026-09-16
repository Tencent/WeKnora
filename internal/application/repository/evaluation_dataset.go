package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrEvaluationDatasetNotFound aliases the shared dataset not-found sentinel.
	ErrEvaluationDatasetNotFound = interfaces.ErrEvaluationDatasetNotFound
	// ErrEvaluationDatasetVersionNotFound aliases the shared version not-found sentinel.
	ErrEvaluationDatasetVersionNotFound = interfaces.ErrEvaluationDatasetVersionNotFound
	// ErrEvaluationDatasetAlreadyExists aliases the shared duplicate-dataset sentinel.
	ErrEvaluationDatasetAlreadyExists = interfaces.ErrEvaluationDatasetAlreadyExists
	// ErrEvaluationDatasetVersionConflict aliases the shared version conflict sentinel.
	ErrEvaluationDatasetVersionConflict = interfaces.ErrEvaluationDatasetVersionConflict
	// ErrEvaluationDatasetInvalid aliases the shared invalid-content sentinel.
	ErrEvaluationDatasetInvalid = interfaces.ErrEvaluationDatasetInvalid
)

type evaluationDatasetRepository struct {
	db *gorm.DB
}

// NewEvaluationDatasetRepository constructs a database-backed dataset registry.
func NewEvaluationDatasetRepository(db *gorm.DB) interfaces.EvaluationDatasetRepository {
	return &evaluationDatasetRepository{db: db}
}

// CreateDataset persists one dataset identity within its declared scope.
func (r *evaluationDatasetRepository) CreateDataset(ctx context.Context, dataset *types.EvaluationDataset) error {
	if dataset == nil {
		return errors.New("create evaluation dataset: nil dataset")
	}
	if dataset.ID == "" || dataset.Name == "" {
		return errors.New("create evaluation dataset: id and name are required")
	}
	switch dataset.Scope {
	case types.EvaluationDatasetScopeSystem:
		if dataset.OwnerTenantID != nil {
			return fmt.Errorf("create evaluation dataset: system dataset has no owner: %w", ErrEvaluationDatasetInvalid)
		}
	case types.EvaluationDatasetScopeTenant:
		if dataset.OwnerTenantID == nil || *dataset.OwnerTenantID == 0 {
			return fmt.Errorf("create evaluation dataset: tenant dataset requires owner_tenant_id: %w",
				ErrEvaluationDatasetInvalid)
		}
	default:
		return fmt.Errorf("create evaluation dataset: unknown scope %q: %w", dataset.Scope, ErrEvaluationDatasetInvalid)
	}
	now := time.Now().UTC()
	if dataset.CreatedAt.IsZero() {
		dataset.CreatedAt = now
	}
	if dataset.UpdatedAt.IsZero() {
		dataset.UpdatedAt = dataset.CreatedAt
	}
	dataset.CreatedAt = dataset.CreatedAt.UTC()
	dataset.UpdatedAt = dataset.UpdatedAt.UTC()

	result := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).
		Create(dataset)
	if result.Error != nil {
		return fmt.Errorf("create evaluation dataset %s: %w", dataset.ID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("create evaluation dataset %s: %w", dataset.ID, ErrEvaluationDatasetAlreadyExists)
	}
	return nil
}

// GetDataset returns one dataset only when the tenant may see it.
func (r *evaluationDatasetRepository) GetDataset(
	ctx context.Context,
	tenantID uint64,
	datasetID string,
) (*types.EvaluationDataset, error) {
	var dataset types.EvaluationDataset
	err := r.db.WithContext(ctx).
		Where("id = ? AND (scope = ? OR owner_tenant_id = ?)",
			datasetID, types.EvaluationDatasetScopeSystem, tenantID).
		First(&dataset).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrEvaluationDatasetNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get evaluation dataset %s: %w", datasetID, err)
	}
	normalizeEvaluationDatasetTimes(&dataset)
	return &dataset, nil
}

// ListDatasets returns every dataset visible to the tenant, ordered by id for stability.
func (r *evaluationDatasetRepository) ListDatasets(
	ctx context.Context,
	tenantID uint64,
) ([]*types.EvaluationDataset, error) {
	var datasets []*types.EvaluationDataset
	err := r.db.WithContext(ctx).
		Where("scope = ? OR owner_tenant_id = ?", types.EvaluationDatasetScopeSystem, tenantID).
		Order("id ASC").
		Find(&datasets).Error
	if err != nil {
		return nil, fmt.Errorf("list evaluation datasets: %w", err)
	}
	for _, dataset := range datasets {
		normalizeEvaluationDatasetTimes(dataset)
	}
	return datasets, nil
}

// CreateVersion atomically validates and persists one immutable version with
// its complete content in a single transaction. Referential completeness,
// duplicate identifiers, and declared counts are verified before any write.
func (r *evaluationDatasetRepository) CreateVersion(
	ctx context.Context,
	version *types.EvaluationDatasetVersion,
	content *types.EvaluationDatasetVersionInput,
) error {
	if version == nil || content == nil {
		return errors.New("create evaluation dataset version: version and content are required")
	}
	if err := validateEvaluationDatasetVersionContent(version, content); err != nil {
		return err
	}
	if version.CreatedAt.IsZero() {
		version.CreatedAt = time.Now().UTC()
	}
	version.CreatedAt = version.CreatedAt.UTC()

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var dataset types.EvaluationDataset
		if err := tx.Where("id = ?", version.DatasetID).First(&dataset).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("create evaluation dataset version %s: %w",
					version.ID, ErrEvaluationDatasetNotFound)
			}
			return fmt.Errorf("create evaluation dataset version %s: load dataset: %w", version.ID, err)
		}

		var conflictCount int64
		if err := tx.Model(&types.EvaluationDatasetVersion{}).
			Where("dataset_id = ? AND (version_number = ? OR content_sha256 = ?)",
				version.DatasetID, version.VersionNumber, version.ContentSHA256).
			Count(&conflictCount).Error; err != nil {
			return fmt.Errorf("create evaluation dataset version %s: check conflicts: %w", version.ID, err)
		}
		if conflictCount > 0 {
			return fmt.Errorf("create evaluation dataset version %s: %w",
				version.ID, ErrEvaluationDatasetVersionConflict)
		}

		if err := tx.Create(version).Error; err != nil {
			return fmt.Errorf("create evaluation dataset version %s: %w", version.ID, err)
		}

		passages := make([]types.EvaluationDatasetPassage, 0, len(content.Passages))
		for _, input := range content.Passages {
			metadata := types.JSON(`{}`)
			if len(input.Metadata) != 0 {
				metadata = types.JSON(append(types.JSON(nil), input.Metadata...))
			}
			passages = append(passages, types.EvaluationDatasetPassage{
				DatasetVersionID: version.ID,
				PID:              input.PID,
				Content:          input.Content,
				Metadata:         metadata,
			})
		}
		if len(passages) > 0 {
			if err := tx.CreateInBatches(&passages, 200).Error; err != nil {
				return fmt.Errorf("create evaluation dataset version %s: passages: %w", version.ID, err)
			}
		}

		questions := make([]types.EvaluationDatasetQuestion, 0, len(content.Questions))
		for index, input := range content.Questions {
			questions = append(questions, types.EvaluationDatasetQuestion{
				DatasetVersionID: version.ID,
				QID:              input.QID,
				SampleIndex:      index,
				Question:         input.Question,
				Answer:           input.Answer,
			})
		}
		if len(questions) > 0 {
			if err := tx.CreateInBatches(&questions, 200).Error; err != nil {
				return fmt.Errorf("create evaluation dataset version %s: questions: %w", version.ID, err)
			}
		}

		relevance := make([]types.EvaluationDatasetRelevance, 0, len(content.Relevance))
		for _, input := range content.Relevance {
			relevance = append(relevance, types.EvaluationDatasetRelevance{
				DatasetVersionID: version.ID,
				QID:              input.QID,
				PID:              input.PID,
				Grade:            input.Grade,
			})
		}
		if len(relevance) > 0 {
			if err := tx.CreateInBatches(&relevance, 200).Error; err != nil {
				return fmt.Errorf("create evaluation dataset version %s: relevance: %w", version.ID, err)
			}
		}

		result := tx.Model(&types.EvaluationDataset{}).
			Where("id = ?", version.DatasetID).
			Updates(map[string]any{
				"current_version_id": version.ID,
				"updated_at":         version.CreatedAt,
			})
		if result.Error != nil {
			return fmt.Errorf(
				"create evaluation dataset version %s: update current version: %w",
				version.ID,
				result.Error,
			)
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("create evaluation dataset version %s: update current version: %w",
				version.ID, ErrEvaluationDatasetNotFound)
		}
		return nil
	})
}

// GetVersion returns one version only when its dataset is visible to the tenant.
func (r *evaluationDatasetRepository) GetVersion(
	ctx context.Context,
	tenantID uint64,
	datasetVersionID string,
) (*types.EvaluationDatasetVersion, error) {
	var version types.EvaluationDatasetVersion
	err := r.db.WithContext(ctx).
		Joins("JOIN evaluation_datasets ON evaluation_datasets.id = evaluation_dataset_versions.dataset_id").
		Where(
			"evaluation_dataset_versions.id = ? AND "+
				"(evaluation_datasets.scope = ? OR evaluation_datasets.owner_tenant_id = ?)",
			datasetVersionID, types.EvaluationDatasetScopeSystem, tenantID).
		First(&version).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrEvaluationDatasetVersionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get evaluation dataset version %s: %w", datasetVersionID, err)
	}
	version.CreatedAt = version.CreatedAt.UTC()
	return &version, nil
}

// ListVersions returns all versions of one visible dataset ordered by version_number.
func (r *evaluationDatasetRepository) ListVersions(
	ctx context.Context,
	tenantID uint64,
	datasetID string,
) ([]*types.EvaluationDatasetVersion, error) {
	if _, err := r.GetDataset(ctx, tenantID, datasetID); err != nil {
		return nil, err
	}
	var versions []*types.EvaluationDatasetVersion
	err := r.db.WithContext(ctx).
		Where("dataset_id = ?", datasetID).
		Order("version_number ASC").
		Find(&versions).Error
	if err != nil {
		return nil, fmt.Errorf("list evaluation dataset versions for %s: %w", datasetID, err)
	}
	for _, version := range versions {
		version.CreatedAt = version.CreatedAt.UTC()
	}
	return versions, nil
}

// GetVersionContent returns the complete content of one visible version in
// canonical order: passages by PID, questions by sample_index, relevance by QID/PID.
func (r *evaluationDatasetRepository) GetVersionContent(
	ctx context.Context,
	tenantID uint64,
	datasetVersionID string,
) (*types.EvaluationDatasetVersionContent, error) {
	version, err := r.GetVersion(ctx, tenantID, datasetVersionID)
	if err != nil {
		return nil, err
	}

	content := &types.EvaluationDatasetVersionContent{Version: version}
	if err := r.db.WithContext(ctx).
		Where("dataset_version_id = ?", datasetVersionID).
		Order("pid ASC").
		Find(&content.Passages).Error; err != nil {
		return nil, fmt.Errorf("get evaluation dataset version %s: passages: %w", datasetVersionID, err)
	}
	if err := r.db.WithContext(ctx).
		Where("dataset_version_id = ?", datasetVersionID).
		Order("sample_index ASC").
		Find(&content.Questions).Error; err != nil {
		return nil, fmt.Errorf("get evaluation dataset version %s: questions: %w", datasetVersionID, err)
	}
	if err := r.db.WithContext(ctx).
		Where("dataset_version_id = ?", datasetVersionID).
		Order("qid ASC, pid ASC").
		Find(&content.Relevance).Error; err != nil {
		return nil, fmt.Errorf("get evaluation dataset version %s: relevance: %w", datasetVersionID, err)
	}

	if len(content.Passages) != version.PassageCount ||
		len(content.Questions) != version.QuestionCount ||
		len(content.Relevance) != version.RelevanceCount {
		return nil, fmt.Errorf(
			"get evaluation dataset version %s: stored counts %d/%d/%d differ from content %d/%d/%d: %w",
			datasetVersionID,
			version.PassageCount, version.QuestionCount, version.RelevanceCount,
			len(content.Passages), len(content.Questions), len(content.Relevance),
			ErrEvaluationDatasetInvalid,
		)
	}
	return content, nil
}

func validateEvaluationDatasetVersionContent(
	version *types.EvaluationDatasetVersion,
	content *types.EvaluationDatasetVersionInput,
) error {
	if version.ID == "" || version.DatasetID == "" {
		return errors.New("create evaluation dataset version: id and dataset_id are required")
	}
	if version.VersionNumber < 1 || version.SchemaVersion != types.EvaluationDatasetSchemaVersion {
		return fmt.Errorf("create evaluation dataset version: unsupported version_number %d or schema_version %d: %w",
			version.VersionNumber, version.SchemaVersion, ErrEvaluationDatasetInvalid)
	}
	if len(version.ArtifactSHA256) != 64 || len(version.ContentSHA256) != 64 {
		return fmt.Errorf("create evaluation dataset version: artifact/content SHA-256 must be 64 hex chars: %w",
			ErrEvaluationDatasetInvalid)
	}
	if version.PassageCount != len(content.Passages) ||
		version.QuestionCount != len(content.Questions) ||
		version.RelevanceCount != len(content.Relevance) {
		return fmt.Errorf("create evaluation dataset version: declared counts %d/%d/%d differ from input %d/%d/%d: %w",
			version.PassageCount, version.QuestionCount, version.RelevanceCount,
			len(content.Passages), len(content.Questions), len(content.Relevance),
			ErrEvaluationDatasetInvalid)
	}

	pids := make(map[string]struct{}, len(content.Passages))
	for _, passage := range content.Passages {
		if passage.PID == "" {
			return fmt.Errorf("create evaluation dataset version: empty passage id: %w", ErrEvaluationDatasetInvalid)
		}
		if _, duplicate := pids[passage.PID]; duplicate {
			return fmt.Errorf("create evaluation dataset version: duplicate passage id %q: %w",
				passage.PID, ErrEvaluationDatasetInvalid)
		}
		pids[passage.PID] = struct{}{}
	}

	qids := make(map[string]struct{}, len(content.Questions))
	for _, question := range content.Questions {
		if question.QID == "" {
			return fmt.Errorf("create evaluation dataset version: empty question id: %w", ErrEvaluationDatasetInvalid)
		}
		if _, duplicate := qids[question.QID]; duplicate {
			return fmt.Errorf("create evaluation dataset version: duplicate question id %q: %w",
				question.QID, ErrEvaluationDatasetInvalid)
		}
		qids[question.QID] = struct{}{}
	}

	edges := make(map[string]struct{}, len(content.Relevance))
	for _, edge := range content.Relevance {
		if _, ok := qids[edge.QID]; !ok {
			return fmt.Errorf("create evaluation dataset version: relevance references unknown question %q: %w",
				edge.QID, ErrEvaluationDatasetInvalid)
		}
		if _, ok := pids[edge.PID]; !ok {
			return fmt.Errorf("create evaluation dataset version: relevance references unknown passage %q: %w",
				edge.PID, ErrEvaluationDatasetInvalid)
		}
		if edge.Grade < 0 {
			return fmt.Errorf("create evaluation dataset version: negative relevance grade: %w",
				ErrEvaluationDatasetInvalid)
		}
		key := edge.QID + "\x00" + edge.PID
		if _, duplicate := edges[key]; duplicate {
			return fmt.Errorf("create evaluation dataset version: duplicate relevance edge %q/%q: %w",
				edge.QID, edge.PID, ErrEvaluationDatasetInvalid)
		}
		edges[key] = struct{}{}
	}
	return nil
}

func normalizeEvaluationDatasetTimes(dataset *types.EvaluationDataset) {
	dataset.CreatedAt = dataset.CreatedAt.UTC()
	dataset.UpdatedAt = dataset.UpdatedAt.UTC()
}
