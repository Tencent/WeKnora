// Package limiter provides a distributed, per-key concurrency governor for
// outbound model-provider calls. The shared finite resource is the model
// provider (its request/concurrency budget), so concurrency is capped at the
// model-client layer — keyed by model ID — rather than at the asynq queue layer
// (queue weights are scheduling priority, not throttling).
//
// The Redis implementation is a self-healing distributed semaphore built on a
// sorted set: each held slot is a ZSET member (unique token) scored by its
// lease expiry. Acquire atomically prunes expired leases, counts live holders,
// and admits a new one only while under the limit. A background heartbeat
// refreshes the lease so long calls keep their slot; a crashed holder's lease
// simply expires and is reclaimed. Every backend error fails OPEN (the call is
// allowed) so a limiter/Redis outage can never halt model traffic.
package limiter

import (
	"context"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ModelConcurrencyLimiter caps the number of concurrent in-flight calls per
// key (typically a model ID) across all processes sharing the same backend.
type ModelConcurrencyLimiter interface {
	// Acquire blocks until a slot for key is available or ctx is done. It
	// returns a release func that MUST be invoked to free the slot. On any
	// backend error (or ctx cancellation) it fails open: release is a no-op and
	// err is nil, so callers proceed without a slot rather than dropping the
	// call.
	Acquire(ctx context.Context, key string, limit int) (release func(), err error)
}

// noop is the release returned on the fail-open / passthrough paths.
func noop() {}

const (
	// defaultLeaseTTL is how long a held slot survives without a heartbeat
	// before another acquirer may reclaim it. Sized well above a typical
	// enrichment LLM call so a slow provider never loses its slot mid-call;
	// the heartbeat refreshes it for genuinely long calls.
	defaultLeaseTTL = 5 * time.Minute
	// defaultPollInterval is how often a waiting acquirer re-checks for a free
	// slot. Small enough to stay responsive, large enough to avoid hammering
	// Redis under contention.
	defaultPollInterval = 200 * time.Millisecond
	// keyPrefix namespaces the semaphore ZSETs in Redis.
	keyPrefix = "weknora:modelsem:"
)

// acquireScript atomically prunes expired leases, counts live holders, and
// admits the caller (adding its token scored by lease expiry) only while the
// live count is below the limit. Returns 1 on admission, 0 when full.
//
//	KEYS[1] = semaphore ZSET key
//	ARGV[1] = now (unix ms)
//	ARGV[2] = limit
//	ARGV[3] = caller token
//	ARGV[4] = lease TTL (ms)
var acquireScript = redis.NewScript(`
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
local count = redis.call('ZCARD', KEYS[1])
if count < tonumber(ARGV[2]) then
    redis.call('ZADD', KEYS[1], ARGV[1] + ARGV[4], ARGV[3])
    redis.call('PEXPIRE', KEYS[1], ARGV[4] * 2)
    return 1
end
return 0
`)

type redisLimiter struct {
	rdb          *redis.Client
	ttl          time.Duration
	pollInterval time.Duration
}

// NewRedisLimiter builds a distributed limiter backed by rdb. A nil client
// yields a limiter that always fails open.
func NewRedisLimiter(rdb *redis.Client) ModelConcurrencyLimiter {
	return &redisLimiter{
		rdb:          rdb,
		ttl:          defaultLeaseTTL,
		pollInterval: defaultPollInterval,
	}
}

func (l *redisLimiter) Acquire(ctx context.Context, key string, limit int) (func(), error) {
	if l == nil || l.rdb == nil || limit <= 0 || key == "" {
		return noop, nil
	}

	zkey := keyPrefix + key
	token := uuid.NewString()
	ttlMs := l.ttl.Milliseconds()

	for {
		now := time.Now().UnixMilli()
		res, err := acquireScript.Run(ctx, l.rdb, []string{zkey},
			now, limit, token, ttlMs).Int()
		if err != nil {
			// Fail open: a limiter outage must never block model traffic.
			logger.Warnf(ctx, "[ModelLimiter] acquire failed for key=%s, failing open: %v", key, err)
			return noop, nil
		}
		if res == 1 {
			return l.hold(zkey, token), nil
		}

		select {
		case <-ctx.Done():
			// Fail open on cancellation too: let the inner call observe the
			// cancelled context and return its own error, rather than us
			// synthesising one here.
			return noop, nil
		case <-time.After(l.pollInterval):
		}
	}
}

// hold starts a heartbeat that refreshes the lease and returns an idempotent
// release that stops the heartbeat and drops the slot.
func (l *redisLimiter) hold(zkey, token string) func() {
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(l.ttl / 3)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				now := time.Now().UnixMilli()
				// Detached ctx: the heartbeat must outlive request ctx up to
				// release. Best-effort; a failed refresh just risks early
				// reclamation, which the limit already tolerates.
				_ = l.rdb.ZAdd(context.Background(), zkey, redis.Z{
					Score:  float64(now + l.ttl.Milliseconds()),
					Member: token,
				}).Err()
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			_ = l.rdb.ZRem(context.Background(), zkey, token).Err()
		})
	}
}
