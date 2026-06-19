package service

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/observability"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

type TaskJobDispatcher struct {
	repo     interfaces.TaskJobRepository
	enqueuer interfaces.TaskEnqueuer
}

type UserRootDispatchRequest struct {
	JobID          string
	ExecutionID    string
	TenantID       uint64
	CreatedBy      string
	Kind           types.TaskJobKind
	Scope          string
	ScopeID        string
	RelatedID      string
	ProcessAttempt int
	DisplayName    string
	Metadata       types.JSON
	ReplaySpec     types.JSON
	Task           *asynq.Task
	Options        []asynq.Option
}

func NewTaskJobDispatcher(
	repo interfaces.TaskJobRepository,
	enqueuer interfaces.TaskEnqueuer,
) *TaskJobDispatcher {
	return &TaskJobDispatcher{repo: repo, enqueuer: enqueuer}
}

func (d *TaskJobDispatcher) DispatchUserRoot(ctx context.Context, req UserRootDispatchRequest) (*asynq.TaskInfo, error) {
	if d == nil || d.enqueuer == nil {
		return nil, errors.New("task ledger dispatcher: task enqueuer is not configured")
	}
	if req.Task == nil || req.JobID == "" || req.ExecutionID == "" {
		return nil, errors.New("task ledger dispatcher: task, job_id, and execution_id are required")
	}
	if len(req.Metadata) == 0 {
		req.Metadata = types.JSON(`{}`)
	}
	if len(req.ReplaySpec) == 0 {
		req.ReplaySpec = types.JSON(`{}`)
	}

	if d.repo != nil {
		now := time.Now()
		err := d.repo.CreateJobAndExecution(ctx, &types.TaskJob{
			JobID:          req.JobID,
			TenantID:       req.TenantID,
			CreatedBy:      req.CreatedBy,
			Kind:           req.Kind,
			Origin:         types.TaskJobOriginUser,
			DisplayName:    req.DisplayName,
			Scope:          req.Scope,
			ScopeID:        req.ScopeID,
			RelatedID:      req.RelatedID,
			ProcessAttempt: req.ProcessAttempt,
			State:          types.TaskJobStateQueued,
			Metadata:       req.Metadata,
			ReplaySpec:     req.ReplaySpec,
			CreatedAt:      now,
			UpdatedAt:      now,
		}, &types.TaskExecution{
			ExecutionID:    req.ExecutionID,
			JobID:          req.JobID,
			ProcessAttempt: req.ProcessAttempt,
			TaskType:       req.Task.Type(),
			Queue:          queueNameFromOptions(req.Options),
			State:          types.TaskExecutionStateQueued,
			EnqueuedAt:     now,
		})
		if err != nil {
			observability.RecordTaskLedgerWriteFailure("dispatcher", "create_job_execution")
			logger.Warnf(ctx, "task ledger: failed to create job/execution job=%s exec=%s: %v",
				req.JobID, req.ExecutionID, err)
		}
	}

	opts := append([]asynq.Option{}, req.Options...)
	opts = append(opts, asynq.TaskID(req.ExecutionID))
	info, err := d.enqueuer.Enqueue(req.Task, opts...)
	if err != nil {
		if d.repo != nil {
			if _, markErr := d.repo.MarkDispatchFailed(context.Background(), req.JobID, req.ExecutionID, interfaces.TaskLedgerFailure{
				ErrorClass: types.TaskErrorClassEnqueueFailed,
				LastError:  err.Error(),
			}, time.Now()); markErr != nil {
				observability.RecordTaskLedgerWriteFailure("dispatcher", "dispatch_failed")
			}
		}
		return nil, err
	}
	if d.repo != nil {
		if _, err := d.repo.MarkDispatched(context.Background(), req.ExecutionID, time.Now()); err != nil {
			observability.RecordTaskLedgerWriteFailure("dispatcher", "dispatched")
		}
	}
	return info, nil
}

func queueNameFromOptions(opts []asynq.Option) string {
	for _, opt := range opts {
		if opt.Type() == asynq.QueueOpt {
			if q, ok := opt.Value().(string); ok {
				return q
			}
		}
	}
	return types.QueueDefault
}
