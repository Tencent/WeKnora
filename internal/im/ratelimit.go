package im

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// rateLimitWindow is the sliding window duration for rate limiting.
	rateLimitWindow = 60 * time.Second
	// rateLimitMaxRequests is the maximum number of requests allowed per window per key.
	rateLimitMaxRequests = 10
	// rateLimitCleanupInterval is how often stale entries are purged.
	rateLimitCleanupInterval = 1 * time.Minute
)

// ──────────────────────────────────────────────────────────────────────────────
// distributedLimiter: Redis ZSET + local fallback
// ──────────────────────────────────────────────────────────────────────────────

// rateLimitScript is an atomic Lua script that implements a sliding-window rate
// limiter on a Redis Sorted Set. It prunes expired entries, checks the count,
// and conditionally adds a new member — all in a single round-trip.
//
// KEYS[1] = the rate-limit key
// ARGV[1] = now (Unix milliseconds)
// ARGV[2] = window size (milliseconds)
// ARGV[3] = max allowed requests
// ARGV[4] = unique member value (e.g. now_ms as string)
//
// Returns 1 if the request is allowed, 0 if rate-limited.
var rateLimitScript = redis.NewScript(`
local key     = KEYS[1]
local now     = tonumber(ARGV[1])
local window  = tonumber(ARGV[2])
local maxReq  = tonumber(ARGV[3])
local member  = ARGV[4]

redis.call('ZREMRANGEBYSCORE', key, 0, now - window)
local count = redis.call('ZCARD', key)
if count < maxReq then
    redis.call('ZADD', key, now, member)
    redis.call('PEXPIRE', key, window + 1000)
    return 1
end
return 0
`)

// distributedLimiter tries Redis first, falls back to a local sliding-window
// limiter when Redis is unavailable (nil client or transient error).
type distributedLimiter struct {
	redisClient *redis.Client
	local       *slidingWindowLimiter
	window      time.Duration
	maxRequests int
	instanceID  string // used to disambiguate ZSET members across instances
}

func newDistributedLimiter(redisClient *redis.Client, window time.Duration, maxRequests int, instanceID string) *distributedLimiter {
	return &distributedLimiter{
		redisClient: redisClient,
		local:       newSlidingWindowLimiter(window, maxRequests),
		window:      window,
		maxRequests: maxRequests,
		instanceID:  instanceID,
	}
}

// Allow returns true if the request for the given key is within the rate limit.
func (d *distributedLimiter) Allow(key string) bool {
	if d.redisClient != nil {
		allowed, err := d.redisAllow(context.Background(), key)
		if err == nil {
			return allowed
		}
		// Redis failed — fall through to local limiter.
	}
	return d.local.Allow(key)
}

func (d *distributedLimiter) redisAllow(ctx context.Context, key string) (bool, error) {
	redisKey := RedisKeyRateLimit + key
	nowMs := time.Now().UnixMilli()
	windowMs := d.window.Milliseconds()
	member := fmt.Sprintf("%s:%d", d.instanceID, nowMs) // instanceID prevents ZSET member collision across instances

	result, err := rateLimitScript.Run(ctx, d.redisClient,
		[]string{redisKey},
		nowMs, windowMs, d.maxRequests, member,
	).Int64()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

// cleanupLoop delegates to the local limiter's cleanup for the fallback path.
func (d *distributedLimiter) cleanupLoop(stopCh <-chan struct{}) {
	d.local.cleanupLoop(stopCh)
}

// ──────────────────────────────────────────────────────────────────────────────
// slidingWindowLimiter: local in-memory fallback (original implementation)
// ──────────────────────────────────────────────────────────────────────────────

// rateLimitEntry holds the request timestamps for a single key.
type rateLimitEntry struct {
	mu         sync.Mutex
	timestamps []time.Time
	deleted    bool // marked true when removed from the map by cleanupLoop
}

// slidingWindowLimiter implements per-key sliding window rate limiting.
type slidingWindowLimiter struct {
	window      time.Duration
	maxRequests int
	entries     sync.Map // key -> *rateLimitEntry
}

func newSlidingWindowLimiter(window time.Duration, maxRequests int) *slidingWindowLimiter {
	return &slidingWindowLimiter{
		window:      window,
		maxRequests: maxRequests,
	}
}

// Allow checks if the request for the given key is within the rate limit.
// Returns true if allowed, false if rate limited.
func (l *slidingWindowLimiter) Allow(key string) bool {
	now := time.Now()
	cutoff := now.Add(-l.window)

	for {
		val, _ := l.entries.LoadOrStore(key, &rateLimitEntry{})
		entry := val.(*rateLimitEntry)

		entry.mu.Lock()
		// If the entry was concurrently deleted by cleanupLoop, retry with a fresh one.
		if entry.deleted {
			entry.mu.Unlock()
			l.entries.Delete(key) // ensure stale entry is gone
			continue
		}

		// Remove expired timestamps
		valid := entry.timestamps[:0]
		for _, t := range entry.timestamps {
			if t.After(cutoff) {
				valid = append(valid, t)
			}
		}
		entry.timestamps = valid

		if len(entry.timestamps) >= l.maxRequests {
			entry.mu.Unlock()
			return false
		}

		entry.timestamps = append(entry.timestamps, now)
		entry.mu.Unlock()
		return true
	}
}

// cleanupLoop periodically removes stale entries from the limiter.
func (l *slidingWindowLimiter) cleanupLoop(stopCh <-chan struct{}) {
	ticker := time.NewTicker(rateLimitCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-l.window)
			l.entries.Range(func(key, val interface{}) bool {
				entry := val.(*rateLimitEntry)
				entry.mu.Lock()
				allExpired := true
				for _, t := range entry.timestamps {
					if t.After(cutoff) {
						allExpired = false
						break
					}
				}
				if allExpired {
					entry.deleted = true
					l.entries.Delete(key)
				}
				entry.mu.Unlock()
				return true
			})
		case <-stopCh:
			return
		}
	}
}
