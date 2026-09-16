package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrEvaluationTaskNotFound aliases the shared repository not-found sentinel.
	ErrEvaluationTaskNotFound = interfaces.ErrEvaluationTaskNotFound
	// ErrEvaluationTaskAlreadyExists aliases the shared repository duplicate sentinel.
	ErrEvaluationTaskAlreadyExists = interfaces.ErrEvaluationTaskAlreadyExists
	// ErrEvaluationTaskTenantMismatch aliases the shared repository tenant sentinel.
	ErrEvaluationTaskTenantMismatch = interfaces.ErrEvaluationTaskTenantMismatch
	// ErrEvaluationTaskOwnerConflict aliases the shared repository owner sentinel.
	ErrEvaluationTaskOwnerConflict = interfaces.ErrEvaluationTaskOwnerConflict
	// ErrEvaluationTaskVersionConflict aliases the shared repository version sentinel.
	ErrEvaluationTaskVersionConflict = interfaces.ErrEvaluationTaskVersionConflict
	// ErrEvaluationTaskStateConflict aliases the shared repository state sentinel.
	ErrEvaluationTaskStateConflict = interfaces.ErrEvaluationTaskStateConflict
)

type evaluationTaskRepository struct {
	db *gorm.DB
}

// NewEvaluationTaskRepository constructs a database-backed task repository.
func NewEvaluationTaskRepository(db *gorm.DB) interfaces.EvaluationTaskRepository {
	return &evaluationTaskRepository{db: db}
}

// CreateTask persists the initial task snapshot within an explicit tenant boundary.
func (r *evaluationTaskRepository) CreateTask(
	ctx context.Context,
	tenantID uint64,
	task *types.EvaluationTaskEntity,
) error {
	if task == nil {
		return errors.New("create evaluation task: nil task")
	}
	if tenantID == 0 || task.ID == "" || task.TenantID == 0 || task.DatasetID == "" {
		return errors.New("create evaluation task: id, tenant_id, and dataset_id are required")
	}
	if tenantID != task.TenantID {
		return fmt.Errorf(
			"create evaluation task: boundary tenant_id %d, entity tenant_id %d: %w",
			tenantID,
			task.TenantID,
			ErrEvaluationTaskTenantMismatch,
		)
	}
	if task.TemporaryKnowledgeBaseID == "" || task.OwnerID == "" {
		return errors.New("create evaluation task: temporary_kb_id and owner_id are required")
	}
	if task.LeaseExpiresAt == nil || task.LeaseExpiresAt.IsZero() {
		return errors.New("create evaluation task: lease_expires_at is required")
	}
	if err := validateInitialEvaluationTask(task); err != nil {
		return err
	}
	now := time.Now().UTC()
	if task.StartTime.IsZero() {
		task.StartTime = now
	}
	if task.HeartbeatAt.IsZero() {
		task.HeartbeatAt = task.StartTime
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	if task.Version == 0 {
		task.Version = 1
	}
	if len(task.CleanupErrors) == 0 {
		task.CleanupErrors = types.JSON(`[]`)
	}
	if len(task.Params) == 0 {
		task.Params = types.JSON(`{}`)
	}
	normalizeEvaluationTaskTimes(task)
	if task.StartTime.After(task.HeartbeatAt) || task.HeartbeatAt.After(*task.LeaseExpiresAt) {
		return errors.New("create evaluation task: expected start_time <= heartbeat_at <= lease_expires_at")
	}

	result := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).
		Create(task)
	if result.Error != nil {
		return fmt.Errorf("create evaluation task %s: %w", task.ID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("create evaluation task %s: %w", task.ID, ErrEvaluationTaskAlreadyExists)
	}
	return nil
}

// GetTask returns one task only when it belongs to the requested tenant.
func (r *evaluationTaskRepository) GetTask(
	ctx context.Context,
	tenantID uint64,
	taskID string,
) (*types.EvaluationTaskEntity, error) {
	var task types.EvaluationTaskEntity
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, taskID).
		First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrEvaluationTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get evaluation task %s: %w", taskID, err)
	}
	normalizeEvaluationTaskTimes(&task)
	return &task, nil
}

// GetTasksByIDs returns a tenant-scoped batch projection in one query.
func (r *evaluationTaskRepository) GetTasksByIDs(
	ctx context.Context,
	tenantID uint64,
	taskIDs []string,
) (map[string]*types.EvaluationTaskEntity, error) {
	tasksByID := make(map[string]*types.EvaluationTaskEntity, len(taskIDs))
	if tenantID == 0 || len(taskIDs) == 0 {
		return tasksByID, nil
	}
	var tasks []*types.EvaluationTaskEntity
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id IN ?", tenantID, taskIDs).
		Find(&tasks).Error; err != nil {
		return nil, fmt.Errorf("get evaluation tasks by ids: %w", err)
	}
	for _, task := range tasks {
		tasksByID[task.ID] = task
	}
	return tasksByID, nil
}

