package asynqledger

import (
	"context"
	"errors"
	"time"

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
				return next.ProcessTask(ctx, task)
			}

			handlerErr := next.ProcessTask(ctx, task)
			now := time.Now()
			switch {
			case handlerErr == nil:
				_, _ = repo.MarkExecSucceededIfNonTerminal(context.Background(), taskID, now)
			case errors.Is(handlerErr, context.Canceled):
				_, _ = repo.MarkExecCanceledIfNonTerminal(context.Background(), taskID, interfaces.TaskLedgerFailure{
					ErrorClass: types.TaskErrorClassCanceled,
					LastError:  truncateLedgerError(handlerErr),
				}, now)
			case errors.Is(handlerErr, asynq.SkipRetry) || retryCount >= maxRetry:
				_, _ = repo.MarkExecFailedIfNonTerminal(context.Background(), taskID, interfaces.TaskLedgerFailure{
					ErrorClass: types.TaskErrorClassTerminal,
					LastError:  truncateLedgerError(handlerErr),
				}, now)
			default:
				_, _ = repo.MarkExecRetryingIfNonTerminal(context.Background(), taskID, retryCount+1, interfaces.TaskLedgerFailure{
					ErrorClass: types.TaskErrorClassRetryable,
					LastError:  truncateLedgerError(handlerErr),
				})
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
