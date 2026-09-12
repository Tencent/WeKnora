package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrEvaluationQuestionResultConflict aliases the shared conflict sentinel.
	ErrEvaluationQuestionResultConflict = interfaces.ErrEvaluationQuestionResultConflict
	// ErrEvaluationQuestionResultCanceled aliases the shared canceled sentinel.
	ErrEvaluationQuestionResultCanceled = interfaces.ErrEvaluationQuestionResultCanceled
)

type evaluationQuestionResultRepository struct {
	db *gorm.DB
}

// NewEvaluationQuestionResultRepository constructs the per-question repository.
func NewEvaluationQuestionResultRepository(db *gorm.DB) interfaces.EvaluationQuestionResultRepository {
	return &evaluationQuestionResultRepository{db: db}
}

// PublishQuestionResult atomically inserts one per-question row and advances
// the owning task's finished counter, aggregate metric, and version inside
// one transaction. The task update requires the tenant, owner, Running status,
// version, the preceding finished value, an unexpired lease, and absence of a
// cancellation request.
func (r *evaluationQuestionResultRepository) PublishQuestionResult(
	ctx context.Context,
	command interfaces.EvaluationQuestionResultCommand,
) (*types.EvaluationTaskEntity, bool, error) {
	if err := validateEvaluationTaskMutationIdentity(
		command.TenantID, command.TaskID, command.OwnerID, command.ExpectedVersion); err != nil {
		return nil, false, fmt.Errorf("publish evaluation question result: %w", err)
	}
	if command.Result == nil {
		return nil, false, errors.New("publish evaluation question result: result is required")
	}
	if command.Now.IsZero() || command.LeaseExpiresAt.IsZero() {
		return nil, false, errors.New("publish evaluation question result: now and lease_expires_at are required")
	}
	now := command.Now.UTC()
	leaseExpiresAt := command.LeaseExpiresAt.UTC()
	if !leaseExpiresAt.After(now) {
		return nil, false, errors.New("publish evaluation question result: lease_expires_at must be after now")
	}
	if command.Total < 1 || command.Finished < 1 || command.Finished > command.Total {
		return nil, false, errors.New("publish evaluation question result: expected 1 <= finished <= total")
	}
	if command.Result.SampleIndex < 0 || command.Result.SampleIndex >= command.Total {
		return nil, false, errors.New("publish evaluation question result: expected 0 <= sample_index < total")
	}
	if err := validateEvaluationTaskJSONObject(command.Metric, true); err != nil {
		return nil, false, fmt.Errorf("publish evaluation question result: metric: %w", err)
	}
	if err := validateEvaluationTaskJSONObject(command.RuntimeMetrics, true); err != nil {
		return nil, false, fmt.Errorf("publish evaluation question result: runtime_metrics: %w", err)
	}
	row, err := evaluationQuestionResultRowFrom(command)
	if err != nil {
		return nil, false, err
	}
	resultHash := row.ResultHash

	var updatedTask types.EvaluationTaskEntity
	inserted := false
	txErr := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		createResult := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "task_id"}, {Name: "sample_index"}},
			DoNothing: true,
		}).Create(row)
		if createResult.Error != nil {
			return fmt.Errorf("publish evaluation question result %s/%d: insert: %w",
				command.TaskID, row.SampleIndex, createResult.Error)
		}
		if createResult.RowsAffected == 0 {
			var existing types.EvaluationQuestionResultEntity
			if err := tx.Where("tenant_id = ? AND task_id = ? AND sample_index = ?",
				command.TenantID, command.TaskID, row.SampleIndex).
				First(&existing).Error; err != nil {
				return fmt.Errorf("publish evaluation question result %s/%d: load existing: %w",
					command.TaskID, row.SampleIndex, err)
			}
			if existing.ResultHash != resultHash {
				return fmt.Errorf("publish evaluation question result %s/%d: %w",
					command.TaskID, row.SampleIndex, ErrEvaluationQuestionResultConflict)
			}
			// Identical retry: idempotent success without another finished
			// increment or version bump.
			return nil
		}
		inserted = true

		updateResult := tx.Model(&updatedTask).
			Clauses(clause.Returning{}).
			Where("tenant_id = ? AND id = ? AND owner_id = ? AND version = ?",
				command.TenantID, command.TaskID, command.OwnerID, command.ExpectedVersion).
			Where("status = ?", types.EvaluationStatueRunning).
			Where("lease_expires_at > ?", now).
			Where("cancel_requested_at IS NULL").
			Where("(total = 0 OR total = ?) AND finished = ?", command.Total, command.Finished-1).
			Updates(map[string]any{
				"total":           command.Total,
				"finished":        command.Finished,
				"metric":          command.Metric,
				"runtime_metrics": command.RuntimeMetrics,
				"heartbeat_at": gorm.Expr(
					"CASE WHEN heartbeat_at > ? THEN heartbeat_at ELSE ? END", now, now),
				"lease_expires_at": gorm.Expr(
					"CASE WHEN lease_expires_at > ? THEN lease_expires_at ELSE ? END",
					leaseExpiresAt, leaseExpiresAt),
				"updated_at": gorm.Expr(
					"CASE WHEN updated_at > ? THEN updated_at ELSE ? END", now, now),
				"version": gorm.Expr("version + 1"),
			})
		if updateResult.Error != nil {
			return fmt.Errorf("publish evaluation question result %s: task update: %w",
				command.TaskID, updateResult.Error)
		}
		if updateResult.RowsAffected != 1 {
			var taskState types.EvaluationTaskEntity
			stateErr := tx.Select("cancel_requested_at").
				Where("tenant_id = ? AND id = ?", command.TenantID, command.TaskID).
				First(&taskState).Error
			if stateErr == nil && taskState.CancelRequestedAt != nil {
				return fmt.Errorf("publish evaluation question result %s: %w",
					command.TaskID, ErrEvaluationQuestionResultCanceled)
			}
			return fmt.Errorf("publish evaluation question result %s: task state conflict: %w",
				command.TaskID, ErrEvaluationTaskStateConflict)
		}
		return nil
	})
	if txErr != nil {
		return nil, false, txErr
	}
	if !inserted {
		current, err := NewEvaluationTaskRepository(r.db).GetTask(ctx, command.TenantID, command.TaskID)
		if err != nil {
			return nil, false, err
		}
		return current, false, nil
	}
	normalizeEvaluationTaskTimes(&updatedTask)
	return &updatedTask, true, nil
}

