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

type taskJobRepository struct {
	db *gorm.DB
}

func NewTaskJobRepository(db *gorm.DB) interfaces.TaskJobRepository {
	return &taskJobRepository{db: db}
}

var taskLedgerTerminalStates = []string{
	string(types.TaskJobStateSucceeded),
	string(types.TaskJobStateFailed),
	string(types.TaskJobStateCanceled),
}

var taskExecutionTerminalStates = []string{
	string(types.TaskExecutionStateSucceeded),
	string(types.TaskExecutionStateFailed),
	string(types.TaskExecutionStateCanceled),
}

func (r *taskJobRepository) CreateJobAndExecution(
	ctx context.Context,
	job *types.TaskJob,
	execution *types.TaskExecution,
) error {
	if job == nil || execution == nil {
		return errors.New("task ledger: job and execution are required")
	}
	if job.JobID == "" || execution.ExecutionID == "" {
		return errors.New("task ledger: job_id and execution_id are required")
	}
	if execution.JobID == "" {
		execution.JobID = job.JobID
	}
	if execution.JobID != job.JobID {
		return fmt.Errorf("task ledger: execution job_id %q does not match job_id %q", execution.JobID, job.JobID)
	}
	if job.TenantID == 0 || job.Kind == "" || job.Scope == "" || job.ScopeID == "" {
		return errors.New("task ledger: tenant_id, kind, scope, and scope_id are required")
	}
	if execution.TaskType == "" {
		return errors.New("task ledger: execution task_type is required")
	}
	if job.Origin == "" {
		job.Origin = types.TaskJobOriginUser
	}
	if job.State == "" {
		job.State = types.TaskJobStateQueued
	}
	if len(job.Metadata) == 0 {
		job.Metadata = types.JSON(`{}`)
	}
	if len(job.ReplaySpec) == 0 {
		job.ReplaySpec = types.JSON(`{}`)
	}
	if execution.State == "" {
		execution.State = types.TaskExecutionStateQueued
	}
	if execution.EnqueuedAt.IsZero() {
		execution.EnqueuedAt = time.Now()
	}
	execution.ProcessAttempt = job.ProcessAttempt

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(job).Error; err != nil {
			return err
		}
		return tx.Create(execution).Error
	})
}

func (r *taskJobRepository) MarkJobProcessingIfCurrentAttempt(ctx context.Context, sel interfaces.TaskJobAttemptSelector) (bool, error) {
	res := r.baseJobAttemptUpdate(ctx, sel).
		Where("state = ?", types.TaskJobStateQueued).
		Updates(map[string]any{"state": types.TaskJobStateProcessing})
	return rowsChanged(res)
}

func (r *taskJobRepository) MarkJobFinalizingIfCurrentAttempt(ctx context.Context, sel interfaces.TaskJobAttemptSelector) (bool, error) {
	res := r.baseJobAttemptUpdate(ctx, sel).
		Where("state = ?", types.TaskJobStateProcessing).
		Updates(map[string]any{"state": types.TaskJobStateFinalizing})
	return rowsChanged(res)
}

func (r *taskJobRepository) MarkJobSucceededIfCurrentAttempt(ctx context.Context, sel interfaces.TaskJobAttemptSelector, finishedAt time.Time) (bool, error) {
	res := r.baseJobAttemptUpdate(ctx, sel).
		Where("state NOT IN ?", taskLedgerTerminalStates).
		Updates(map[string]any{
			"state":            types.TaskJobStateSucceeded,
			"last_error_class": "",
			"last_error":       "",
			"failed_task_type": "",
			"failed_task_id":   "",
			"finished_at":      finishedAt,
		})
	return rowsChanged(res)
}

func (r *taskJobRepository) MarkJobFailedIfCurrentAttempt(
	ctx context.Context,
	sel interfaces.TaskJobAttemptSelector,
	failure interfaces.TaskLedgerFailure,
	finishedAt time.Time,
) (bool, error) {
	return r.markJobTerminal(ctx, sel, types.TaskJobStateFailed, failure, finishedAt)
}

func (r *taskJobRepository) MarkJobCanceledIfCurrentAttempt(
	ctx context.Context,
	sel interfaces.TaskJobAttemptSelector,
	failure interfaces.TaskLedgerFailure,
	finishedAt time.Time,
) (bool, error) {
	if failure.ErrorClass == "" {
		failure.ErrorClass = types.TaskErrorClassCanceled
	}
	return r.markJobTerminal(ctx, sel, types.TaskJobStateCanceled, failure, finishedAt)
}

func (r *taskJobRepository) MarkDispatched(ctx context.Context, executionID string, dispatchedAt time.Time) (bool, error) {
	res := r.db.WithContext(ctx).Model(&types.TaskExecution{}).
		Where("execution_id = ?", executionID).
		Where("state NOT IN ?", taskExecutionTerminalStates).
		Update("dispatched_at", dispatchedAt)
	return rowsChanged(res)
}

