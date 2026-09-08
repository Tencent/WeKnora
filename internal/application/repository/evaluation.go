package repository

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type evaluationRunRepository struct{ db *gorm.DB }

// NewEvaluationRunRepository creates a database-backed evaluation run repository.
func NewEvaluationRunRepository(db *gorm.DB) interfaces.EvaluationRunRepository {
	return &evaluationRunRepository{db: db}
}

func (r *evaluationRunRepository) Create(ctx context.Context, run *types.EvaluationRun) error {
	return r.db.WithContext(ctx).Create(run).Error
}

func (r *evaluationRunRepository) GetByTaskID(
	ctx context.Context, tenantID uint64, taskID string,
) (*types.EvaluationRun, error) {
	var run types.EvaluationRun
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND task_id = ?", tenantID, taskID).First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &run, err
}

func (r *evaluationRunRepository) MarkRunning(
	ctx context.Context, tenantID uint64, taskID string, startedAt time.Time,
) error {
	res := r.db.WithContext(ctx).Model(&types.EvaluationRun{}).
		Where("tenant_id = ? AND task_id = ? AND status = ?", tenantID, taskID, types.EvaluationStatuePending).
		Updates(map[string]interface{}{"status": types.EvaluationStatueRunning, "started_at": startedAt})
	return transitionError(res, "mark evaluation running")
}

func (r *evaluationRunRepository) UpdateTotal(ctx context.Context, tenantID uint64, taskID string, total int) error {
	if total < 0 {
		return fmt.Errorf("evaluation total must be non-negative")
	}
	res := r.db.WithContext(ctx).Model(&types.EvaluationRun{}).
		Where("tenant_id = ? AND task_id = ? AND status = ?", tenantID, taskID, types.EvaluationStatueRunning).
		Update("total", total)
	return transitionError(res, "update evaluation total")
}

func (r *evaluationRunRepository) IncrementFinished(ctx context.Context, tenantID uint64, taskID string) error {
	res := r.db.WithContext(ctx).Model(&types.EvaluationRun{}).
		Where("tenant_id = ? AND task_id = ? AND status = ?", tenantID, taskID, types.EvaluationStatueRunning).
		Updates(map[string]interface{}{"finished": gorm.Expr("finished + 1")})
	return transitionError(res, "increment evaluation progress")
}

func (r *evaluationRunRepository) MarkSuccess(
	ctx context.Context, tenantID uint64, taskID string,
	metric *types.MetricResult, finishedAt time.Time,
) error {
	updates, err := terminalUpdates(metric, finishedAt)
	if err != nil {
		return err
	}
	updates["status"] = types.EvaluationStatueSuccess
	updates["error_message"] = ""
	return r.markTerminal(ctx, tenantID, taskID, updates, "mark evaluation successful")
}

func (r *evaluationRunRepository) MarkFailed(
	ctx context.Context, tenantID uint64, taskID string,
	metric *types.MetricResult, message string, finishedAt time.Time,
) error {
	updates, err := terminalUpdates(metric, finishedAt)
	if err != nil {
		return err
	}
	updates["status"] = types.EvaluationStatueFailed
	updates["error_message"] = message
	return r.markTerminal(ctx, tenantID, taskID, updates, "mark evaluation failed")
}

func (r *evaluationRunRepository) markTerminal(
	ctx context.Context, tenantID uint64, taskID string,
	updates map[string]interface{}, operation string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run types.EvaluationRun
		if err := tx.Select("started_at").
			Where("tenant_id = ? AND task_id = ? AND status = ?", tenantID, taskID, types.EvaluationStatueRunning).
			First(&run).Error; err != nil {
			return fmt.Errorf("%s: %w", operation, err)
		}
		finishedAt, ok := updates["finished_at"].(time.Time)
		if ok && run.StartedAt != nil {
			updates["duration_ms"] = finishedAt.Sub(*run.StartedAt).Milliseconds()
		}
		res := tx.Model(&types.EvaluationRun{}).
			Where("tenant_id = ? AND task_id = ? AND status = ?", tenantID, taskID, types.EvaluationStatueRunning).
			Updates(updates)
		return transitionError(res, operation)
	})
}

func terminalUpdates(metric *types.MetricResult, finishedAt time.Time) (map[string]interface{}, error) {
	updates := map[string]interface{}{"finished_at": finishedAt}
	if metric == nil {
		return updates, nil
	}
	values := map[string]float64{
		"precision": metric.RetrievalMetrics.Precision, "recall": metric.RetrievalMetrics.Recall,
		"ndcg_3": metric.RetrievalMetrics.NDCG3, "ndcg_10": metric.RetrievalMetrics.NDCG10,
		"mrr": metric.RetrievalMetrics.MRR, "map": metric.RetrievalMetrics.MAP,
		"bleu_1": metric.GenerationMetrics.BLEU1, "bleu_2": metric.GenerationMetrics.BLEU2,
		"bleu_4": metric.GenerationMetrics.BLEU4, "rouge_1": metric.GenerationMetrics.ROUGE1,
		"rouge_2": metric.GenerationMetrics.ROUGE2, "rouge_l": metric.GenerationMetrics.ROUGEL,
	}
	for name, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return nil, fmt.Errorf("invalid evaluation metric %s: %v", name, value)
		}
		updates[name] = value
	}
	return updates, nil
}

func transitionError(res *gorm.DB, operation string) error {
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return fmt.Errorf("%s: run not found or state changed", operation)
	}
	return nil
}
