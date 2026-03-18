package im

import (
	"sync"
	"time"
)

const (
	// rateLimitWindow is the sliding window duration for rate limiting.
	rateLimitWindow = 60 * time.Second
	// rateLimitMaxRequests is the maximum number of requests allowed per window per key.
	rateLimitMaxRequests = 10
	// rateLimitCleanupInterval is how often stale entries are purged.
	rateLimitCleanupInterval = 1 * time.Minute
)

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