// TryStartTask moves one lease-valid pending task to Running for its current owner.
func (r *evaluationTaskRepository) TryStartTask(
	ctx context.Context,
	command types.EvaluationTaskStartCommand,
) (*types.EvaluationTaskEntity, error) {
	if err := validateEvaluationTaskMutationIdentity(
		command.TenantID,
		command.TaskID,
		command.OwnerID,
		command.ExpectedVersion,
	); err != nil {
		return nil, fmt.Errorf("start evaluation task: %w", err)
	}
	if command.Now.IsZero() || command.LeaseExpiresAt.IsZero() {
		return nil, errors.New("start evaluation task: now and lease_expires_at are required")
	}
	command.Now = command.Now.UTC()
	command.LeaseExpiresAt = command.LeaseExpiresAt.UTC()
	if !command.LeaseExpiresAt.After(command.Now) {
		return nil, errors.New("start evaluation task: lease_expires_at must be after now")
	}

	return r.updateEvaluationTask(
		ctx,
		command.TenantID,
		command.TaskID,
		command.OwnerID,
		command.ExpectedVersion,
		[]types.EvaluationStatue{types.EvaluationStatuePending},
		"lease_expires_at > ? AND start_time <= ?",
		[]any{command.Now, command.Now},
		map[string]any{
			"status":         types.EvaluationStatueRunning,
			"end_time":       nil,
			"err_msg":        "",
			"cleanup_errors": types.JSON(`[]`),
			"heartbeat_at": gorm.Expr(
				"CASE WHEN heartbeat_at > ? THEN heartbeat_at ELSE ? END",
				command.Now, command.Now,
			),
			"lease_expires_at": gorm.Expr(
				"CASE WHEN lease_expires_at > ? THEN lease_expires_at ELSE ? END",
				command.LeaseExpiresAt, command.LeaseExpiresAt,
			),
			"updated_at": gorm.Expr(
				"CASE WHEN updated_at > ? THEN updated_at ELSE ? END",
				command.Now, command.Now,
			),
			"version": gorm.Expr("version + 1"),
		},
		func(task *types.EvaluationTaskEntity) bool {
			return task.Status == types.EvaluationStatuePending &&
				task.LeaseExpiresAt != nil && task.LeaseExpiresAt.After(command.Now) &&
				!task.StartTime.After(command.Now)
		},
		"start",
	)
}

// HeartbeatTask renews one running task lease without changing its business version.
func (r *evaluationTaskRepository) HeartbeatTask(
	ctx context.Context,
	command types.EvaluationTaskHeartbeatCommand,
) (*types.EvaluationTaskEntity, error) {
	if command.TenantID == 0 || command.TaskID == "" || command.OwnerID == "" {
		return nil, errors.New("heartbeat evaluation task: tenant_id, task_id, and owner_id are required")
	}
	if command.Now.IsZero() || command.LeaseExpiresAt.IsZero() {
		return nil, errors.New("heartbeat evaluation task: now and lease_expires_at are required")
	}
	command.Now = command.Now.UTC()
	command.LeaseExpiresAt = command.LeaseExpiresAt.UTC()
	if !command.LeaseExpiresAt.After(command.Now) {
		return nil, errors.New("heartbeat evaluation task: lease_expires_at must be after now")
	}

	var updated types.EvaluationTaskEntity
	result := r.db.WithContext(ctx).
		Model(&updated).
		Clauses(clause.Returning{}).
		Where(
			"tenant_id = ? AND id = ? AND owner_id = ? AND status = ? AND lease_expires_at > ?",
			command.TenantID,
			command.TaskID,
			command.OwnerID,
			types.EvaluationStatueRunning,
			command.Now,
		).
		Updates(map[string]any{
			"heartbeat_at": gorm.Expr(
				"CASE WHEN heartbeat_at > ? THEN heartbeat_at ELSE ? END",
				command.Now, command.Now,
			),
			"lease_expires_at": gorm.Expr(
				"CASE WHEN lease_expires_at > ? THEN lease_expires_at ELSE ? END",
				command.LeaseExpiresAt, command.LeaseExpiresAt,
			),
			"updated_at": gorm.Expr(
				"CASE WHEN updated_at > ? THEN updated_at ELSE ? END",
				command.Now, command.Now,
			),
		})
	if result.Error != nil {
		return nil, fmt.Errorf("heartbeat evaluation task %s: %w", command.TaskID, result.Error)
	}
	if result.RowsAffected > 1 {
		return nil, fmt.Errorf(
			"heartbeat evaluation task %s: invariant violation: updated %d rows",
			command.TaskID,
			result.RowsAffected,
		)
	}
	if result.RowsAffected == 0 {
		return nil, r.classifyEvaluationTaskHeartbeatConflict(ctx, command)
	}
	normalizeEvaluationTaskTimes(&updated)
	return &updated, nil
}