// ListQuestionResults returns one keyset page in ascending sample_index,
// excluding soft-deleted rows and honoring the tenant boundary.
func (r *evaluationQuestionResultRepository) ListQuestionResults(
	ctx context.Context,
	tenantID uint64,
	taskID string,
	sampleIndexFrom int,
	limit int,
) ([]*types.EvaluationQuestionResultEntity, error) {
	if err := r.authorizeQuestionResultRead(ctx, tenantID, taskID); err != nil {
		return nil, err
	}
	if limit < 1 {
		limit = types.EvaluationQuestionPageDefaultSize
	}
	if limit > types.EvaluationQuestionPageMaxSize {
		limit = types.EvaluationQuestionPageMaxSize
	}
	var rows []*types.EvaluationQuestionResultEntity
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND task_id = ? AND sample_index >= ?", tenantID, taskID, sampleIndexFrom).
		Order("sample_index ASC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list evaluation question results %s: %w", taskID, err)
	}
	for _, row := range rows {
		normalizeEvaluationQuestionResultTimes(row)
	}
	return rows, nil
}

func normalizeEvaluationQuestionResultTimes(row *types.EvaluationQuestionResultEntity) {
	row.CreatedAt = row.CreatedAt.UTC()
	row.UpdatedAt = row.UpdatedAt.UTC()
	if row.DeletedAt.Valid {
		row.DeletedAt.Time = row.DeletedAt.Time.UTC()
	}
}

