package limiter

import (
	"context"
	"sync"
	"time"
)

// localRateLimiter is an in-process token-bucket rate limiter keyed by an
// arbitrary string. It is the Lite-mode counterpart to redisRateLimiter: Lite
// runs a single process with no Redis, so a shared distributed bucket is
// neither available nor needed — but background ingestion can still exceed a
// provider's RPM/TPM, so we still enforce the budget locally.
type localRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*localBucket
}

type localBucket struct {
	tokens float64
	last   time.Time
}

// NewLocalRateLimiter builds an in-process per-key token-bucket rate limiter.
func NewLocalRateLimiter() RateLimiter {
	return &localRateLimiter{buckets: make(map[string]*localBucket)}
}

// refillLocked advances the bucket to now, capping at capacity. Caller holds mu.
func (b *localBucket) refill(now time.Time, rate float64, capacity int) {
	elapsed := now.Sub(b.last).Milliseconds()
	if elapsed < 0 {
		elapsed = 0
	}
	b.tokens += float64(elapsed) * rate
	if b.tokens > float64(capacity) {
		b.tokens = float64(capacity)
	}
	b.last = now
}

func (l *localRateLimiter) bucket(key string, now time.Time, capacity int) *localBucket {
	b, ok := l.buckets[key]
	if !ok {
		b = &localBucket{tokens: float64(capacity), last: now}
		l.buckets[key] = b
	}
	return b
}

func (l *localRateLimiter) Wait(ctx context.Context, key string, ratePerMin, capacity, tokens int) error {
	if l == nil || ratePerMin <= 0 || capacity <= 0 || tokens <= 0 || key == "" {
		return nil
	}
	if tokens > capacity {
		tokens = capacity
	}
	rate := ratePerMs(ratePerMin)

	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	for {
		now := time.Now()
		l.mu.Lock()
		b := l.bucket(key, now, capacity)
		b.refill(now, rate, capacity)
		if b.tokens >= float64(tokens) {
			b.tokens -= float64(tokens)
			l.mu.Unlock()
			return nil
		}
		deficit := float64(tokens) - b.tokens
		l.mu.Unlock()

		wait := time.Duration(deficit/rate) * time.Millisecond
		if wait <= 0 || wait > rateMaxPoll {
			wait = rateMaxPoll
		}
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}

func (l *localRateLimiter) Adjust(ctx context.Context, key string, ratePerMin, capacity, delta int) {
	if l == nil || ratePerMin <= 0 || capacity <= 0 || delta == 0 || key == "" {
		return
	}
	rate := ratePerMs(ratePerMin)
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		return
	}
	b.refill(now, rate, capacity)
	b.tokens += float64(delta)
	if b.tokens > float64(capacity) {
		b.tokens = float64(capacity)
	}
}
