package datasource

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

type outboxTaskEnqueuer struct {
	mu       sync.Mutex
	seen     []*asynq.Task
	err      error
	conflict bool
}

func (e *outboxTaskEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seen = append(e.seen, task)
	if e.conflict {
		return nil, asynq.ErrTaskIDConflict
	}
	if e.err != nil {
		return nil, e.err
	}
	return &asynq.TaskInfo{ID: "queue-task", Queue: types.QueueSync, Type: types.TypeDataSourceSync}, nil
}

type outboxSyncLogRepo struct {
	mu         sync.Mutex
	logs       map[string]*types.SyncLog
	dispatched []string
	failures   []string
}

func newOutboxSyncLogRepo(log *types.SyncLog) *outboxSyncLogRepo {
	return &outboxSyncLogRepo{logs: map[string]*types.SyncLog{log.ID: log}}
}
func (r *outboxSyncLogRepo) Create(_ context.Context, log *types.SyncLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs[log.ID] = log
	return nil
}
func (r *outboxSyncLogRepo) FindByID(_ context.Context, id string) (*types.SyncLog, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	log, ok := r.logs[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return log, nil
}
func (r *outboxSyncLogRepo) FindByDataSource(context.Context, string, int, int) ([]*types.SyncLog, error) {
	return nil, nil
}
func (r *outboxSyncLogRepo) FindLatest(context.Context, string) (*types.SyncLog, error) {
	return nil, nil
}
func (r *outboxSyncLogRepo) HasRunningSync(context.Context, string) (bool, error) { return false, nil }
func (r *outboxSyncLogRepo) Update(_ context.Context, log *types.SyncLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs[log.ID] = log
	return nil
}
func (r *outboxSyncLogRepo) UpdateResult(context.Context, *types.SyncLog) error      { return nil }
func (r *outboxSyncLogRepo) CancelPendingByDataSource(context.Context, string) error { return nil }
func (r *outboxSyncLogRepo) CleanupOldLogs(context.Context, int) error               { return nil }
func (r *outboxSyncLogRepo) FindUndispatched(_ context.Context, _ int) ([]*types.SyncLog, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]*types.SyncLog, 0)
	for _, log := range r.logs {
		if log.Status == types.SyncLogStatusPending && log.DispatchedAt == nil && log.TaskID != "" {
			result = append(result, log)
		}
	}
	return result, nil
}
func (r *outboxSyncLogRepo) MarkDispatched(_ context.Context, id string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	log := r.logs[id]
	log.DispatchedAt = &at
	r.dispatched = append(r.dispatched, id)
	return nil
}
func (r *outboxSyncLogRepo) MarkDispatchFailure(_ context.Context, id string, attempts int, next time.Time, message string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	log := r.logs[id]
	log.DispatchAttempts = attempts
	log.NextDispatchAt = &next
	log.LastDispatchError = message
	r.failures = append(r.failures, id)
	return nil
}

func testOutboxLog() *types.SyncLog {
	return &types.SyncLog{ID: "log-1", DataSourceID: "ds-1", Status: types.SyncLogStatusPending, TaskID: SyncTaskID("ds-1", "log-1"), TaskPayload: types.JSON(`{"data_source_id":"ds-1","sync_log_id":"log-1"}`)}
}

func TestSyncOutboxDispatchMarksSuccessfulDelivery(t *testing.T) {
	log := testOutboxLog()
	repo := newOutboxSyncLogRepo(log)
	enqueuer := &outboxTaskEnqueuer{}
	dispatcher := NewSyncOutboxDispatcher(repo, enqueuer)
	info, err := dispatcher.Dispatch(context.Background(), log)
	if err != nil {
		t.Fatal(err)
	}
	if info == nil || info.ID != "queue-task" {
		t.Fatalf("unexpected task info: %#v", info)
	}
	if log.DispatchedAt == nil {
		t.Fatal("dispatch timestamp was not recorded")
	}
	if len(repo.dispatched) != 1 {
		t.Fatalf("dispatched records = %d", len(repo.dispatched))
	}
}

func TestSyncOutboxTreatsTaskIDConflictAsAlreadyDelivered(t *testing.T) {
	log := testOutboxLog()
	repo := newOutboxSyncLogRepo(log)
	enqueuer := &outboxTaskEnqueuer{conflict: true}
	_, err := NewSyncOutboxDispatcher(repo, enqueuer).Dispatch(context.Background(), log)
	if err != nil {
		t.Fatal(err)
	}
	if log.DispatchedAt == nil {
		t.Fatal("task ID conflict must converge to dispatched")
	}
}

func TestSyncOutboxRetainsPendingOnQueueFailure(t *testing.T) {
	log := testOutboxLog()
	repo := newOutboxSyncLogRepo(log)
	enqueuer := &outboxTaskEnqueuer{err: errors.New("redis unavailable")}
	_, err := NewSyncOutboxDispatcher(repo, enqueuer).Dispatch(context.Background(), log)
	if err == nil {
		t.Fatal("queue failure should be returned")
	}
	if log.DispatchedAt != nil {
		t.Fatal("failed delivery must remain undispatched")
	}
	if log.DispatchAttempts != 1 || log.NextDispatchAt == nil {
		t.Fatalf("retry metadata not recorded: %+v", log)
	}
}

func TestSyncOutboxDispatchPendingRecoversUndeliveredRows(t *testing.T) {
	log := testOutboxLog()
	repo := newOutboxSyncLogRepo(log)
	enqueuer := &outboxTaskEnqueuer{}
	dispatched, err := NewSyncOutboxDispatcher(repo, enqueuer).DispatchPending(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if dispatched != 1 || log.DispatchedAt == nil {
		t.Fatalf("pending row was not recovered: dispatched=%d log=%+v", dispatched, log)
	}
}
