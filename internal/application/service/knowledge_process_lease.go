package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	ErrKnowledgeProcessLeaseBusy = errors.New("knowledge process lease busy")
	ErrKnowledgeProcessLeaseLost = errors.New("knowledge process lease lost")
)

const (
	knowledgeProcessLeaseTTL   = 2 * time.Minute
	knowledgeProcessLeaseRenew = 30 * time.Second
)

type knowledgeProcessLeaseCtxKey struct{}

type keyedMutex struct {
	mu sync.Mutex
}

type KnowledgeProcessLease struct {
	Context context.Context
	Cancel  context.CancelFunc
	Token   string
	Key     string
	owned   bool
	Release func()
}

var globalMemProcessLeases sync.Map // knowledge-process key -> *keyedMutex

func knowledgeProcessLeaseKey(tenantID uint64, knowledgeID string) string {
	return fmt.Sprintf("knowledge-process:%d:%s", tenantID, knowledgeID)
}

func leaseFromCtx(ctx context.Context, key string) *KnowledgeProcessLease {
	lease, _ := ctx.Value(knowledgeProcessLeaseCtxKey{}).(*KnowledgeProcessLease)
	if lease != nil && lease.Key == key {
		return lease
	}
	return nil
}

func withKnowledgeProcessLease(ctx context.Context, lease *KnowledgeProcessLease) context.Context {
	return context.WithValue(ctx, knowledgeProcessLeaseCtxKey{}, lease)
}

func (s *knowledgeService) acquireKnowledgeProcessLease(
	ctx context.Context,
	tenantID uint64,
	knowledgeID string,
) (*KnowledgeProcessLease, error) {
	return acquireKnowledgeProcessLease(ctx, s.redisClient, tenantID, knowledgeID)
}

func acquireKnowledgeProcessLease(
	ctx context.Context,
	redisClient *redis.Client,
	tenantID uint64,
	knowledgeID string,
) (*KnowledgeProcessLease, error) {
	if tenantID == 0 || knowledgeID == "" {
		leaseCtx, cancel := context.WithCancel(ctx)
		var once sync.Once
		return &KnowledgeProcessLease{
			Context: leaseCtx,
			Cancel:  cancel,
			Release: func() { once.Do(cancel) },
		}, nil
	}
	key := knowledgeProcessLeaseKey(tenantID, knowledgeID)
	if lease := leaseFromCtx(ctx, key); lease != nil {
		borrowed := *lease
		borrowed.owned = false
		borrowed.Release = func() {}
		return &borrowed, nil
	}
	if redisClient == nil {
		return acquireMemoryKnowledgeProcessLease(ctx, key)
	}
	return acquireRedisKnowledgeProcessLease(ctx, redisClient, key)
}

func acquireMemoryKnowledgeProcessLease(ctx context.Context, key string) (*KnowledgeProcessLease, error) {
	raw, _ := globalMemProcessLeases.LoadOrStore(key, &keyedMutex{})
	km := raw.(*keyedMutex)
	if !km.mu.TryLock() {
		return nil, ErrKnowledgeProcessLeaseBusy
	}
	leaseCtx, cancel := context.WithCancel(ctx)
	var once sync.Once
	lease := &KnowledgeProcessLease{
		Context: leaseCtx,
		Cancel:  cancel,
		Key:     key,
		owned:   true,
	}
	release := func() {
		once.Do(func() {
			cancel()
			km.mu.Unlock()
		})
	}
	lease.Release = release
	lease.Context = withKnowledgeProcessLease(leaseCtx, lease)
	return lease, nil
}

func acquireRedisKnowledgeProcessLease(ctx context.Context, redisClient *redis.Client, key string) (*KnowledgeProcessLease, error) {
	token := uuid.NewString()
	acquired, err := redisClient.SetNX(ctx, key, token, knowledgeProcessLeaseTTL).Result()
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, ErrKnowledgeProcessLeaseBusy
	}

	leaseCtx, cancel := context.WithCancel(ctx)
	stop := make(chan struct{})
	var once sync.Once
	lease := &KnowledgeProcessLease{
		Context: leaseCtx,
		Cancel:  cancel,
		Token:   token,
		Key:     key,
		owned:   true,
	}
	go func() {
		ticker := time.NewTicker(knowledgeProcessLeaseRenew)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if ok := renewRedisKnowledgeProcessLease(context.Background(), redisClient, key, token); !ok {
					cancel()
					return
				}
			case <-stop:
				return
			}
		}
	}()

	release := func() {
		once.Do(func() {
			cancel()
			close(stop)
			releaseRedisKnowledgeProcessLease(context.Background(), redisClient, key, token)
		})
	}
	lease.Release = release
	lease.Context = withKnowledgeProcessLease(leaseCtx, lease)
	return lease, nil
}

func renewRedisKnowledgeProcessLease(ctx context.Context, redisClient *redis.Client, key, token string) bool {
	return renewRedisTokenLease(ctx, redisClient, key, token, knowledgeProcessLeaseTTL)
}

func renewRedisTokenLease(ctx context.Context, redisClient *redis.Client, key, token string, ttl time.Duration) bool {
	if redisClient == nil {
		return false
	}
	const script = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0`
	n, err := redisClient.Eval(ctx, script, []string{key}, token, int64(ttl/time.Millisecond)).Int()
	if err != nil {
		logger.Warnf(ctx, "redis token lease renew failed key=%s: %v", key, err)
		return false
	}
	return n == 1
}

func releaseRedisKnowledgeProcessLease(ctx context.Context, redisClient *redis.Client, key, token string) {
	releaseRedisTokenLease(ctx, redisClient, key, token)
}

func releaseRedisTokenLease(ctx context.Context, redisClient *redis.Client, key, token string) {
	if redisClient == nil {
		return
	}
	const script = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0`
	if err := redisClient.Eval(ctx, script, []string{key}, token).Err(); err != nil {
		logger.Warnf(ctx, "redis token lease release failed key=%s: %v", key, err)
	}
}

func (l *KnowledgeProcessLease) Err() error {
	if l == nil || l.Context == nil {
		return nil
	}
	if err := l.Context.Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrKnowledgeProcessLeaseLost, err)
	}
	return nil
}

func (l *KnowledgeProcessLease) ActiveContext(ctx context.Context) context.Context {
	if l == nil || l.Context == nil {
		return ctx
	}
	return l.Context
}
