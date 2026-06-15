package repository

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type evaluationRepository struct{ db *gorm.DB }

func NewEvaluationRepository(db *gorm.DB) interfaces.EvaluationRepository {
	return &evaluationRepository{db: db}
}

func (r *evaluationRepository) CreateDataset(ctx context.Context, dataset *types.EvaluationDataset) error {
	return r.db.WithContext(ctx).Create(dataset).Error
}
func (r *evaluationRepository) GetDataset(ctx context.Context, tenantID uint64, id string) (*types.EvaluationDataset, error) {
	var dataset types.EvaluationDataset
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&dataset).Error; err != nil {
		return nil, err
	}
	var count int64
	if err := r.db.WithContext(ctx).Model(&types.EvaluationSample{}).Where("tenant_id = ? AND dataset_id = ?", tenantID, id).Count(&count).Error; err != nil {
		return nil, err
	}
	dataset.SampleCount = int(count)
	return &dataset, nil
}
func (r *evaluationRepository) ListDatasets(ctx context.Context, tenantID uint64, page *types.Pagination) (*types.PageResult, error) {
	var total int64
	var datasets []*types.EvaluationDataset
	q := r.db.WithContext(ctx).Model(&types.EvaluationDataset{}).Where("tenant_id = ?", tenantID)
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	if err := q.Order("created_at DESC").Offset(page.Offset()).Limit(page.Limit()).Find(&datasets).Error; err != nil {
		return nil, err
	}
	for _, d := range datasets {
		var count int64
		if err := r.db.WithContext(ctx).Model(&types.EvaluationSample{}).Where("tenant_id = ? AND dataset_id = ?", tenantID, d.ID).Count(&count).Error; err != nil {
			return nil, err
		}
		d.SampleCount = int(count)
	}
	return types.NewPageResult(total, page, datasets), nil
}
func (r *evaluationRepository) UpdateDataset(ctx context.Context, dataset *types.EvaluationDataset) error {
	return r.db.WithContext(ctx).Save(dataset).Error
}
func (r *evaluationRepository) DeleteDataset(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ? AND dataset_id = ?", tenantID, id).Delete(&types.EvaluationSample{}).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&types.EvaluationDataset{}).Error
	})
}
func (r *evaluationRepository) CreateSample(ctx context.Context, sample *types.EvaluationSample) error {
	return r.db.WithContext(ctx).Create(sample).Error
}
func (r *evaluationRepository) CreateSamples(ctx context.Context, samples []*types.EvaluationSample) error {
	if len(samples) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&samples).Error
}
func (r *evaluationRepository) GetSample(ctx context.Context, tenantID uint64, datasetID, id string) (*types.EvaluationSample, error) {
	var sample types.EvaluationSample
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND dataset_id = ? AND id = ?", tenantID, datasetID, id).First(&sample).Error; err != nil {
		return nil, err
	}
	return &sample, nil
}
func (r *evaluationRepository) ListSamples(ctx context.Context, tenantID uint64, datasetID string, page *types.Pagination) (*types.PageResult, error) {
	var total int64
	var samples []*types.EvaluationSample
	q := r.db.WithContext(ctx).Model(&types.EvaluationSample{}).Where("tenant_id = ? AND dataset_id = ?", tenantID, datasetID)
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	if err := q.Order("created_at ASC").Offset(page.Offset()).Limit(page.Limit()).Find(&samples).Error; err != nil {
		return nil, err
	}
	return types.NewPageResult(total, page, samples), nil
}
func (r *evaluationRepository) ListAllSamples(ctx context.Context, tenantID uint64, datasetID string) ([]*types.EvaluationSample, error) {
	var samples []*types.EvaluationSample
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND dataset_id = ?", tenantID, datasetID).Order("created_at ASC").Find(&samples).Error
	return samples, err
}
func (r *evaluationRepository) UpdateSample(ctx context.Context, sample *types.EvaluationSample) error {
	return r.db.WithContext(ctx).Save(sample).Error
}
func (r *evaluationRepository) DeleteSample(ctx context.Context, tenantID uint64, datasetID, id string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND dataset_id = ? AND id = ?", tenantID, datasetID, id).Delete(&types.EvaluationSample{}).Error
}
func (r *evaluationRepository) CreateRunWithResults(ctx context.Context, run *types.EvaluationRun, results []*types.EvaluationRunResult) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(run).Error; err != nil {
			return err
		}
		if len(results) > 0 {
			return tx.Create(&results).Error
		}
		return nil
	})
}
func (r *evaluationRepository) GetRun(ctx context.Context, tenantID uint64, id string) (*types.EvaluationRun, error) {
	var run types.EvaluationRun
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&run).Error; err != nil {
		return nil, err
	}
	return &run, nil
}
func (r *evaluationRepository) ListRuns(ctx context.Context, tenantID uint64, page *types.Pagination) (*types.PageResult, error) {
	var total int64
	var runs []*types.EvaluationRun
	q := r.db.WithContext(ctx).Model(&types.EvaluationRun{}).Where("tenant_id = ?", tenantID)
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	if err := q.Order("created_at DESC").Offset(page.Offset()).Limit(page.Limit()).Find(&runs).Error; err != nil {
		return nil, err
	}
	return types.NewPageResult(total, page, runs), nil
}
func (r *evaluationRepository) UpdateRun(ctx context.Context, run *types.EvaluationRun) error {
	return r.db.WithContext(ctx).Save(run).Error
}
func (r *evaluationRepository) GetRunResult(ctx context.Context, tenantID uint64, runID, id string) (*types.EvaluationRunResult, error) {
	var result types.EvaluationRunResult
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND run_id = ? AND id = ?", tenantID, runID, id).First(&result).Error; err != nil {
		return nil, err
	}
	return &result, nil
}
func (r *evaluationRepository) ListRunResults(ctx context.Context, tenantID uint64, runID string, page *types.Pagination) (*types.PageResult, error) {
	var total int64
	var results []*types.EvaluationRunResult
	q := r.db.WithContext(ctx).Model(&types.EvaluationRunResult{}).Where("tenant_id = ? AND run_id = ?", tenantID, runID)
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	if err := q.Order("sample_index ASC").Offset(page.Offset()).Limit(page.Limit()).Find(&results).Error; err != nil {
		return nil, err
	}
	return types.NewPageResult(total, page, results), nil
}
func (r *evaluationRepository) ListAllRunResults(ctx context.Context, tenantID uint64, runID string) ([]*types.EvaluationRunResult, error) {
	var results []*types.EvaluationRunResult
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND run_id = ?", tenantID, runID).Order("sample_index ASC").Find(&results).Error
	return results, err
}
func (r *evaluationRepository) UpdateRunResult(ctx context.Context, result *types.EvaluationRunResult) error {
	return r.db.WithContext(ctx).Save(result).Error
}
func (r *evaluationRepository) MarkInterruptedRunsFailed(ctx context.Context, message string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&types.EvaluationRun{}).Where("status IN ?", []types.EvaluationRunStatus{types.EvaluationRunPending, types.EvaluationRunRunning}).Updates(map[string]interface{}{"status": types.EvaluationRunFailed, "error": message, "completed_at": &now}).Error
}
