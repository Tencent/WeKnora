package embedding

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/common/redislock"
	"github.com/redis/go-redis/v9"
)

const (
	defaultEmbeddingCacheLockLease   = 30 * time.Second
	defaultEmbeddingCacheLockWait    = 2 * time.Second
	defaultEmbeddingCacheLockRenewal = 10 * time.Second
)

var (
	errEmbeddingLockWaitTimeout = errors.New("embedding cache distributed lock wait timed out")
	errEmbeddingLockUnavailable = errors.New("embedding cache distributed lock unavailable")
)

type redisLockProvider interface {
	lockClient() redis.UniversalClient
}

func (r *redisResultCache) lockClient() redis.UniversalClient {
	if r == nil {
		return nil
	}
	return r.client
}

func embeddingDistributedLockEnabled() bool {
	value := strings.TrimSpace(os.Getenv("WEKNORA_EMBEDDING_CACHE_DISTRIBUTED_LOCK_ENABLED"))
	if value == "" {
		return true
	}
	enabled, err := strconv.ParseBool(value)
	return err == nil && enabled
}

func resolveEmbeddingCacheLockDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}

func embeddingCacheLockSettings() (lease, wait, renewal time.Duration) {
	lease = resolveEmbeddingCacheLockDuration("WEKNORA_EMBEDDING_CACHE_LOCK_LEASE", defaultEmbeddingCacheLockLease)
	wait = resolveEmbeddingCacheLockDuration("WEKNORA_EMBEDDING_CACHE_LOCK_WAIT", defaultEmbeddingCacheLockWait)
	renewal = resolveEmbeddingCacheLockDuration(
		"WEKNORA_EMBEDDING_CACHE_LOCK_RENEWAL", defaultEmbeddingCacheLockRenewal)
	if renewal >= lease {
		renewal = lease / 3
		if renewal <= 0 {
			renewal = time.Millisecond
		}
	}
	return lease, wait, renewal
}

func cacheLockClient(cache ResultCache) redis.UniversalClient {
	provider, ok := cache.(redisLockProvider)
	if !ok {
		return nil
	}
	return provider.lockClient()
}

// withEmbeddingRedisLock waits for a token-owned lock for a bounded period.
// Once acquired, the callback receives a context canceled if ownership is
// lost. The caller can distinguish a lock infrastructure failure from a
// callback/provider failure and choose to fail open.
func withEmbeddingRedisLock(
	ctx context.Context,
	client redis.UniversalClient,
	key string,
	lease time.Duration,
	wait time.Duration,
	renewal time.Duration,
	fn func(context.Context) (interface{}, error),
) (interface{}, error) {
	if client == nil {
		return nil, fmt.Errorf("%w: redis client is nil", errEmbeddingLockUnavailable)
	}
	token, err := redislock.NewToken()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errEmbeddingLockUnavailable, err)
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, wait)
	defer cancelWait()
	lockKey := key + ":lock"
	for {
		acquired, acquireErr := redislock.TryAcquire(waitCtx, client, lockKey, token, lease)
		if acquireErr != nil {
			return nil, fmt.Errorf("%w: %v", errEmbeddingLockUnavailable, acquireErr)
		}
		if acquired {
			break
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-waitCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, fmt.Errorf("%w: %v", errEmbeddingLockWaitTimeout, waitCtx.Err())
		case <-timer.C:
		}
	}

	ownershipCtx, cancelOwnership := context.WithCancelCause(ctx)
	renewCtx, stopRenewal := context.WithCancel(context.WithoutCancel(ctx))
	renewErr := make(chan error, 1)
	go renewEmbeddingLock(renewCtx, cancelOwnership, client, lockKey, token, lease, renewal, renewErr)

	result, callbackErr := fn(ownershipCtx)
	stopRenewal()
	// Join the renewal worker before releasing its token-owned lock.
	<-renewErr
	cancelOwnership(nil)

	releaseCtx, cancelRelease := context.WithTimeout(context.Background(), 5*time.Second)
	_, _ = redislock.Release(releaseCtx, client, lockKey, token)
	cancelRelease()
	// Lock maintenance must never replace the provider's result or error.
	return result, callbackErr
}

func renewEmbeddingLock(
	ctx context.Context,
	cancelOwnership context.CancelCauseFunc,
	client redis.UniversalClient,
	key string,
	token string,
	lease time.Duration,
	renewal time.Duration,
	result chan<- error,
) {
	ticker := time.NewTicker(renewal)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			result <- nil
			return
		case <-ticker.C:
			renewed, err := redislock.Renew(ctx, client, key, token, lease)
			if err != nil || !renewed {
				if err == nil {
					err = redislock.ErrLockOwnershipLost
				}
				ownershipErr := fmt.Errorf("%w for %q: %v", redislock.ErrLockOwnershipLost, key, err)
				cancelOwnership(ownershipErr)
				result <- ownershipErr
				return
			}
		}
	}
}
