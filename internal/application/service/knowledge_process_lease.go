package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/google/uuid"
)

var ErrKnowledgeProcessLeaseBusy = errors.New("knowledge process lease busy")

const (
	knowledgeProcessLeaseTTL   = 2 * time.Minute
	knowledgeProcessLeaseRenew = 30 * time.Second
)

type knowledgeProcessLeaseCtxKey struct{}

type keyedMutex struct {
	mu sync.Mutex
}

func knowledgeProcessLeaseKey(tenantID uint64, knowledgeID string) string {
	return fmt.Sprintf("knowledge-process:%d:%s", tenantID, knowledgeID)
}

func leaseHeldInCtx(ctx context.Context, key string) bool {
	held, _ := ctx.Value(knowledgeProcessLeaseCtxKey{}).(string)
	return held == key
}

func withKnowledgeProcessLease(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, knowledgeProcessLeaseCtxKey{}, key)
}

func (s *knowledgeService) acquireKnowledgeProcessLease(
	ctx context.Context,
	tenantID uint64,
	knowledgeID string,
) (context.Context, func(), error) {
	if tenantID == 0 || knowledgeID == "" {
		return ctx, func() {}, nil
	}
	key := knowledgeProcessLeaseKey(tenantID, knowledgeID)
	if leaseHeldInCtx(ctx, key) {
		return ctx, func() {}, nil
	}
	if s.redisClient == nil {
		return s.acquireMemoryKnowledgeProcessLease(ctx, key)
	}
	return s.acquireRedisKnowledgeProcessLease(ctx, key)
}

func (s *knowledgeService) acquireMemoryKnowledgeProcessLease(ctx context.Context, key string) (context.Context, func(), error) {
	raw, _ := s.memProcessLeases.LoadOrStore(key, &keyedMutex{})
	km := raw.(*keyedMutex)
	if !km.mu.TryLock() {
		return ctx, func() {}, ErrKnowledgeProcessLeaseBusy
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		km.mu.Unlock()
		s.memProcessLeases.Delete(key)
	}
	return withKnowledgeProcessLease(ctx, key), release, nil
}

func (s *knowledgeService) acquireRedisKnowledgeProcessLease(ctx context.Context, key string) (context.Context, func(), error) {
	token := uuid.NewString()
	acquired, err := s.redisClient.SetNX(ctx, key, token, knowledgeProcessLeaseTTL).Result()
	if err != nil {
		return ctx, func() {}, err
	}
	if !acquired {
		return ctx, func() {}, ErrKnowledgeProcessLeaseBusy
	}

	stop := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(knowledgeProcessLeaseRenew)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.renewRedisKnowledgeProcessLease(context.Background(), key, token)
			case <-stop:
				return
			}
		}
	}()

	release := func() {
		once.Do(func() {
			close(stop)
			s.releaseRedisKnowledgeProcessLease(context.Background(), key, token)
		})
	}
	return withKnowledgeProcessLease(ctx, key), release, nil
}

func (s *knowledgeService) renewRedisKnowledgeProcessLease(ctx context.Context, key, token string) {
	if s.redisClient == nil {
		return
	}
	const script = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0`
	if err := s.redisClient.Eval(ctx, script, []string{key}, token, int64(knowledgeProcessLeaseTTL/time.Millisecond)).Err(); err != nil {
		logger.Warnf(ctx, "knowledge process lease renew failed key=%s: %v", key, err)
	}
}

func (s *knowledgeService) releaseRedisKnowledgeProcessLease(ctx context.Context, key, token string) {
	if s.redisClient == nil {
		return
	}
	const script = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0`
	if err := s.redisClient.Eval(ctx, script, []string{key}, token).Err(); err != nil {
		logger.Warnf(ctx, "knowledge process lease release failed key=%s: %v", key, err)
	}
}