// ClaimExpiredTasks atomically assigns expired active tasks to a recovery owner.
func (r *evaluationTaskRepository) ClaimExpiredTasks(
	ctx context.Context,
	command types.EvaluationTaskClaimExpiredCommand,
) ([]*types.EvaluationTaskEntity, error) {
	if command.OwnerID == "" {
		return nil, errors.New("claim expired evaluation tasks: owner_id is required")
	}
	if command.Now.IsZero() || command.LeaseExpiresAt.IsZero() {
		return nil, errors.New("claim expired evaluation tasks: now and lease_expires_at are required")
	}
	if command.Limit <= 0 {
		return nil, errors.New("claim expired evaluation tasks: limit must be positive")
	}
	command.Now = command.Now.UTC()
	command.LeaseExpiresAt = command.LeaseExpiresAt.UTC()
	if !command.LeaseExpiresAt.After(command.Now) {
		return nil, errors.New("claim expired evaluation tasks: lease_expires_at must be after now")
	}

	activeStatuses := []types.EvaluationStatue{
		types.EvaluationStatuePending,
		types.EvaluationStatueRunning,
	}
	candidates := r.db.WithContext(ctx).
		Model(&types.EvaluationTaskEntity{}).
		Select("id").
		Where("status IN ? AND lease_expires_at <= ?", activeStatuses, command.Now).
		Order("lease_expires_at ASC").
		Order("id ASC").
		Limit(command.Limit)

	var claimed []*types.EvaluationTaskEntity
	result := r.db.WithContext(ctx).
		Model(&claimed).
		Clauses(clause.Returning{}).
		Where("id IN (?)", candidates).
		Where("status IN ? AND lease_expires_at <= ?", activeStatuses, command.Now).
		Updates(map[string]any{
			"owner_id": command.OwnerID,
			"heartbeat_at": gorm.Expr(
				"CASE WHEN heartbeat_at > ? THEN heartbeat_at ELSE ? END",
				command.Now, command.Now,
			),
			"lease_expires_at": command.LeaseExpiresAt,
			"updated_at": gorm.Expr(
				"CASE WHEN updated_at > ? THEN updated_at ELSE ? END",
				command.Now, command.Now,
			),
			"version": gorm.Expr("version + 1"),
		})
	if result.Error != nil {
		return nil, fmt.Errorf("claim expired evaluation tasks: %w", result.Error)
	}
	if result.RowsAffected > int64(command.Limit) {
		return nil, fmt.Errorf(
			"claim expired evaluation tasks: invariant violation: claimed %d rows with limit %d",
			result.RowsAffected,
			command.Limit,
		)
	}
	for _, task := range claimed {
		normalizeEvaluationTaskTimes(task)
	}
	return claimed, nil
}

// PublishProgress atomically publishes a complete progress snapshot and renews its lease.
func (r *evaluationTaskRepository) PublishProgress(
	ctx context.Context,
	command types.EvaluationTaskProgressCommand,
) (*types.EvaluationTaskEntity, error) {
	if err := validateEvaluationTaskMutationIdentity(
		command.TenantID,
		command.TaskID,
		command.OwnerID,
		command.ExpectedVersion,
	); err != nil {
		return nil, fmt.Errorf("publish evaluation progress: %w", err)
	}
	if command.Now.IsZero() || command.LeaseExpiresAt.IsZero() {
		return nil, errors.New("publish evaluation progress: now and lease_expires_at are required")
	}
	command.Now = command.Now.UTC()
	command.LeaseExpiresAt = command.LeaseExpiresAt.UTC()
	if !command.LeaseExpiresAt.After(command.Now) {
		return nil, errors.New("publish evaluation progress: lease_expires_at must be after now")
	}
	if command.Total < 0 || command.Finished < 0 || command.Finished > command.Total {
		return nil, errors.New("publish evaluation progress: expected 0 <= finished <= total")
	}
	if err := validateEvaluationTaskJSONObject(command.Metric, true); err != nil {
		return nil, fmt.Errorf("publish evaluation progress: metric: %w", err)
	}
	if err := validateEvaluationTaskJSONObject(command.RuntimeMetrics, true); err != nil {
		return nil, fmt.Errorf("publish evaluation progress: runtime_metrics: %w", err)
	}

	return r.updateEvaluationTask(
		ctx,
		command.TenantID,
		command.TaskID,
		command.OwnerID,
		command.ExpectedVersion,
		[]types.EvaluationStatue{types.EvaluationStatueRunning},
		"(total = 0 OR total = ?) AND finished <= ? AND lease_expires_at > ?",
		[]any{command.Total, command.Finished, command.Now},
		map[string]any{
			"total":           command.Total,
			"finished":        command.Finished,
			"metric":          command.Metric,
			"runtime_metrics": command.RuntimeMetrics,
			"heartbeat_at": gorm.Expr(
				"CASE WHEN heartbeat_at > ? THEN heartbeat_at ELSE ? END",
				command.Now, command.Now,
			),
			"lease_expires_at": gorm.Expr(
				"CASE WHEN lease_expires_at > ? THEN lease_expires_at ELSE ? END",
				command.LeaseExpiresAt, command.LeaseExpiresAt,
			),
			"updated_at": gorm.Expr(
				"CASE WHEN updated_at > ? THEN updated_at ELSE ? END",
				command.Now, command.Now,
			),
			"version": gorm.Expr("version + 1"),
		},
		func(task *types.EvaluationTaskEntity) bool {
			return task.Status == types.EvaluationStatueRunning &&
				task.LeaseExpiresAt != nil && task.LeaseExpiresAt.After(command.Now) &&
				(task.Total == 0 || task.Total == command.Total) &&
				task.Finished <= command.Finished
		},
		"publish progress for",
	)
}

