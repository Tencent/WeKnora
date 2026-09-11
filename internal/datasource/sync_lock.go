package datasource

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var ErrSyncRunning = errors.New("datasource_sync_running")
var DefaultSyncLocks = NewSyncLocks(nil)

// SyncLocks is shared by scheduling, mutations and workers. Lite is single-process.
type SyncLocks struct {
	Redis  *redis.Client
	mu     sync.Mutex
	owners map[string]string
}

func NewSyncLocks(rdb *redis.Client) *SyncLocks {
	return &SyncLocks{Redis: rdb, owners: map[string]string{}}
}

type leaseKey struct{}
type syncLease struct {
	check func(context.Context) error
}

func CheckSyncLease(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if lease, ok := ctx.Value(leaseKey{}).(*syncLease); ok {
		return lease.check(ctx)
	}
	return nil
}

func (l *SyncLocks) Acquire(ctx context.Context, tenant uint64, id string) (context.Context, func(), error) {
	key, token := fmt.Sprintf("datasource:lease:%d:%s", tenant, id), uuid.NewString()
	if l.Redis != nil {
		ok, err := l.Redis.SetNX(ctx, key, token, 120*time.Second).Result()
		if err != nil {
			return ctx, nil, err
		}
		if !ok {
			return ctx, nil, ErrSyncRunning
		}
	} else {
		l.mu.Lock()
		if l.owners == nil {
			l.owners = map[string]string{}
		}
		if l.owners[key] != "" {
			l.mu.Unlock()
			return ctx, nil, ErrSyncRunning
		}
		l.owners[key] = token
		l.mu.Unlock()
	}
	runCtx, cancel := context.WithCancel(ctx)
	lease := &syncLease{check: func(ctx context.Context) error {
		if l.Redis != nil {
			owner, err := l.Redis.Get(ctx, key).Result()
			if err != nil || owner != token {
				cancel()
				return errors.New("datasource_lease_lost")
			}
		} else {
			l.mu.Lock()
			ok := l.owners[key] == token
			l.mu.Unlock()
			if !ok {
				cancel()
				return errors.New("datasource_lease_lost")
			}
		}
		return nil
	}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				if l.Redis != nil {
					ok, err := l.Redis.Eval(runCtx, `if redis.call("get",KEYS[1]) == ARGV[1] then return redis.call("expire",KEYS[1],120) else return 0 end`, []string{key}, token).Int()
					if err != nil || ok != 1 {
						cancel()
						return
					}
				}
			}
		}
	}()
	release := func() {
		cancel()
		<-done
		if l.Redis != nil {
			ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_ = l.Redis.Eval(ctx, `if redis.call("get",KEYS[1]) == ARGV[1] then return redis.call("del",KEYS[1]) else return 0 end`, []string{key}, token).Err()
		} else {
			l.mu.Lock()
			if l.owners[key] == token {
				delete(l.owners, key)
			}
			l.mu.Unlock()
		}
	}
	return context.WithValue(runCtx, leaseKey{}, lease), release, nil
}