func (r *taskJobRepository) MarkDispatchFailed(
	ctx context.Context,
	jobID, executionID string,
	failure interfaces.TaskLedgerFailure,
	finishedAt time.Time,
) (bool, error) {
	if failure.ErrorClass == "" {
		failure.ErrorClass = types.TaskErrorClassEnqueueFailed
	}
	changed := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&types.TaskExecution{}).
			Where("execution_id = ?", executionID).
			Where("state NOT IN ?", taskExecutionTerminalStates).
			Updates(execFailureUpdates(types.TaskExecutionStateFailed, failure, finishedAt))
		ok, err := rowsChanged(res)
		if err != nil {
			return err
		}
		changed = changed || ok

		res = tx.Model(&types.TaskJob{}).
			Where("job_id = ?", jobID).
			Where("state NOT IN ?", taskLedgerTerminalStates).
			Updates(jobFailureUpdates(types.TaskJobStateFailed, failure, finishedAt))
		ok, err = rowsChanged(res)
		if err != nil {
			return err
		}
		changed = changed || ok
		return nil
	})
	return changed, err
}

func (r *taskJobRepository) MarkExecActiveIfExists(
	ctx context.Context,
	executionID string,
	retryCount int,
	startedAt time.Time,
) (bool, error) {
	res := r.db.WithContext(ctx).Model(&types.TaskExecution{}).
		Where("execution_id = ?", executionID).
		Where("state NOT IN ?", taskExecutionTerminalStates).
		Updates(map[string]any{
			"state":         types.TaskExecutionStateActive,
			"retry_count":   retryCount,
			"dispatched_at": gorm.Expr("COALESCE(dispatched_at, ?)", startedAt),
			"started_at":    startedAt,
		})
	return rowsChanged(res)
}

func (r *taskJobRepository) MarkExecRetryingIfNonTerminal(
	ctx context.Context,
	executionID string,
	retryCount int,
	failure interfaces.TaskLedgerFailure,
) (bool, error) {
	res := r.db.WithContext(ctx).Model(&types.TaskExecution{}).
		Where("execution_id = ?", executionID).
		Where("state NOT IN ?", taskExecutionTerminalStates).
		Updates(map[string]any{
			"state":       types.TaskExecutionStateRetrying,
			"retry_count": retryCount,
			"error_class": failure.ErrorClass,
			"last_error":  failure.LastError,
		})
	return rowsChanged(res)
}

func (r *taskJobRepository) MarkExecSucceededIfNonTerminal(ctx context.Context, executionID string, finishedAt time.Time) (bool, error) {
	res := r.db.WithContext(ctx).Model(&types.TaskExecution{}).
		Where("execution_id = ?", executionID).
		Where("state NOT IN ?", taskExecutionTerminalStates).
		Updates(map[string]any{
			"state":       types.TaskExecutionStateSucceeded,
			"error_class": "",
			"last_error":  "",
			"finished_at": finishedAt,
		})
	return rowsChanged(res)
}

func (r *taskJobRepository) MarkExecFailedIfNonTerminal(
	ctx context.Context,
	executionID string,
	failure interfaces.TaskLedgerFailure,
	finishedAt time.Time,
) (bool, error) {
	res := r.db.WithContext(ctx).Model(&types.TaskExecution{}).
		Where("execution_id = ?", executionID).
		Where("state NOT IN ?", taskExecutionTerminalStates).
		Updates(execFailureUpdates(types.TaskExecutionStateFailed, failure, finishedAt))
	return rowsChanged(res)
}

func (r *taskJobRepository) MarkExecCanceledIfNonTerminal(
	ctx context.Context,
	executionID string,
	failure interfaces.TaskLedgerFailure,
	finishedAt time.Time,
) (bool, error) {
	if failure.ErrorClass == "" {
		failure.ErrorClass = types.TaskErrorClassCanceled
	}
	res := r.db.WithContext(ctx).Model(&types.TaskExecution{}).
		Where("execution_id = ?", executionID).
		Where("state NOT IN ?", taskExecutionTerminalStates).
		Updates(execFailureUpdates(types.TaskExecutionStateCanceled, failure, finishedAt))
	return rowsChanged(res)
}