// RecordTemporaryKnowledge records resource ownership once for a running task.
func (r *evaluationTaskRepository) RecordTemporaryKnowledge(
	ctx context.Context,
	command types.EvaluationTaskKnowledgeCommand,
) (*types.EvaluationTaskEntity, error) {
	if err := validateEvaluationTaskMutationIdentity(
		command.TenantID,
		command.TaskID,
		command.OwnerID,
		command.ExpectedVersion,
	); err != nil {
		return nil, fmt.Errorf("record evaluation temporary knowledge: %w", err)
	}
	if command.TemporaryKnowledgeID == "" || command.UpdatedAt.IsZero() {
		return nil, errors.New("record evaluation temporary knowledge: knowledge ID and updated_at are required")
	}
	command.UpdatedAt = command.UpdatedAt.UTC()

	return r.updateEvaluationTask(
		ctx,
		command.TenantID,
		command.TaskID,
		command.OwnerID,
		command.ExpectedVersion,
		[]types.EvaluationStatue{types.EvaluationStatueRunning},
		"(temporary_knowledge_id IS NULL OR temporary_knowledge_id = '') AND lease_expires_at > ?",
		[]any{command.UpdatedAt},
		map[string]any{
			"temporary_knowledge_id": command.TemporaryKnowledgeID,
			"updated_at": gorm.Expr(
				"CASE WHEN updated_at > ? THEN updated_at ELSE ? END",
				command.UpdatedAt, command.UpdatedAt,
			),
			"version": gorm.Expr("version + 1"),
		},
		func(task *types.EvaluationTaskEntity) bool {
			return task.Status == types.EvaluationStatueRunning &&
				task.LeaseExpiresAt != nil && task.LeaseExpiresAt.After(command.UpdatedAt) &&
				task.TemporaryKnowledgeID == ""
		},
		"record temporary knowledge for",
	)
}

// PublishTerminal atomically stores a stable terminal snapshot and releases the lease.
func (r *evaluationTaskRepository) PublishTerminal(
	ctx context.Context,
	command types.EvaluationTaskTerminalCommand,
) (*types.EvaluationTaskEntity, error) {
	if err := validateEvaluationTaskMutationIdentity(
		command.TenantID,
		command.TaskID,
		command.OwnerID,
		command.ExpectedVersion,
	); err != nil {
		return nil, fmt.Errorf("publish evaluation terminal state: %w", err)
	}
	if command.EndTime.IsZero() {
		return nil, errors.New("publish evaluation terminal state: end_time is required")
	}
	command.EndTime = command.EndTime.UTC()
	switch command.Status {
	case types.EvaluationStatueSuccess:
		if command.ErrMsg != "" {
			return nil, errors.New("publish evaluation terminal state: successful task must not have err_msg")
		}
	case types.EvaluationStatueFailed, types.EvaluationStatueTimedOut,
		types.EvaluationStatueInterrupted, types.EvaluationStatueCanceled:
		if command.ErrMsg == "" {
			return nil, errors.New(
				"publish evaluation terminal state: failed, timed out, interrupted, or canceled task requires err_msg",
			)
		}
	default:
		return nil, errors.New("publish evaluation terminal state: unsupported terminal status")
	}
	if len(command.CleanupErrors) == 0 {
		command.CleanupErrors = types.JSON(`[]`)
	}
	if err := validateEvaluationTaskStringArray(command.CleanupErrors); err != nil {
		return nil, fmt.Errorf("publish evaluation terminal state: cleanup_errors: %w", err)
	}
	if err := validateEvaluationTaskJSONObject(command.Metric, true); err != nil {
		return nil, fmt.Errorf("publish evaluation terminal state: metric: %w", err)
	}
	if err := validateEvaluationTaskJSONObject(command.RuntimeMetrics, true); err != nil {
		return nil, fmt.Errorf("publish evaluation terminal state: runtime_metrics: %w", err)
	}

	// Terminal compare-and-swap truth: Canceled requires a persistent cancel
	// request; every other terminal state requires its absence.
	cancelCondition := "cancel_requested_at IS NULL"
	if command.Status == types.EvaluationStatueCanceled {
		cancelCondition = "cancel_requested_at IS NOT NULL"
	}
	stateCondition := "start_time <= ? AND heartbeat_at <= ? AND lease_expires_at > ? AND " + cancelCondition
	if command.Status == types.EvaluationStatueSuccess {
		// The terminal compare-and-swap checks the persisted row set once in the
		// same statement. Failure and cancellation retain their partial rows.
		stateCondition += ` AND finished = total AND (
			SELECT COUNT(*) FROM evaluation_question_results AS question_results
			WHERE question_results.tenant_id = evaluation_tasks.tenant_id
				AND question_results.task_id = evaluation_tasks.id
				AND question_results.deleted_at IS NULL
		) = total`
	}

	return r.updateEvaluationTask(
		ctx,
		command.TenantID,
		command.TaskID,
		command.OwnerID,
		command.ExpectedVersion,
		[]types.EvaluationStatue{types.EvaluationStatuePending, types.EvaluationStatueRunning},
		stateCondition,
		[]any{command.EndTime, command.EndTime, command.EndTime},
		map[string]any{
			"status":           command.Status,
			"end_time":         command.EndTime,
			"err_msg":          command.ErrMsg,
			"cleanup_errors":   command.CleanupErrors,
			"metric":           command.Metric,
			"runtime_metrics":  command.RuntimeMetrics,
			"lease_expires_at": nil,
			"updated_at": gorm.Expr(
				"CASE WHEN updated_at > ? THEN updated_at ELSE ? END",
				command.EndTime, command.EndTime,
			),
			"version": gorm.Expr("version + 1"),
		},
		func(task *types.EvaluationTaskEntity) bool {
			if task.Status != types.EvaluationStatuePending && task.Status != types.EvaluationStatueRunning {
				return false
			}
			if task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(command.EndTime) ||
				task.StartTime.After(command.EndTime) || task.HeartbeatAt.After(command.EndTime) {
				return false
			}
			if command.Status == types.EvaluationStatueCanceled {
				return task.CancelRequestedAt != nil
			}
			return task.CancelRequestedAt == nil &&
				(command.Status != types.EvaluationStatueSuccess || task.Finished == task.Total)
		},
		"publish terminal state for",
	)
}

