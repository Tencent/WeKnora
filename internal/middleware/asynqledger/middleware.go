package asynqledger

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/observability"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

// Middleware updates pre-seeded task_executions rows for user root tasks.
// Missing execution rows are treated as internal fan-out work and are never
// inserted here.
func Middleware(repo interfaces.TaskJobRepository) asynq.MiddlewareFunc {
	return func(next asynq.Handler) asynq.Handler {
		return asynq.HandlerFunc(func(ctx context.Context, task *asynq.Task) error {
			if repo == nil {
				return next.ProcessTask(ctx, task)
			}
			taskID, ok := asynq.GetTaskID(ctx)
			if !ok || taskID == "" {
				return next.ProcessTask(ctx, task)
			}
			retryCount, _ := asynq.GetRetryCount(ctx)
			maxRetry, _ := asynq.GetMaxRetry(ctx)

			updated, err := repo.MarkExecActiveIfExists(ctx, taskID, retryCount, time.Now())
			if err != nil || !updated {
				if err != nil {
					observability.RecordTaskLedgerWriteFailure("asynq", "exec_active")
				}
				return next.ProcessTask(ctx, task)
			}

			handlerErr := next.ProcessTask(ctx, task)
			now := time.Now()
			switch {
			case handlerErr == nil:
				if _, err := repo.MarkExecSucceededIfNonTerminal(context.Background(), taskID, now); err != nil {
					observability.RecordTaskLedgerWriteFailure("asynq", "exec_succeeded")
				}
				markRootJobSucceededIfNeeded(repo, taskID, now)
			case types.ClassifyTaskError(handlerErr) == types.TaskErrorClassCanceled:
				if _, err := repo.MarkExecCanceledIfNonTerminal(context.Background(), taskID, interfaces.TaskLedgerFailure{
					ErrorClass: types.ClassifyTaskError(handlerErr),
					LastError:  truncateLedgerError(handlerErr),
				}, now); err != nil {
					observability.RecordTaskLedgerWriteFailure("asynq", "exec_canceled")
				}
				markRootJobCanceledIfNeeded(repo, taskID, task.Type(), handlerErr, now)
			case errors.Is(handlerErr, asynq.SkipRetry) || retryCount >= maxRetry:
				if _, err := repo.MarkExecFailedIfNonTerminal(context.Background(), taskID, interfaces.TaskLedgerFailure{
					ErrorClass: types.ClassifyTaskError(handlerErr),
					LastError:  truncateLedgerError(handlerErr),
				}, now); err != nil {
					observability.RecordTaskLedgerWriteFailure("asynq", "exec_failed")
				}
				markRootJobFailedIfNeeded(repo, taskID, task.Type(), handlerErr, now)
			default:
				if _, err := repo.MarkExecRetryingIfNonTerminal(context.Background(), taskID, retryCount+1, interfaces.TaskLedgerFailure{
					ErrorClass: types.ClassifyTaskError(handlerErr),
					LastError:  truncateLedgerError(handlerErr),
				}); err != nil {
					observability.RecordTaskLedgerWriteFailure("asynq", "exec_retrying")
				}
			}
			return handlerErr
		})
	}
}

func markRootJobSucceededIfNeeded(repo interfaces.TaskJobRepository, executionID string, finishedAt time.Time) {
	job, ok := rootCompletingJob(repo, executionID)
	if !ok {
		return
	}
	if _, err := repo.MarkJobSucceededIfCurrentAttempt(context.Background(), jobAttemptSelector(job), finishedAt); err != nil {
		observability.RecordTaskLedgerWriteFailure("asynq", "job_succeeded")
	}
}

func markRootJobFailedIfNeeded(repo interfaces.TaskJobRepository, executionID, taskType string, taskErr error, finishedAt time.Time) {
	job, ok := rootCompletingJob(repo, executionID)
	if !ok {
		return
	}
	if _, err := repo.MarkJobFailedIfCurrentAttempt(context.Background(), jobAttemptSelector(job), interfaces.TaskLedgerFailure{
		ErrorClass:     types.ClassifyTaskError(taskErr),
		LastError:      truncateLedgerError(taskErr),
		FailedTaskType: taskType,
		FailedTaskID:   executionID,
	}, finishedAt); err != nil {
		observability.RecordTaskLedgerWriteFailure("asynq", "job_failed")
	}
}

func markRootJobCanceledIfNeeded(repo interfaces.TaskJobRepository, executionID, taskType string, taskErr error, finishedAt time.Time) {
	job, ok := rootCompletingJob(repo, executionID)
	if !ok {
		return
	}
	if _, err := repo.MarkJobCanceledIfCurrentAttempt(context.Background(), jobAttemptSelector(job), interfaces.TaskLedgerFailure{
		ErrorClass:     types.TaskErrorClassCanceled,
		LastError:      truncateLedgerError(taskErr),
		FailedTaskType: taskType,
		FailedTaskID:   executionID,
	}, finishedAt); err != nil {
		observability.RecordTaskLedgerWriteFailure("asynq", "job_canceled")
	}
}

func rootCompletingJob(repo interfaces.TaskJobRepository, executionID string) (*types.TaskJob, bool) {
	if repo == nil || executionID == "" {
		return nil, false
	}
	job, err := repo.GetJobByExecutionID(context.Background(), executionID)
	if err != nil {
		observability.RecordTaskLedgerWriteFailure("asynq", "job_lookup")
		return nil, false
	}
	if job == nil || !types.TaskJobKindCompletesOnRootExecution(job.Kind) {
		return nil, false
	}
	return job, true
}

func jobAttemptSelector(job *types.TaskJob) interfaces.TaskJobAttemptSelector {
	return interfaces.TaskJobAttemptSelector{
		TenantID:       job.TenantID,
		Scope:          job.Scope,
		ScopeID:        job.ScopeID,
		ProcessAttempt: job.ProcessAttempt,
	}
}

func truncateLedgerError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 8192 {
		return msg[:8192]
	}
	return msg
}
