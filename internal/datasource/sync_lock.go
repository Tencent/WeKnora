package datasource

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/common/redislock"
	"github.com/redis/go-redis/v9"
)

var ErrSyncAlreadyRunning = errors.New("data source sync already running")

const (
	defaultSyncLockLease    = 45 * time.Second
	defaultSyncLockRenew    = 15 * time.Second
	defaultTriggerLockLease = 15 * time.Second
	defaultTriggerLockRenew = 5 * time.Second
)

func SyncTaskID(dataSourceID, syncLogID string) string {
	return "dssync:" + dataSourceID + ":" + syncLogID
}

type SyncExecutionLock interface {
	Context() context.Context
	Release() error
}

type SyncExecutionLocker interface {
	TryAcquire(context.Context, string) (SyncExecutionLock, error)
}

func NewSyncExecutionLocker(client redis.UniversalClient) SyncExecutionLocker {
	if client == nil {
		return &localSyncLocker{held: make(map[string]struct{})}
	}
	return &redisSyncLocker{client: client, suffix: "sync-lock", lease: defaultSyncLockLease, renew: defaultSyncLockRenew}
}

// SyncCoordinator owns the two distinct synchronization boundaries:
// triggerLock serializes check+create+enqueue, while executionLock serializes
// actual connector execution. Neither relies on Asynq task retention semantics.
type SyncCoordinator struct {
	triggerLock   SyncExecutionLocker
	executionLock SyncExecutionLocker
}

func NewSyncCoordinator(client *redis.Client) *SyncCoordinator {
	if client == nil {
		return &SyncCoordinator{
			triggerLock:   &localSyncLocker{held: make(map[string]struct{})},
			executionLock: &localSyncLocker{held: make(map[string]struct{})},
		}
	}
	return &SyncCoordinator{
		triggerLock:   &redisSyncLocker{client: client, suffix: "trigger-lock", lease: defaultTriggerLockLease, renew: defaultTriggerLockRenew},
		executionLock: &redisSyncLocker{client: client, suffix: "sync-lock", lease: defaultSyncLockLease, renew: defaultSyncLockRenew},
	}
}

func (c *SyncCoordinator) WithTriggerLock(ctx context.Context, dataSourceID string, fn func(context.Context) error) (resultErr error) {
	if c == nil || c.triggerLock == nil {
		return errors.New("data source trigger coordinator is required")
	}
	if fn == nil {
		return errors.New("data source trigger callback is required")
	}
	lock, err := c.triggerLock.TryAcquire(ctx, dataSourceID)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Release()) }()
	return fn(lock.Context())
}

func (c *SyncCoordinator) TryAcquireTrigger(ctx context.Context, dataSourceID string) (SyncExecutionLock, error) {
	if c == nil || c.triggerLock == nil {
		return nil, errors.New("data source trigger coordinator is required")
	}
	return c.triggerLock.TryAcquire(ctx, dataSourceID)
}

func (c *SyncCoordinator) TryAcquireExecution(ctx context.Context, dataSourceID string) (SyncExecutionLock, error) {
	if c == nil || c.executionLock == nil {
		return nil, errors.New("data source execution coordinator is required")
	}
	return c.executionLock.TryAcquire(ctx, dataSourceID)
}

type localSyncLocker struct {
	mu   sync.Mutex
	held map[string]struct{}
}

func (l *localSyncLocker) TryAcquire(ctx context.Context, dataSourceID string) (SyncExecutionLock, error) {
	l.mu.Lock()
	if _, exists := l.held[dataSourceID]; exists {
		l.mu.Unlock()
		return nil, ErrSyncAlreadyRunning
	}
	l.held[dataSourceID] = struct{}{}
	l.mu.Unlock()
	return &localSyncLock{ctx: ctx, release: func() { l.mu.Lock(); delete(l.held, dataSourceID); l.mu.Unlock() }}, nil
}

type localSyncLock struct {
	ctx     context.Context
	once    sync.Once
	release func()
}

func (l *localSyncLock) Context() context.Context { return l.ctx }
func (l *localSyncLock) Release() error           { l.once.Do(l.release); return nil }

type redisSyncLocker struct {
	client redis.UniversalClient
	suffix string
	lease  time.Duration
	renew  time.Duration
}

func (l *redisSyncLocker) TryAcquire(ctx context.Context, dataSourceID string) (SyncExecutionLock, error) {
	if dataSourceID == "" {
		return nil, errors.New("data source ID is required for sync lock")
	}
	token, err := redislock.NewToken()
	if err != nil {
		return nil, err
	}
	key := "weknora:datasource:{" + dataSourceID + "}:" + l.suffix
	acquired, err := redislock.TryAcquire(ctx, l.client, key, token, l.lease)
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, ErrSyncAlreadyRunning
	}
	lockCtx, cancel := context.WithCancelCause(ctx)
	lease := &redisSyncLock{client: l.client, key: key, token: token, lease: l.lease, renew: l.renew, ctx: lockCtx, cancel: cancel, stop: make(chan struct{}), done: make(chan error, 1)}
	go lease.renewLoop()
	return lease, nil
}

type redisSyncLock struct {
	client       redis.UniversalClient
	key, token   string
	lease, renew time.Duration
	ctx          context.Context
	cancel       context.CancelCauseFunc
	stop         chan struct{}
	done         chan error
	once         sync.Once
	releaseErr   error
}

func (l *redisSyncLock) Context() context.Context { return l.ctx }
func (l *redisSyncLock) Release() error {
	l.once.Do(func() {
		close(l.stop)
		renewErr := <-l.done
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		released, err := redislock.Release(releaseCtx, l.client, l.key, l.token)
		cancel()
		if err == nil && !released && renewErr == nil {
			err = redislock.ErrLockOwnershipLost
		}
		l.cancel(nil)
		l.releaseErr = errors.Join(renewErr, err)
	})
	return l.releaseErr
}
func (l *redisSyncLock) renewLoop() {
	ticker := time.NewTicker(l.renew)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			l.done <- nil
			return
		case <-ticker.C:
			renewed, err := redislock.Renew(context.Background(), l.client, l.key, l.token, l.lease)
			if err != nil || !renewed {
				if err == nil {
					err = redislock.ErrLockOwnershipLost
				}
				err = fmt.Errorf("renew data source sync lock: %w", err)
				l.cancel(err)
				l.done <- err
				return
			}
		}
	}
}