// ListTasks returns up to query.Limit tenant tasks ordered by
// (start_time DESC, id DESC) after the exclusive keyset boundary. Soft-deleted
// rows are hidden by the entity's DeletedAt scope.
func (r *evaluationTaskRepository) ListTasks(
	ctx context.Context,
	tenantID uint64,
	query types.EvaluationTaskListQuery,
) ([]*types.EvaluationTaskEntity, error) {
	if tenantID == 0 {
		return nil, errors.New("list evaluation tasks: tenant_id is required")
	}
	if query.Limit <= 0 {
		return nil, errors.New("list evaluation tasks: limit must be positive")
	}
	if query.Status != nil && !isKnownEvaluationTaskStatus(*query.Status) {
		return nil, errors.New("list evaluation tasks: unsupported status filter")
	}
	if query.StartedFrom != nil && query.StartedTo != nil && query.StartedFrom.After(*query.StartedTo) {
		return nil, errors.New("list evaluation tasks: started_from must not be after started_to")
	}
	if (query.StartBefore == nil) != (query.IDBefore == "") {
		return nil, errors.New("list evaluation tasks: keyset boundary requires both start_time and id")
	}

	dbQuery := r.db.WithContext(ctx).
		Model(&types.EvaluationTaskEntity{}).
		Select(
			"id",
			"tenant_id",
			"dataset_id",
			"status",
			"start_time",
			"end_time",
			"total",
			"finished",
			"err_msg",
			"cleanup_errors",
			"cancel_requested_at",
			"dataset_version_id",
			"dataset_content_sha256",
			"experiment_snapshot",
			"experiment_sha256",
		).
		Where("tenant_id = ?", tenantID)
	if query.Status != nil {
		dbQuery = dbQuery.Where("status = ?", *query.Status)
	}
	if query.DatasetID != "" {
		dbQuery = dbQuery.Where("dataset_id = ?", query.DatasetID)
	}
	if query.DatasetVersionID != "" {
		dbQuery = dbQuery.Where("dataset_version_id = ?", query.DatasetVersionID)
	}
	if query.ModelID != "" {
		if r.db.Name() == "postgres" {
			dbQuery = dbQuery.Where(`(
				experiment_snapshot #>> '{models,embedding,id}' = ? OR
				experiment_snapshot #>> '{models,chat,id}' = ? OR
				experiment_snapshot #>> '{models,rerank,id}' = ? OR
				experiment_snapshot #>> '{models,summary,id}' = ?
			)`, query.ModelID, query.ModelID, query.ModelID, query.ModelID)
		} else {
			dbQuery = dbQuery.Where(`(
				json_extract(experiment_snapshot, '$.models.embedding.id') = ? OR
				json_extract(experiment_snapshot, '$.models.chat.id') = ? OR
				json_extract(experiment_snapshot, '$.models.rerank.id') = ? OR
				json_extract(experiment_snapshot, '$.models.summary.id') = ?
			)`, query.ModelID, query.ModelID, query.ModelID, query.ModelID)
		}
	}
	if query.StartedFrom != nil {
		dbQuery = dbQuery.Where("start_time >= ?", query.StartedFrom.UTC())
	}
	if query.StartedTo != nil {
		dbQuery = dbQuery.Where("start_time <= ?", query.StartedTo.UTC())
	}
	for _, label := range query.Labels {
		dbQuery = dbQuery.Where(`EXISTS (
			SELECT 1 FROM evaluation_task_labels
			WHERE evaluation_task_labels.tenant_id = evaluation_tasks.tenant_id
			AND evaluation_task_labels.task_id = evaluation_tasks.id
			AND evaluation_task_labels.label = ?
		)`, label)
	}
	if query.StartBefore != nil {
		startBefore := query.StartBefore.UTC()
		dbQuery = dbQuery.Where(
			"(start_time < ? OR (start_time = ? AND id < ?))",
			startBefore, startBefore, query.IDBefore,
		)
	}

	var tasks []*types.EvaluationTaskEntity
	if err := dbQuery.
		Order("start_time DESC").
		Order("id DESC").
		Limit(query.Limit).
		Find(&tasks).Error; err != nil {
		return nil, fmt.Errorf("list evaluation tasks: %w", err)
	}
	for _, task := range tasks {
		normalizeEvaluationTaskTimes(task)
	}
	return tasks, nil
}