func (r *evaluationQuestionResultRepository) authorizeQuestionResultRead(
	ctx context.Context,
	tenantID uint64,
	taskID string,
) error {
	scope, ok := types.TenantAPIKeyScopeFromContext(ctx)
	if !ok || !scope.IsKnowledgeBaseRestricted() {
		return nil
	}
	var task types.EvaluationTaskEntity
	err := r.db.WithContext(ctx).
		Select("dataset_version_id", "dataset_content_sha256", "experiment_snapshot", "experiment_sha256").
		Where("tenant_id = ? AND id = ?", tenantID, taskID).
		First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return interfaces.ErrEvaluationTaskNotFound
	}
	if err != nil {
		return fmt.Errorf("authorize evaluation question results %s: %w", taskID, err)
	}
	complete := task.DatasetVersionID != nil && *task.DatasetVersionID != "" &&
		task.DatasetContentSHA256 != nil && len(*task.DatasetContentSHA256) == 64 &&
		task.ExperimentSHA256 != nil && len(*task.ExperimentSHA256) == 64
	source, sourceOK := types.EvaluationSourceKnowledgeBaseID(task.ExperimentSnapshot)
	if !complete || !sourceOK || !scope.AllowsKnowledgeBase(source) {
		return interfaces.ErrEvaluationTaskNotFound
	}
	return nil
}

func evaluationQuestionResultRowFrom(
	command interfaces.EvaluationQuestionResultCommand,
) (*types.EvaluationQuestionResultEntity, error) {
	input := command.Result
	groundTruth, err := json.Marshal(input.GroundTruthPIDs)
	if err != nil {
		return nil, fmt.Errorf("publish evaluation question result: ground truth: %w", err)
	}
	searchResults, err := json.Marshal(input.SearchResults)
	if err != nil {
		return nil, fmt.Errorf("publish evaluation question result: search results: %w", err)
	}
	rerankResults, err := json.Marshal(input.RerankResults)
	if err != nil {
		return nil, fmt.Errorf("publish evaluation question result: rerank results: %w", err)
	}
	generationPIDs, err := json.Marshal(input.GenerationPIDs)
	if err != nil {
		return nil, fmt.Errorf("publish evaluation question result: generation pids: %w", err)
	}
	perSampleMetrics := types.JSON(`{}`)
	if input.PerSampleMetrics != nil {
		encoded, err := json.Marshal(input.PerSampleMetrics)
		if err != nil {
			return nil, fmt.Errorf("publish evaluation question result: per-sample metrics: %w", err)
		}
		perSampleMetrics = types.JSON(encoded)
	}
	observations := types.JSON(`[]`)
	if input.Observations != nil {
		encoded, err := json.Marshal(input.Observations)
		if err != nil {
			return nil, fmt.Errorf("publish evaluation question result: metric observations: %w", err)
		}
		observations = types.JSON(encoded)
	}
	status := input.Status
	if status == "" {
		status = types.EvaluationQuestionStatusSuccess
	}

	now := command.Now.UTC()
	return &types.EvaluationQuestionResultEntity{
		TenantID:           command.TenantID,
		TaskID:             command.TaskID,
		SampleIndex:        input.SampleIndex,
		QID:                input.QID,
		Question:           input.Question,
		ReferenceAnswer:    input.ReferenceAnswer,
		GroundTruthPIDs:    types.JSON(groundTruth),
		SearchResults:      types.JSON(searchResults),
		RerankResults:      types.JSON(rerankResults),
		GenerationPIDs:     types.JSON(generationPIDs),
		GeneratedText:      input.GeneratedText,
		ErrorCode:          input.ErrorCode,
		PerSampleMetrics:   perSampleMetrics,
		MetricObservations: observations,
		RetrievalMs:        input.RetrievalMs,
		RerankMs:           input.RerankMs,
		GenerationMs:       input.GenerationMs,
		TotalMs:            input.TotalMs,
		PromptTokens:       input.PromptTokens,
		CompletionTokens:   input.CompletionTokens,
		TotalTokens:        input.TotalTokens,
		UsageReported:      input.UsageReported,
		Status:             status,
		ResultHash:         types.EvaluationQuestionResultHash(input),
		CreatedAt:          now,
		UpdatedAt:          now,
	}, nil
}
