package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type taskJobRepository struct {
	db *gorm.DB
}

func (r *taskJobRepository) Summary(ctx context.Context, q interfaces.TaskJobQuery) (*interfaces.TaskJobSummary, error) {
	var rows []struct {
		State string
		Count int64
	}
	err := r.baseJobQuery(ctx, q).
		Select("state, COUNT(*) AS count").
		Group("state").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := &interfaces.TaskJobSummary{}
	for _, row := range rows {
		switch types.TaskJobState(row.State) {
		case types.TaskJobStateQueued:
			out.Queued = row.Count
		case types.TaskJobStateProcessing, types.TaskJobStateFinalizing:
			out.Processing += row.Count
		case types.TaskJobStateSucceeded:
			out.Succeeded = row.Count
		case types.TaskJobStateFailed:
			out.Failed = row.Count
		case types.TaskJobStateCanceled:
			out.Canceled = row.Count
		}
	}
	return out, nil
}

func (r *taskJobRepository) ListJobs(ctx context.Context, q interfaces.TaskJobQuery) ([]*types.TaskJob, int64, error) {
	page, pageSize := normalizeTaskJobPage(q.Page, q.PageSize)
	base := r.baseJobQuery(ctx, q)
	var total int64
	if err := base.Model(&types.TaskJob{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []*types.TaskJob
	err := base.
		Order(taskJobSort(q.Sort)).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&rows).Error
	return rows, total, err
}

func (r *taskJobRepository) GetJob(ctx context.Context, tenantID uint64, jobID string) (*types.TaskJob, error) {
	var job types.TaskJob
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND job_id = ?", tenantID, jobID).
		First(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *taskJobRepository) GetJobByExecutionID(ctx context.Context, executionID string) (*types.TaskJob, error) {
	var job types.TaskJob
	err := r.db.WithContext(ctx).
		Joins("JOIN task_executions ON task_executions.job_id = task_jobs.job_id").
		Where("task_executions.execution_id = ?", executionID).
		First(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *taskJobRepository) GetLatestJobForScopeAttempt(
	ctx context.Context,
	tenantID uint64,
	scope, scopeID string,
	attempt int,
) (*types.TaskJob, error) {
	var job types.TaskJob
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope = ? AND scope_id = ? AND process_attempt = ?", tenantID, scope, scopeID, attempt).
		Order("created_at DESC").
		First(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *taskJobRepository) ListExecutions(ctx context.Context, tenantID uint64, jobID string) ([]*types.TaskExecution, error) {
	var rows []*types.TaskExecution
	err := r.db.WithContext(ctx).
		Joins("JOIN task_jobs ON task_jobs.job_id = task_executions.job_id").
		Where("task_jobs.tenant_id = ? AND task_executions.job_id = ?", tenantID, jobID).
		Order("task_executions.enqueued_at DESC").
		Find(&rows).Error
	return rows, err
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

func (r *taskJobRepository) MarkExecutionsCanceledForJob(
	ctx context.Context,
	tenantID uint64,
	jobID string,
	failure interfaces.TaskLedgerFailure,
	finishedAt time.Time,
) (int64, error) {
	if failure.ErrorClass == "" {
		failure.ErrorClass = types.TaskErrorClassCanceled
	}
	res := r.db.WithContext(ctx).Model(&types.TaskExecution{}).
		Where("job_id = ?", jobID).
		Where("state NOT IN ?", taskExecutionTerminalStates).
		Where("EXISTS (SELECT 1 FROM task_jobs WHERE task_jobs.job_id = task_executions.job_id AND task_jobs.tenant_id = ?)", tenantID).
		Updates(execFailureUpdates(types.TaskExecutionStateCanceled, failure, finishedAt))
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
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
	tx := r.db.WithContext(ctx).Model(&types.TaskJob{}).
		Where("tenant_id = ?", sel.TenantID).
		Where("scope = ?", sel.Scope).
		Where("scope_id = ?", sel.ScopeID).
		Where("process_attempt = ?", sel.ProcessAttempt)
	if sel.JobID != "" {
		tx = tx.Where("job_id = ?", sel.JobID)
	}
	return tx
}

func (r *taskJobRepository) baseJobQuery(ctx context.Context, q interfaces.TaskJobQuery) *gorm.DB {
	origin := q.Origin
	if origin == "" {
		origin = string(types.TaskJobOriginUser)
	}
	tx := r.db.WithContext(ctx).Model(&types.TaskJob{}).
		Where("tenant_id = ?", q.TenantID).
		Where("origin = ?", origin)
	if !q.IsAdmin {
		tx = tx.Where("created_by = ?", q.UserID)
	} else if q.CreatedBy != "" {
		tx = tx.Where("created_by = ?", q.CreatedBy)
	}
	if q.State != "" {
		if q.State == "failed_or_canceled" {
			tx = tx.Where("state IN ?", []string{string(types.TaskJobStateFailed), string(types.TaskJobStateCanceled)})
		} else if q.State == "processing" {
			tx = tx.Where("state IN ?", []string{string(types.TaskJobStateProcessing), string(types.TaskJobStateFinalizing)})
		} else {
			tx = tx.Where("state = ?", q.State)
		}
	}
	if q.Kind != "" {
		tx = tx.Where("kind = ?", q.Kind)
	}
	if q.KBID != "" {
		tx = tx.Where("related_id = ?", q.KBID)
	}
	if keyword := strings.TrimSpace(q.Q); keyword != "" {
		like := "%" + keyword + "%"
		tx = tx.Where("(display_name LIKE ? OR scope_id LIKE ? OR job_id LIKE ?)", like, like, like)
	}
	return tx
}

func normalizeTaskJobPage(page, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

func taskJobSort(sort string) string {
	switch sort {
	case "created_at_asc":
		return "created_at ASC"
	case "updated_at_desc":
		return "updated_at DESC"
	case "updated_at_asc":
		return "updated_at ASC"
	default:
		return "created_at DESC"
	}
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