// ListTaskLabels batch-loads labels after task pagination, preserving stable
// task keysets independently of label cardinality.
func (r *evaluationTaskRepository) ListTaskLabels(
	ctx context.Context,
	tenantID uint64,
	taskIDs []string,
) (map[string][]string, error) {
	labelsByTask := make(map[string][]string, len(taskIDs))
	if tenantID == 0 || len(taskIDs) == 0 {
		return labelsByTask, nil
	}
	var rows []types.EvaluationTaskLabelEntity
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND task_id IN ?", tenantID, taskIDs).
		Order("task_id ASC").
		Order("label ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list evaluation task labels: %w", err)
	}
	for _, row := range rows {
		labelsByTask[row.TaskID] = append(labelsByTask[row.TaskID], row.Label)
	}
	return labelsByTask, nil
}

// ReplaceTaskLabels atomically replaces the complete label set without
// updating the task row, its business version, or its updated_at value.
func (r *evaluationTaskRepository) ReplaceTaskLabels(
	ctx context.Context,
	tenantID uint64,
	taskID string,
	labels []string,
	now time.Time,
) error {
	if tenantID == 0 || taskID == "" || now.IsZero() {
		return errors.New("replace evaluation task labels: tenant_id, task_id, and now are required")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&types.EvaluationTaskEntity{}).
			Where("tenant_id = ? AND id = ?", tenantID, taskID).
			Count(&count).Error; err != nil {
			return fmt.Errorf("replace evaluation task labels %s: load task: %w", taskID, err)
		}
		if count != 1 {
			return fmt.Errorf("replace evaluation task labels %s: %w", taskID, ErrEvaluationTaskNotFound)
		}
		if err := tx.Where("tenant_id = ? AND task_id = ?", tenantID, taskID).
			Delete(&types.EvaluationTaskLabelEntity{}).Error; err != nil {
			return fmt.Errorf("replace evaluation task labels %s: delete current labels: %w", taskID, err)
		}
		if len(labels) == 0 {
			return nil
		}
		rows := make([]types.EvaluationTaskLabelEntity, 0, len(labels))
		for _, label := range labels {
			rows = append(rows, types.EvaluationTaskLabelEntity{
				TenantID: tenantID, TaskID: taskID, Label: label, CreatedAt: now.UTC(),
			})
		}
		if err := tx.Create(&rows).Error; err != nil {
			return fmt.Errorf("replace evaluation task labels %s: create labels: %w", taskID, err)
		}
		return nil
	})
}

func isKnownEvaluationTaskStatus(status types.EvaluationStatue) bool {
	return status >= types.EvaluationStatuePending && status <= types.EvaluationStatueCanceled
}

