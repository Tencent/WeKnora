// Package ratelimit provides a Redis-backed sliding-window rate limiter with a
// local in-memory fallback when Redis is unavailable (Lite / single-instance).
package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const localCleanupInterval = time.Minute

// rateLimitScript atomically prunes expired ZSET members, checks the count,
// and conditionally records a new hit.
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

// Limiter enforces per-key sliding-window limits. max is evaluated per Allow
// call so callers (e.g. embed channels) can vary budgets without rebuilding
// the limiter.
type Limiter struct {
	redis      *redis.Client
	local      *localLimiter
	keyPrefix  string
	window     time.Duration
	instanceID string
}

// New constructs a limiter. keyPrefix should include a trailing delimiter
// (e.g. "embed:ratelimit:"). When redis is nil, only the local fallback runs.
func New(redisClient *redis.Client, keyPrefix string, window time.Duration, instanceID string) *Limiter {
	if window <= 0 {
		window = time.Minute
	}
	if instanceID == "" {
		instanceID = uuid.New().String()
	}
	return &Limiter{
		redis:      redisClient,
		local:      newLocalLimiter(),
		keyPrefix:  keyPrefix,
		window:     window,
		instanceID: instanceID,
	}
}

// Allow reports whether key is within budget for the current window.
func (l *Limiter) Allow(ctx context.Context, key string, max int) bool {
	if max <= 0 {
		return true
	}
	if l.redis != nil {
		allowed, err := l.redisAllow(ctx, key, max)
		if err == nil {
			return allowed
		}
	}
	return l.local.allow(key, l.window, max)
}

func (l *Limiter) redisAllow(ctx context.Context, key string, max int) (bool, error) {
	redisKey := l.keyPrefix + key
	nowMs := time.Now().UnixMilli()
	windowMs := l.window.Milliseconds()
	member := fmt.Sprintf("%s:%d", l.instanceID, nowMs)

	result, err := rateLimitScript.Run(ctx, l.redis,
		[]string{redisKey},
		nowMs, windowMs, max, member,
	).Int64()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

// StartCleanup runs periodic eviction for the local fallback map. No-op when
// only Redis is in use, but cheap to call either way.
func (l *Limiter) StartCleanup(stopCh <-chan struct{}) {
	l.local.startCleanup(l.window, stopCh)
}

type localEntry struct {
	mu         sync.Mutex
	timestamps []time.Time
	deleted    bool
}

type localLimiter struct {
	entries sync.Map // key -> *localEntry
}

func newLocalLimiter() *localLimiter {
	return &localLimiter{}
}

func (l *localLimiter) allow(key string, window time.Duration, max int) bool {
	now := time.Now()
	cutoff := now.Add(-window)

	for {
		val, _ := l.entries.LoadOrStore(key, &localEntry{})
		entry := val.(*localEntry)

		entry.mu.Lock()
		if entry.deleted {
			entry.mu.Unlock()
			l.entries.Delete(key)
			continue
		}

		valid := entry.timestamps[:0]
		for _, t := range entry.timestamps {
			if t.After(cutoff) {
				valid = append(valid, t)
			}
		}
		entry.timestamps = valid

		if len(entry.timestamps) >= max {
			entry.mu.Unlock()
			return false
		}
		entry.timestamps = append(entry.timestamps, now)
		entry.mu.Unlock()
		return true
	}
}

func (l *localLimiter) startCleanup(window time.Duration, stopCh <-chan struct{}) {
	if window <= 0 {
		window = time.Minute
	}
	ticker := time.NewTicker(localCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			// Evict against the limiter's own window, not a hardcoded minute,
			// so non-minute limiters (e.g. a per-day cap) are not dropped early.
			cutoff := time.Now().Add(-window)
			l.entries.Range(func(key, val any) bool {
				entry := val.(*localEntry)
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
