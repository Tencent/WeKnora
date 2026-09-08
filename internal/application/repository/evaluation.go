package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type evaluationRepository struct{ db *gorm.DB }

func NewEvaluationRepository(db *gorm.DB) interfaces.EvaluationRepository {
	return &evaluationRepository{db: db}
}

func evaluationRunFromDetail(detail *types.EvaluationDetail) (*types.EvaluationRun, error) {
	if detail == nil || detail.Task == nil {
		return nil, fmt.Errorf("evaluation detail and task are required")
	}
	b, err := json.Marshal(detail)
	if err != nil {
		return nil, fmt.Errorf("marshal evaluation detail: %w", err)
	}
	return &types.EvaluationRun{
		ID: detail.Task.ID, TenantID: detail.Task.TenantID,
		DatasetID: detail.Task.DatasetID, Status: detail.Task.Status, Detail: b,
	}, nil
}

func (r *evaluationRepository) Create(ctx context.Context, detail *types.EvaluationDetail) error {
	run, err := evaluationRunFromDetail(detail)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Create(run).Error
}

func (r *evaluationRepository) Update(ctx context.Context, detail *types.EvaluationDetail) error {
	run, err := evaluationRunFromDetail(detail)
	if err != nil {
		return err
	}
	res := r.db.WithContext(ctx).Model(&types.EvaluationRun{}).
		Where("id = ? AND tenant_id = ?", run.ID, run.TenantID).
		Updates(map[string]any{"dataset_id": run.DatasetID, "status": run.Status, "detail": run.Detail})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func decodeEvaluationRun(run *types.EvaluationRun) (*types.EvaluationDetail, error) {
	var detail types.EvaluationDetail
	if err := json.Unmarshal(run.Detail, &detail); err != nil {
		return nil, fmt.Errorf("decode evaluation detail: %w", err)
	}
	return &detail, nil
}

func (r *evaluationRepository) Get(ctx context.Context, tenantID uint64, taskID string) (*types.EvaluationDetail, error) {
	var run types.EvaluationRun
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, taskID).First(&run).Error; err != nil {
		return nil, err
	}
	return decodeEvaluationRun(&run)
}

func (r *evaluationRepository) List(ctx context.Context, tenantID uint64, limit int) ([]*types.EvaluationDetail, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	var runs []*types.EvaluationRun
	if err := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("created_at DESC").Limit(limit).Find(&runs).Error; err != nil {
		return nil, err
	}
	result := make([]*types.EvaluationDetail, 0, len(runs))
	for _, run := range runs {
		detail, err := decodeEvaluationRun(run)
		if err != nil {
			return nil, err
		}
		result = append(result, detail)
	}
	return result, nil
}

func (r *evaluationRepository) MarkRunningInterrupted(ctx context.Context) error {
	var runs []*types.EvaluationRun
	if err := r.db.WithContext(ctx).Where("status = ?", types.EvaluationStatueRunning).Find(&runs).Error; err != nil {
		return err
	}
	for _, run := range runs {
		detail, err := decodeEvaluationRun(run)
		if err != nil {
			return err
		}
		detail.Task.Status = types.EvaluationStatueFailed
		detail.Task.ErrMsg = "evaluation interrupted by service restart"
		if err := r.Update(ctx, detail); err != nil {
			return err
		}
	}
	return nil
}
