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
			case types.ClassifyTaskError(handlerErr) == types.TaskErrorClassCanceled:
				if _, err := repo.MarkExecCanceledIfNonTerminal(context.Background(), taskID, interfaces.TaskLedgerFailure{
					ErrorClass: types.ClassifyTaskError(handlerErr),
					LastError:  truncateLedgerError(handlerErr),
				}, now); err != nil {
					observability.RecordTaskLedgerWriteFailure("asynq", "exec_canceled")
				}
			case errors.Is(handlerErr, asynq.SkipRetry) || retryCount >= maxRetry:
				if _, err := repo.MarkExecFailedIfNonTerminal(context.Background(), taskID, interfaces.TaskLedgerFailure{
					ErrorClass: types.ClassifyTaskError(handlerErr),
					LastError:  truncateLedgerError(handlerErr),
				}, now); err != nil {
					observability.RecordTaskLedgerWriteFailure("asynq", "exec_failed")
				}
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