func (r *taskJobRepository) PrepareManualRetry(
	ctx context.Context,
	jobID, newExecutionID, retryTaskType, queue string,
	enqueuedAt time.Time,
) (*types.TaskExecution, bool, error) {
	if jobID == "" || newExecutionID == "" || retryTaskType == "" {
		return nil, false, errors.New("task ledger: job_id, new execution_id, and task_type are required")
	}
	var created *types.TaskExecution
	changed := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job types.TaskJob
		q := tx
		if tx.Dialector.Name() != "sqlite" {
			q = q.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := q.Where("job_id = ?", jobID).First(&job).Error; err != nil {
			return err
		}
		if job.State != types.TaskJobStateFailed && job.State != types.TaskJobStateCanceled {
			return nil
		}

		var previous types.TaskExecution
		err := tx.Where("job_id = ? AND process_attempt = ?", jobID, job.ProcessAttempt).
			Order("enqueued_at DESC").
			First(&previous).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		nextAttempt := job.ProcessAttempt + 1
		res := tx.Model(&types.TaskJob{}).
			Where("job_id = ?", jobID).
			Where("state IN ?", []string{string(types.TaskJobStateFailed), string(types.TaskJobStateCanceled)}).
			Updates(map[string]any{
				"process_attempt":  nextAttempt,
				"state":            types.TaskJobStateQueued,
				"last_error_class": "",
				"last_error":       "",
				"failed_task_type": "",
				"failed_task_id":   "",
				"finished_at":      nil,
			})
		ok, err := rowsChanged(res)
		if err != nil || !ok {
			return err
		}

		created = &types.TaskExecution{
			ExecutionID:    newExecutionID,
			JobID:          jobID,
			ProcessAttempt: nextAttempt,
			TaskType:       retryTaskType,
			Queue:          queue,
			State:          types.TaskExecutionStateQueued,
			RetryOf:        previous.ExecutionID,
			EnqueuedAt:     enqueuedAt,
		}
		if created.EnqueuedAt.IsZero() {
			created.EnqueuedAt = time.Now()
		}
		if err := tx.Create(created).Error; err != nil {
			return err
		}
		changed = true
		return nil
	})
	return created, changed, err
}

func (r *taskJobRepository) FindStaleDispatches(ctx context.Context, cutoff time.Time, limit int) ([]*types.TaskExecution, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	var rows []*types.TaskExecution
	err := r.db.WithContext(ctx).
		Where("state = ?", types.TaskExecutionStateQueued).
		Where("dispatched_at IS NULL").
		Where("enqueued_at < ?", cutoff).
		Order("enqueued_at ASC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

func (r *taskJobRepository) MarkStaleDispatchFailed(ctx context.Context, executionID string, finishedAt time.Time) (bool, error) {
	var exec types.TaskExecution
	if err := r.db.WithContext(ctx).
		Select("execution_id", "job_id").
		Where("execution_id = ?", executionID).
		First(&exec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return r.MarkDispatchFailed(ctx, exec.JobID, executionID, interfaces.TaskLedgerFailure{
		ErrorClass: types.TaskErrorClassEnqueueFailed,
		LastError:  "dispatch status unknown / process interrupted before enqueue",
	}, finishedAt)
}

func (r *taskJobRepository) DeleteTerminalJobsFinishedBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}
	var jobIDs []string
	if err := r.db.WithContext(ctx).Model(&types.TaskJob{}).
		Where("state IN ?", taskLedgerTerminalStates).
		Where("finished_at IS NOT NULL AND finished_at < ?", cutoff).
		Order("finished_at ASC").
		Limit(limit).
		Pluck("job_id", &jobIDs).Error; err != nil {
		return 0, err
	}
	if len(jobIDs) == 0 {
		return 0, nil
	}
	res := r.db.WithContext(ctx).
		Where("job_id IN ?", jobIDs).
		Delete(&types.TaskJob{})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

func (r *taskJobRepository) baseJobAttemptUpdate(ctx context.Context, sel interfaces.TaskJobAttemptSelector) *gorm.DB {
	return r.db.WithContext(ctx).Model(&types.TaskJob{}).
		Where("tenant_id = ?", sel.TenantID).
		Where("scope = ?", sel.Scope).
		Where("scope_id = ?", sel.ScopeID).
		Where("process_attempt = ?", sel.ProcessAttempt)
}

func (r *taskJobRepository) markJobTerminal(
	ctx context.Context,
	sel interfaces.TaskJobAttemptSelector,
	state types.TaskJobState,
	failure interfaces.TaskLedgerFailure,
	finishedAt time.Time,
) (bool, error) {
	res := r.baseJobAttemptUpdate(ctx, sel).
		Where("state NOT IN ?", taskLedgerTerminalStates).
		Updates(jobFailureUpdates(state, failure, finishedAt))
	return rowsChanged(res)
}

func rowsChanged(res *gorm.DB) (bool, error) {
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func jobFailureUpdates(state types.TaskJobState, failure interfaces.TaskLedgerFailure, finishedAt time.Time) map[string]any {
	return map[string]any{
		"state":            state,
		"last_error_class": failure.ErrorClass,
		"last_error":       failure.LastError,
		"failed_task_type": failure.FailedTaskType,
		"failed_task_id":   failure.FailedTaskID,
		"finished_at":      finishedAt,
	}
}

func execFailureUpdates(state types.TaskExecutionState, failure interfaces.TaskLedgerFailure, finishedAt time.Time) map[string]any {
	return map[string]any{
		"state":       state,
		"error_class": failure.ErrorClass,
		"last_error":  failure.LastError,
		"finished_at": finishedAt,
	}
}
