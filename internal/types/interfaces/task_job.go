package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type TaskJobAttemptSelector struct {
	TenantID       uint64
	Scope          string
	ScopeID        string
	ProcessAttempt int
}

type TaskLedgerFailure struct {
	ErrorClass     types.TaskErrorClass
	LastError      string
	FailedTaskType string
	FailedTaskID   string
}

type TaskJobRepository interface {
	CreateJobAndExecution(ctx context.Context, job *types.TaskJob, execution *types.TaskExecution) error

	MarkJobProcessingIfCurrentAttempt(ctx context.Context, sel TaskJobAttemptSelector) (bool, error)
	MarkJobFinalizingIfCurrentAttempt(ctx context.Context, sel TaskJobAttemptSelector) (bool, error)
	MarkJobSucceededIfCurrentAttempt(ctx context.Context, sel TaskJobAttemptSelector, finishedAt time.Time) (bool, error)
	MarkJobFailedIfCurrentAttempt(ctx context.Context, sel TaskJobAttemptSelector, failure TaskLedgerFailure, finishedAt time.Time) (bool, error)
	MarkJobCanceledIfCurrentAttempt(ctx context.Context, sel TaskJobAttemptSelector, failure TaskLedgerFailure, finishedAt time.Time) (bool, error)

	MarkDispatched(ctx context.Context, executionID string, dispatchedAt time.Time) (bool, error)
	MarkDispatchFailed(ctx context.Context, jobID, executionID string, failure TaskLedgerFailure, finishedAt time.Time) (bool, error)
	MarkExecActiveIfExists(ctx context.Context, executionID string, retryCount int, startedAt time.Time) (bool, error)
	MarkExecRetryingIfNonTerminal(ctx context.Context, executionID string, retryCount int, failure TaskLedgerFailure) (bool, error)
	MarkExecSucceededIfNonTerminal(ctx context.Context, executionID string, finishedAt time.Time) (bool, error)
	MarkExecFailedIfNonTerminal(ctx context.Context, executionID string, failure TaskLedgerFailure, finishedAt time.Time) (bool, error)
	MarkExecCanceledIfNonTerminal(ctx context.Context, executionID string, failure TaskLedgerFailure, finishedAt time.Time) (bool, error)

	PrepareManualRetry(ctx context.Context, jobID, newExecutionID, retryTaskType, queue string, enqueuedAt time.Time) (*types.TaskExecution, bool, error)
	FindStaleDispatches(ctx context.Context, cutoff time.Time, limit int) ([]*types.TaskExecution, error)
	MarkStaleDispatchFailed(ctx context.Context, executionID string, finishedAt time.Time) (bool, error)
	DeleteTerminalJobsFinishedBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error)
}
