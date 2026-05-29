package middleware

import (
	"net/http"
	"sync"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/gin-gonic/gin"
)

// auth_public_ratelimit.go — sliding-window IP rate limiter for the
// unauthenticated share-link endpoints (/auth/invitations/lookup and
// /auth/register-by-invite). Both surfaces accept a plaintext token
// from the request and either reveal tenant context (lookup) or
// create an account (register-by-invite); without a limiter an
// attacker can brute-force token guesses and hammer registration.
//
// The token is 256-bit so guessing the space is infeasible regardless,
// but the limiter still narrows the abuse window for partial-token
// leaks (e.g. via referrer / clipboard managers / accidental commits)
// and bounds the noise this endpoint can add to the user-create path.
//
// Local in-memory only — fine for typical deployments since both
// endpoints handle low absolute volumes; if/when WeKnora horizontally
// scales the auth surface, swap to the Redis-backed limiter in
// internal/im/ratelimit.go (kept lowercase / package-private today).

// publicAuthRateLimitWindow is the rolling window length per IP.
const publicAuthRateLimitWindow = 60 * time.Second

// publicAuthRateLimitMax is the request budget per IP per window for
// each endpoint instance. 30/min comfortably covers a real user
// retrying a failed registration while clamping enumeration
// throughput to ~half a request per second.
const publicAuthRateLimitMax = 30

// publicAuthRateLimitCleanupInterval bounds map growth from one-off
// IPs that never come back.
const publicAuthRateLimitCleanupInterval = 2 * time.Minute

type ipBucket struct {
	mu         sync.Mutex
	timestamps []time.Time
}

type ipRateLimiter struct {
	window  time.Duration
	max     int
	buckets sync.Map // string (IP) -> *ipBucket
}

func newIPRateLimiter(window time.Duration, max int) *ipRateLimiter {
	l := &ipRateLimiter{window: window, max: max}
	go l.cleanupLoop()
	return l
}

// allow returns true if the IP is within budget for the current
// window. Empty IP (proxy stripped X-Forwarded-For) is treated as a
// shared bucket — slightly over-restrictive vs. dropping the limit
// entirely, which would let a misconfigured proxy bypass the gate.
func (l *ipRateLimiter) allow(ip string) bool {
	if ip == "" {
		ip = "_unknown_"
	}
	now := time.Now()
	cutoff := now.Add(-l.window)

	val, _ := l.buckets.LoadOrStore(ip, &ipBucket{})
	b := val.(*ipBucket)
	b.mu.Lock()
	defer b.mu.Unlock()

	kept := b.timestamps[:0]
	for _, t := range b.timestamps {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	b.timestamps = kept
	if len(b.timestamps) >= l.max {
		return false
	}
	b.timestamps = append(b.timestamps, now)
	return true
}

func (l *ipRateLimiter) cleanupLoop() {
	t := time.NewTicker(publicAuthRateLimitCleanupInterval)
	defer t.Stop()
	for range t.C {
		cutoff := time.Now().Add(-l.window)
		l.buckets.Range(func(k, v any) bool {
			b := v.(*ipBucket)
			b.mu.Lock()
			drop := len(b.timestamps) == 0 ||
				b.timestamps[len(b.timestamps)-1].Before(cutoff)
			b.mu.Unlock()
			if drop {
				l.buckets.Delete(k)
			}
			return true
		})
	}
}

// publicAuthLimiter — package-singleton so each route registration
// shares a single bucket map per process. Each route still gets its
// own Gin handler that calls into this same limiter; per-route
// isolation isn't important here (real users don't hit both
// endpoints in the same window) but the shared state makes total
// budget per IP intuitive: 30/min across all share-link surfaces.
var publicAuthLimiter = newIPRateLimiter(
	publicAuthRateLimitWindow, publicAuthRateLimitMax)

// PublicAuthRateLimit returns a Gin middleware that rate-limits the
// unauthenticated share-link endpoints by client IP. 429 is mapped
// through the project's AppError type so it flows through the same
// error middleware as the rest of the auth surface.
func PublicAuthRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		if !publicAuthLimiter.allow(ip) {
			c.Error(&apperrors.AppError{
				Code:     apperrors.ErrTooManyRequests,
				Message:  "too many requests; please retry shortly",
				HTTPCode: http.StatusTooManyRequests,
			})
			c.Abort()
			return
		}
		c.Next()
	}
}
