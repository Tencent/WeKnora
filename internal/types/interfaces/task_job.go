package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type TaskJobAttemptSelector struct {
	JobID          string
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

type TaskJobQuery struct {
	TenantID  uint64
	UserID    string
	IsAdmin   bool
	State     string
	Kind      string
	KBID      string
	CreatedBy string
	Q         string
	Page      int
	PageSize  int
	Sort      string
	Origin    string
}

type TaskJobSummary struct {
	Queued     int64 `json:"queued"`
	Processing int64 `json:"processing"`
	Succeeded  int64 `json:"succeeded"`
	Failed     int64 `json:"failed"`
	Canceled   int64 `json:"canceled"`
}

type TaskJobRepository interface {
	CreateJobAndExecution(ctx context.Context, job *types.TaskJob, execution *types.TaskExecution) error

	Summary(ctx context.Context, q TaskJobQuery) (*TaskJobSummary, error)
	ListJobs(ctx context.Context, q TaskJobQuery) ([]*types.TaskJob, int64, error)
	GetJob(ctx context.Context, tenantID uint64, jobID string) (*types.TaskJob, error)
	GetJobByExecutionID(ctx context.Context, executionID string) (*types.TaskJob, error)
	GetLatestJobForScopeAttempt(ctx context.Context, tenantID uint64, scope, scopeID string, attempt int) (*types.TaskJob, error)
	ListExecutions(ctx context.Context, tenantID uint64, jobID string) ([]*types.TaskExecution, error)

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
	MarkExecutionsCanceledForJob(ctx context.Context, tenantID uint64, jobID string, failure TaskLedgerFailure, finishedAt time.Time) (int64, error)

	FindStaleDispatches(ctx context.Context, cutoff time.Time, limit int) ([]*types.TaskExecution, error)
	MarkStaleDispatchFailed(ctx context.Context, executionID string, finishedAt time.Time) (bool, error)
	DeleteTerminalJobsFinishedBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error)
}
