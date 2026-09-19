package datasource

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

const defaultOutboxBatchSize = 100

const syncDispatchAlertThreshold = 10

// SyncOutboxDispatcher reliably projects pending SyncLog rows into Asynq.
// The database row is the durable intent; queue delivery is retryable and
// idempotent through the per-run TaskID stored on that row.
type SyncOutboxDispatcher struct {
	repo     interfaces.SyncLogRepository
	enqueuer interfaces.TaskEnqueuer
}

func NewSyncOutboxDispatcher(repo interfaces.SyncLogRepository, enqueuer interfaces.TaskEnqueuer) *SyncOutboxDispatcher {
	return &SyncOutboxDispatcher{repo: repo, enqueuer: enqueuer}
}

func (d *SyncOutboxDispatcher) Dispatch(ctx context.Context, log *types.SyncLog) (*asynq.TaskInfo, error) {
	if d == nil || d.repo == nil || d.enqueuer == nil {
		return nil, errors.New("sync outbox dispatcher is not configured")
	}
	if log == nil || log.ID == "" || log.DataSourceID == "" {
		return nil, errors.New("sync outbox log identity is required")
	}
	if log.TaskID == "" || len(log.TaskPayload) == 0 {
		return nil, errors.New("sync outbox task_id and payload are required")
	}

	task := asynq.NewTask(types.TypeDataSourceSync, []byte(log.TaskPayload))
	info, err := d.enqueuer.Enqueue(task,
		asynq.Queue(types.QueueSync), asynq.MaxRetry(5), asynq.Timeout(2*time.Hour), asynq.TaskID(log.TaskID))
	if err != nil && !errors.Is(err, asynq.ErrTaskIDConflict) && !errors.Is(err, asynq.ErrDuplicateTask) {
		attempts := log.DispatchAttempts + 1
		next := time.Now().UTC().Add(syncDispatchBackoff(attempts))
		if attempts >= syncDispatchAlertThreshold {
			logger.Errorf(ctx, "data source sync outbox delivery is repeatedly failing: log=%s task=%s attempts=%d next=%s err=%v", log.ID, log.TaskID, attempts, next.Format(time.RFC3339), err)
		}
		if outbox, ok := d.repo.(interfaces.SyncLogOutboxRepository); ok {
			_ = outbox.MarkDispatchFailure(ctx, log.ID, attempts, next, err.Error())
		}
		return nil, fmt.Errorf("dispatch data source sync %q: %w", log.ID, err)
	}

	dispatchedAt := time.Now().UTC()
	if outbox, ok := d.repo.(interfaces.SyncLogOutboxRepository); ok {
		if markErr := outbox.MarkDispatched(ctx, log.ID, dispatchedAt); markErr != nil {
			return info, fmt.Errorf("mark data source sync %q dispatched: %w", log.ID, markErr)
		}
	}
	log.DispatchedAt = &dispatchedAt
	log.NextDispatchAt = nil
	log.LastDispatchError = ""
	if info == nil {
		info = &asynq.TaskInfo{ID: log.TaskID, Queue: types.QueueSync, Type: types.TypeDataSourceSync}
	}
	return info, nil
}

func (d *SyncOutboxDispatcher) DispatchPending(ctx context.Context, limit int) (int, error) {
	if d == nil || d.repo == nil {
		return 0, errors.New("sync outbox dispatcher is not configured")
	}
	outbox, ok := d.repo.(interfaces.SyncLogOutboxRepository)
	if !ok {
		return 0, nil
	}
	if limit <= 0 {
		limit = defaultOutboxBatchSize
	}
	logs, err := outbox.FindUndispatched(ctx, limit)
	if err != nil {
		return 0, err
	}
	dispatched := 0
	var resultErr error
	for _, log := range logs {
		if _, err := d.Dispatch(ctx, log); err != nil {
			resultErr = errors.Join(resultErr, err)
			continue
		}
		dispatched++
	}
	return dispatched, resultErr
}

func syncDispatchBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 7 {
		attempt = 7
	}
	delay := 5 * time.Second * time.Duration(1<<(attempt-1))
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}
