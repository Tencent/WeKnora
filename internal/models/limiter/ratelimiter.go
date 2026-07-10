package limiter

import (
	"context"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/redis/go-redis/v9"
)

// RateLimiter enforces a per-minute budget (RPM or TPM) via a token bucket
// keyed by an arbitrary string (typically "rpm:<modelID>" / "tpm:<modelID>").
// Unlike the concurrency semaphore, a rate budget is consumed over time and
// refills continuously, so there is no per-call "slot" to release — only an
// optional Adjust to reconcile an over- or under-estimated reservation.
//
// Every backend error (or ctx cancellation) fails OPEN: Wait returns nil so a
// limiter/Redis outage can never halt model traffic, mirroring the concurrency
// limiter's philosophy.
type RateLimiter interface {
	// Wait blocks until `tokens` units are available in the bucket for key
	// under the given ratePerMin, then consumes them. capacity bounds the
	// bucket (burst ceiling). A request for more than capacity is capped to
	// capacity so a single oversized call can never deadlock. Fails open.
	Wait(ctx context.Context, key string, ratePerMin, capacity, tokens int) error
	// Adjust reconciles a prior reservation: delta>0 returns unused tokens to
	// the bucket (capped at capacity), delta<0 debits extra tokens (the bucket
	// may go negative, throttling subsequent calls). Best-effort, never blocks.
	Adjust(ctx context.Context, key string, ratePerMin, capacity, delta int)
}

const (
	// rateWindow is the budget window: RPM / TPM are "per minute".
	rateWindow = time.Minute
	// rateKeyPrefix namespaces the token-bucket hashes in Redis.
	rateKeyPrefix = "weknora:modelrate:"
	// rateBucketTTL is how long an idle bucket survives before Redis drops it
	// (after which it re-initialises full). Comfortably above the window so a
	// steadily-used bucket never expires mid-flight.
	rateBucketTTL = 2 * rateWindow
	// rateMaxPoll bounds a single sleep between bucket re-checks so a wildly
	// wrong server-computed wait can't park a goroutine for minutes.
	rateMaxPoll = 2 * time.Second
)

// consumeScript refills the bucket by elapsed*rate (capped at capacity) then,
// if enough tokens are present, consumes `want` and returns -1 (granted).
// Otherwise it persists the refill WITHOUT consuming and returns the estimated
// wait in ms until `want` tokens would be available.
//
//	KEYS[1] = bucket hash key
//	ARGV[1] = now (unix ms)
//	ARGV[2] = rate (tokens per ms, float)
//	ARGV[3] = capacity
//	ARGV[4] = want
//	ARGV[5] = ttl (ms)
var consumeScript = redis.NewScript(`
local now  = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local cap  = tonumber(ARGV[3])
local want = tonumber(ARGV[4])
local ttl  = tonumber(ARGV[5])
local data = redis.call('HMGET', KEYS[1], 't', 'ts')
local tokens = tonumber(data[1])
local ts = tonumber(data[2])
if tokens == nil then tokens = cap; ts = now end
local elapsed = now - ts
if elapsed < 0 then elapsed = 0 end
tokens = math.min(cap, tokens + elapsed * rate)
if tokens >= want then
    tokens = tokens - want
    redis.call('HMSET', KEYS[1], 't', tokens, 'ts', now)
    redis.call('PEXPIRE', KEYS[1], ttl)
    return -1
end
redis.call('HMSET', KEYS[1], 't', tokens, 'ts', now)
redis.call('PEXPIRE', KEYS[1], ttl)
local deficit = want - tokens
local wait = math.ceil(deficit / rate)
return wait
`)

// adjustScript refills then applies delta (clamped at capacity on the high
// side; may go negative on the low side to throttle later calls). Returns 1.
//
//	KEYS[1] = bucket hash key
//	ARGV[1] = now (unix ms)
//	ARGV[2] = rate (tokens per ms, float)
//	ARGV[3] = capacity
//	ARGV[4] = delta
//	ARGV[5] = ttl (ms)
var adjustScript = redis.NewScript(`
local now  = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local cap  = tonumber(ARGV[3])
local delta= tonumber(ARGV[4])
local ttl  = tonumber(ARGV[5])
local data = redis.call('HMGET', KEYS[1], 't', 'ts')
local tokens = tonumber(data[1])
local ts = tonumber(data[2])
if tokens == nil then return 0 end
local elapsed = now - ts
if elapsed < 0 then elapsed = 0 end
tokens = math.min(cap, tokens + elapsed * rate)
tokens = tokens + delta
if tokens > cap then tokens = cap end
redis.call('HMSET', KEYS[1], 't', tokens, 'ts', now)
redis.call('PEXPIRE', KEYS[1], ttl)
return 1
`)

type redisRateLimiter struct {
	rdb *redis.Client
}

// NewRedisRateLimiter builds a distributed token-bucket rate limiter backed by
// rdb. A nil client yields a limiter that always fails open.
func NewRedisRateLimiter(rdb *redis.Client) RateLimiter {
	return &redisRateLimiter{rdb: rdb}
}

// ratePerMs converts a per-minute budget to tokens-per-millisecond.
func ratePerMs(ratePerMin int) float64 {
	return float64(ratePerMin) / float64(rateWindow.Milliseconds())
}

func (l *redisRateLimiter) Wait(ctx context.Context, key string, ratePerMin, capacity, tokens int) error {
	if l == nil || l.rdb == nil || ratePerMin <= 0 || capacity <= 0 || tokens <= 0 || key == "" {
		return nil
	}
	// A single request larger than the whole bucket can never be satisfied by
	// waiting; cap it so we admit after at most one full-bucket refill instead
	// of parking forever.
	if tokens > capacity {
		tokens = capacity
	}
	rate := ratePerMs(ratePerMin)
	rkey := rateKeyPrefix + key
	ttl := rateBucketTTL.Milliseconds()

	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	for {
		now := time.Now().UnixMilli()
		res, err := consumeScript.Run(ctx, l.rdb, []string{rkey},
			now, strconv.FormatFloat(rate, 'f', -1, 64), capacity, tokens, ttl).Int64()
		if err != nil {
			logger.Warnf(ctx, "[ModelRateLimiter] consume failed for key=%s, failing open: %v", key, err)
			return nil
		}
		if res < 0 {
			return nil // granted
		}
		wait := time.Duration(res) * time.Millisecond
		if wait <= 0 || wait > rateMaxPoll {
			wait = rateMaxPoll
		}
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			// Fail open on cancellation: let the inner call observe the
			// cancelled context and surface its own error.
			return nil
		case <-timer.C:
		}
	}
}

func (l *redisRateLimiter) Adjust(ctx context.Context, key string, ratePerMin, capacity, delta int) {
	if l == nil || l.rdb == nil || ratePerMin <= 0 || capacity <= 0 || delta == 0 || key == "" {
		return
	}
	rate := ratePerMs(ratePerMin)
	rkey := rateKeyPrefix + key
	ttl := rateBucketTTL.Milliseconds()
	if err := adjustScript.Run(context.Background(), l.rdb, []string{rkey},
		time.Now().UnixMilli(), strconv.FormatFloat(rate, 'f', -1, 64), capacity, delta, ttl).Err(); err != nil {
		logger.Warnf(ctx, "[ModelRateLimiter] adjust failed for key=%s (ignored): %v", key, err)
	}
}