// DeleteExpiredTerminalTasks physically removes up to limit terminal tasks
// whose end_time is older than cutoff, including already soft-deleted rows.
// Pending and Running tasks are never touched. Deletion happens oldest first
// so repeated bounded batches converge. Child tables owned by later
// milestones must cascade through foreign keys or explicit same-transaction
// deletion; M2 has no evaluation child tables.
func (r *evaluationTaskRepository) DeleteExpiredTerminalTasks(
	ctx context.Context,
	cutoff time.Time,
	limit int,
) (int64, error) {
	if cutoff.IsZero() {
		return 0, errors.New("delete expired evaluation tasks: cutoff is required")
	}
	if limit <= 0 {
		return 0, errors.New("delete expired evaluation tasks: limit must be positive")
	}
	cutoff = cutoff.UTC()

	terminalStatuses := []types.EvaluationStatue{
		types.EvaluationStatueSuccess,
		types.EvaluationStatueFailed,
		types.EvaluationStatueTimedOut,
		types.EvaluationStatueInterrupted,
		types.EvaluationStatueCanceled,
	}
	batch := r.db.WithContext(ctx).
		Model(&types.EvaluationTaskEntity{}).
		Unscoped().
		Select("id").
		Where("status IN ? AND end_time IS NOT NULL AND end_time < ?", terminalStatuses, cutoff).
		Order("end_time ASC").
		Order("id ASC").
		Limit(limit)

	result := r.db.WithContext(ctx).
		Unscoped().
		Where("id IN (?)", batch).
		Delete(&types.EvaluationTaskEntity{})
	if result.Error != nil {
		return 0, fmt.Errorf("delete expired evaluation tasks: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// DeleteTask soft-deletes one terminal task. Missing, cross-tenant, and
// already deleted tasks are idempotent successes; active tasks are rejected
// with a state conflict.
func (r *evaluationTaskRepository) DeleteTask(
	ctx context.Context,
	tenantID uint64,
	taskID string,
	now time.Time,
) error {
	if tenantID == 0 || taskID == "" {
		return errors.New("delete evaluation task: tenant_id and task_id are required")
	}
	if now.IsZero() {
		return errors.New("delete evaluation task: now is required")
	}
	now = now.UTC()

	terminalStatuses := []types.EvaluationStatue{
		types.EvaluationStatueSuccess,
		types.EvaluationStatueFailed,
		types.EvaluationStatueTimedOut,
		types.EvaluationStatueInterrupted,
		types.EvaluationStatueCanceled,
	}
	result := r.db.WithContext(ctx).
		Model(&types.EvaluationTaskEntity{}).
		Where("tenant_id = ? AND id = ? AND status IN ?", tenantID, taskID, terminalStatuses).
		Update("deleted_at", now)
	if result.Error != nil {
		return fmt.Errorf("delete evaluation task %s: %w", taskID, result.Error)
	}
	if result.RowsAffected == 1 {
		return nil
	}
	if result.RowsAffected > 1 {
		return fmt.Errorf(
			"delete evaluation task %s: invariant violation: updated %d rows",
			taskID,
			result.RowsAffected,
		)
	}

	// Distinguish an active task from a missing or already deleted one; the
	// latter two stay hidden and idempotent.
	var task types.EvaluationTaskEntity
	err := r.db.WithContext(ctx).
		Unscoped().
		Where("tenant_id = ? AND id = ?", tenantID, taskID).
		First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete evaluation task %s: %w", taskID, err)
	}
	if task.DeletedAt.Valid {
		return nil
	}
	return fmt.Errorf("delete evaluation task %s: %w", taskID, ErrEvaluationTaskStateConflict)
}

// RequestCancel records the first persistent cancel request for one active
// task. The first request time wins, the execution version is unchanged, and
// terminal tasks are returned unchanged.
func (r *evaluationTaskRepository) RequestCancel(
	ctx context.Context,
	command types.EvaluationTaskCancelCommand,
) (*types.EvaluationTaskEntity, error) {
	if command.TenantID == 0 || command.TaskID == "" {
		return nil, errors.New("request evaluation task cancel: tenant_id and task_id are required")
	}
	if command.Now.IsZero() {
		return nil, errors.New("request evaluation task cancel: now is required")
	}
	command.Now = command.Now.UTC()

	activeStatuses := []types.EvaluationStatue{
		types.EvaluationStatuePending,
		types.EvaluationStatueRunning,
	}
	result := r.db.WithContext(ctx).
		Model(&types.EvaluationTaskEntity{}).
		Where("tenant_id = ? AND id = ? AND status IN ? AND cancel_requested_at IS NULL",
			command.TenantID, command.TaskID, activeStatuses).
		Updates(map[string]any{
			"cancel_requested_at": command.Now,
			"updated_at": gorm.Expr(
				"CASE WHEN updated_at > ? THEN updated_at ELSE ? END",
				command.Now, command.Now,
			),
		})
	if result.Error != nil {
		return nil, fmt.Errorf("request cancel for evaluation task %s: %w", command.TaskID, result.Error)
	}
	if result.RowsAffected > 1 {
		return nil, fmt.Errorf(
			"request cancel for evaluation task %s: invariant violation: updated %d rows",
			command.TaskID,
			result.RowsAffected,
		)
	}
	task, err := r.GetTask(ctx, command.TenantID, command.TaskID)
	if err != nil {
		return nil, fmt.Errorf("request cancel for evaluation task %s: %w", command.TaskID, err)
	}
	return task, nil
}

func (r *evaluationTaskRepository) classifyEvaluationTaskHeartbeatConflict(
	ctx context.Context,
	command types.EvaluationTaskHeartbeatCommand,
) error {
	task, err := r.GetTask(ctx, command.TenantID, command.TaskID)
	if err != nil {
		return fmt.Errorf("heartbeat evaluation task %s: %w", command.TaskID, err)
	}
	if task.OwnerID != command.OwnerID {
		return fmt.Errorf("heartbeat evaluation task %s: %w", command.TaskID, ErrEvaluationTaskOwnerConflict)
	}
	if task.Status != types.EvaluationStatueRunning ||
		task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(command.Now) {
		return fmt.Errorf("heartbeat evaluation task %s: %w", command.TaskID, ErrEvaluationTaskStateConflict)
	}
	return fmt.Errorf(
		"heartbeat evaluation task %s: concurrent state changed: %w",
		command.TaskID,
		ErrEvaluationTaskStateConflict,
	)
}

func (r *evaluationTaskRepository) updateEvaluationTask(
	ctx context.Context,
	tenantID uint64,
	taskID string,
	ownerID string,
	expectedVersion uint64,
	allowedStatuses []types.EvaluationStatue,
	extraCondition string,
	extraArgs []any,
	updates map[string]any,
	stateMatches func(*types.EvaluationTaskEntity) bool,
	action string,
) (*types.EvaluationTaskEntity, error) {
	var updated types.EvaluationTaskEntity
	query := r.db.WithContext(ctx).
		Model(&updated).
		Clauses(clause.Returning{}).
		Where("tenant_id = ? AND id = ? AND owner_id = ? AND version = ?", tenantID, taskID, ownerID, expectedVersion).
		Where("status IN ?", allowedStatuses)
	if extraCondition != "" {
		query = query.Where(extraCondition, extraArgs...)
	}
	result := query.Updates(updates)
	if result.Error != nil {
		return nil, fmt.Errorf("%s evaluation task %s: %w", action, taskID, result.Error)
	}
	if result.RowsAffected > 1 {
		return nil, fmt.Errorf(
			"%s evaluation task %s: invariant violation: updated %d rows",
			action,
			taskID,
			result.RowsAffected,
		)
	}
	if result.RowsAffected == 0 {
		return nil, r.classifyEvaluationTaskMutationConflict(
			ctx,
			tenantID,
			taskID,
			ownerID,
			expectedVersion,
			stateMatches,
			action,
		)
	}
	normalizeEvaluationTaskTimes(&updated)
	return &updated, nil
}

func (r *evaluationTaskRepository) classifyEvaluationTaskMutationConflict(
	ctx context.Context,
	tenantID uint64,
	taskID string,
	ownerID string,
	expectedVersion uint64,
	stateMatches func(*types.EvaluationTaskEntity) bool,
	action string,
) error {
	task, err := r.GetTask(ctx, tenantID, taskID)
	if err != nil {
		return fmt.Errorf("%s evaluation task %s: %w", action, taskID, err)
	}
	if task.OwnerID != ownerID {
		return fmt.Errorf("%s evaluation task %s: %w", action, taskID, ErrEvaluationTaskOwnerConflict)
	}
	if stateMatches != nil && !stateMatches(task) {
		return fmt.Errorf("%s evaluation task %s: %w", action, taskID, ErrEvaluationTaskStateConflict)
	}
	if task.Version != expectedVersion {
		return fmt.Errorf("%s evaluation task %s: expected version %d, current version %d: %w",
			action, taskID, expectedVersion, task.Version, ErrEvaluationTaskVersionConflict)
	}
	return fmt.Errorf(
		"%s evaluation task %s: concurrent state changed: %w",
		action,
		taskID,
		ErrEvaluationTaskStateConflict,
	)
}

func validateEvaluationTaskMutationIdentity(tenantID uint64, taskID, ownerID string, expectedVersion uint64) error {
	if tenantID == 0 || taskID == "" || ownerID == "" || expectedVersion == 0 {
		return errors.New("tenant_id, task_id, owner_id, and expected_version are required")
	}
	return nil
}

func validateEvaluationTaskJSONObject(value types.JSON, allowEmpty bool) error {
	if len(value) == 0 {
		if allowEmpty {
			return nil
		}
		return errors.New("JSON object is required")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(value, &object); err != nil || object == nil {
		return errors.New("must be a valid JSON object")
	}
	return nil
}

func validateEvaluationTaskStringArray(value types.JSON) error {
	var values []string
	if err := json.Unmarshal(value, &values); err != nil || values == nil {
		return errors.New("must be a valid JSON string array")
	}
	return nil
}

func normalizeEvaluationTaskTimes(task *types.EvaluationTaskEntity) {
	task.StartTime = task.StartTime.UTC()
	if task.EndTime != nil {
		endTime := task.EndTime.UTC()
		task.EndTime = &endTime
	}
	if task.LeaseExpiresAt != nil {
		leaseExpiresAt := task.LeaseExpiresAt.UTC()
		task.LeaseExpiresAt = &leaseExpiresAt
	}
	if task.CancelRequestedAt != nil {
		cancelRequestedAt := task.CancelRequestedAt.UTC()
		task.CancelRequestedAt = &cancelRequestedAt
	}
	task.HeartbeatAt = task.HeartbeatAt.UTC()
	task.CreatedAt = task.CreatedAt.UTC()
	task.UpdatedAt = task.UpdatedAt.UTC()
}

func validateInitialEvaluationTask(task *types.EvaluationTaskEntity) error {
	if task.Status != types.EvaluationStatuePending {
		return errors.New("create evaluation task: status must be pending")
	}
	if task.EndTime != nil || task.Total != 0 || task.Finished != 0 || task.ErrMsg != "" {
		return errors.New("create evaluation task: terminal and progress fields must be empty")
	}
	if len(task.Metric) != 0 || task.TemporaryKnowledgeID != "" {
		return errors.New("create evaluation task: result and temporary knowledge fields must be empty")
	}
	if len(task.RuntimeMetrics) != 0 {
		return errors.New("create evaluation task: runtime_metrics must be empty")
	}
	if task.Version > 1 || task.DeletedAt.Valid {
		return errors.New("create evaluation task: version and deletion fields must describe a new task")
	}
	if task.CancelRequestedAt != nil {
		return errors.New("create evaluation task: cancel_requested_at must be empty")
	}
	if len(task.CleanupErrors) != 0 {
		var cleanupErrors []string
		unmarshalErr := json.Unmarshal(task.CleanupErrors, &cleanupErrors)
		if unmarshalErr != nil || cleanupErrors == nil || len(cleanupErrors) != 0 {
			return errors.New("create evaluation task: cleanup_errors must be an empty JSON array")
		}
	}
	if len(task.Params) != 0 {
		var params map[string]json.RawMessage
		if err := json.Unmarshal(task.Params, &params); err != nil || params == nil {
			return errors.New("create evaluation task: params must be a JSON object")
		}
	}
	return nil
}
