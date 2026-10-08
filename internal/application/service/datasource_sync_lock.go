package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/common/redislock"
	"github.com/redis/go-redis/v9"
)

const (
	dataSourceSyncLockLease = 30 * time.Second
	dataSourceSyncLockRenew = 10 * time.Second
)

// dataSourceSyncCoordinator serializes fetch/ingest/checkpoint for one source.
// Lite has one worker process; standard deployments share renewable Redis locks.
// Lease loss cancels cooperative work; this is not a database fencing protocol.
type dataSourceSyncCoordinator struct {
	redis *redis.Client
	mu    sync.Mutex
	local map[string]*dataSourceSyncEntry
}

type dataSourceSyncEntry struct {
	token chan struct{}
	refs  int
}

func (c *dataSourceSyncCoordinator) run(ctx context.Context, sourceID string, fn func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.redis != nil {
		return redislock.TryWithRenewableLock(ctx, c.redis, dataSourceSyncLockKey(sourceID),
			dataSourceSyncLockLease, dataSourceSyncLockRenew, fn)
	}
	return c.runLocal(ctx, sourceID, fn)
}

func dataSourceSyncLockKey(sourceID string) string {
	// Source IDs are globally unique. Do not partition by the task's tenant
	// metadata: every attempt for the same source must contend on the same key.
	return "weknora:datasource:sync:" + sourceID
}

func (c *dataSourceSyncCoordinator) runLocal(
	ctx context.Context, sourceID string, fn func(context.Context) error,
) error {
	entry := c.retain(sourceID)
	defer c.release(sourceID, entry)
	select {
	case entry.token <- struct{}{}:
		defer func() { <-entry.token }()
	default:
		return redislock.ErrLockBusy
	}
	// Acquisition and cancellation may become ready together.
	if err := ctx.Err(); err != nil {
		return err
	}
	err := fn(ctx)
	return errors.Join(err, ctx.Err())
}

func (c *dataSourceSyncCoordinator) retain(sourceID string) *dataSourceSyncEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.local == nil {
		c.local = make(map[string]*dataSourceSyncEntry)
	}
	entry := c.local[sourceID]
	if entry == nil {
		entry = &dataSourceSyncEntry{token: make(chan struct{}, 1)}
		c.local[sourceID] = entry
	}
	entry.refs++
	return entry
}

func (c *dataSourceSyncCoordinator) release(sourceID string, entry *dataSourceSyncEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry.refs--
	if entry.refs == 0 {
		delete(c.local, sourceID)
	}
}
