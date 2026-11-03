package limiter

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/Tencent/WeKnora/internal/logger"
)

// RateLimit is a per-model request-rate budget. It is the second dimension of
// provider governance: the concurrency semaphore caps how many calls are in
// flight, while this caps how many start per second.
//
// The two answer different provider limits. A concurrency cap cannot hold a
// burst down: when a provider rejects calls quickly with 429, the slots turn
// over just as quickly and the effective request rate is unbounded. Providers
// that publish a requests-per-second (or requests-per-minute) account quota
// therefore have to be paced, not just bounded in flight.
//
// RPS <= 0 disables rate limiting (the default: behaviour is unchanged).
// Burst <= 0 with RPS > 0 is coerced to 1 so a misconfigured row degrades to
// strict pacing instead of panicking at limiter construction.
type RateLimit struct {
	RPS   float64
	Burst int
}

// Enabled reports whether this budget asks for pacing.
func (r RateLimit) Enabled() bool { return r.RPS > 0 }

func (r RateLimit) burst() int {
	if r.Burst < 1 {
		return 1
	}
	return r.Burst
}

type rateBucket struct {
	limiter *rate.Limiter
	rps     float64
	burst   int
	name    atomic.Value // string
	waiting atomic.Int64
	// delayed counts calls that had to wait for a token. A non-zero value
	// with 429s in the logs means the budget is realistic; zero means it has
	// never been the binding constraint.
	delayed atomic.Int64
}

type rateRegistry struct {
	mu      sync.Mutex
	buckets map[string]*rateBucket
}

// rateGovernor is process-local by design. A distributed bucket would have to
// be atomic per request to be useful, and the provider quota is an account
// fact that every replica already shares through the provider itself; for N
// replicas the operator sets RPS to quota/N. Keeping this in-process also
// means a limiter outage cannot delay model traffic, matching the fail-open
// contract of the concurrency limiter.
var rateGovernor = &rateRegistry{buckets: make(map[string]*rateBucket)}

func resetRateGovernor() {
	rateGovernor.mu.Lock()
	rateGovernor.buckets = make(map[string]*rateBucket)
	rateGovernor.mu.Unlock()
}

// bucketFor returns the token bucket for a model, creating it or retuning it
// when the operator changes max_rps / max_burst without a restart. Callers
// hold no lock.
func bucketFor(modelID, modelName string, rl RateLimit) *rateBucket {
	burst := rl.burst()

	rateGovernor.mu.Lock()
	defer rateGovernor.mu.Unlock()

	bucket := rateGovernor.buckets[modelID]
	if bucket == nil {
		bucket = &rateBucket{
			limiter: rate.NewLimiter(rate.Limit(rl.RPS), burst),
			rps:     rl.RPS,
			burst:   burst,
		}
		rateGovernor.buckets[modelID] = bucket
	} else if bucket.rps != rl.RPS || bucket.burst != burst {
		bucket.limiter.SetLimit(rate.Limit(rl.RPS))
		bucket.limiter.SetBurst(burst)
		bucket.rps = rl.RPS
		bucket.burst = burst
	}
	if modelName != "" {
		bucket.name.Store(modelName)
	}
	return bucket
}

// WaitRate blocks until the model's rate budget admits one call, and returns
// immediately when pacing is disabled. It never fails: a cancelled context, a
// deadline shorter than the required wait, or an internal error all let the
// call through, because a limiter problem must not drop model traffic. The
// provider request that follows will observe the cancelled context itself.
//
// Interactive and background calls both pay this wait. Unlike the concurrency
// semaphore — which is deliberately limited to background work so user-facing
// latency is never queued behind ingestion — a per-second quota is spent by
// every request, so counting only part of the traffic would defeat the point.
func WaitRate(ctx context.Context, modelID, modelName string, rl RateLimit) {
	if !rl.Enabled() || modelID == "" {
		return
	}

	bucket := bucketFor(modelID, modelName, rl)

	reservation := bucket.limiter.ReserveN(time.Now(), 1)
	if !reservation.OK() {
		return
	}
	delay := reservation.Delay()
	if deadline, ok := ctx.Deadline(); ok && time.Now().Add(delay).After(deadline) {
		// The call cannot be admitted before its own deadline; do not hold a
		// reservation it will never use.
		reservation.Cancel()
		logger.Debugf(ctx, "[limiter] rate wait for model %s exceeds deadline, passing through", modelID)
		return
	}
	if delay <= 0 {
		return
	}

	bucket.waiting.Add(1)
	defer bucket.waiting.Add(-1)
	bucket.delayed.Add(1)

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		reservation.Cancel()
		logger.Debugf(ctx, "[limiter] rate wait cancelled for model %s: %v", modelID, ctx.Err())
	}
}

// RateStat is a point-in-time view of one model's request-rate budget.
// Waiting and Delayed are process-local, matching the in-process bucket.
type RateStat struct {
	ModelID string  `json:"model_id"`
	Name    string  `json:"name"`
	RPS     float64 `json:"rps"`
	Burst   int     `json:"burst"`
	Waiting int64   `json:"waiting"`
	Delayed int64   `json:"delayed"`
}

// RateStats returns the rate budgets this process has observed, sorted by
// model ID so the API response is stable.
func RateStats() []RateStat {
	rateGovernor.mu.Lock()
	defer rateGovernor.mu.Unlock()

	stats := make([]RateStat, 0, len(rateGovernor.buckets))
	for modelID, bucket := range rateGovernor.buckets {
		name, _ := bucket.name.Load().(string)
		stats = append(stats, RateStat{
			ModelID: modelID,
			Name:    name,
			RPS:     bucket.rps,
			Burst:   bucket.burst,
			Waiting: bucket.waiting.Load(),
			Delayed: bucket.delayed.Load(),
		})
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].ModelID < stats[j].ModelID })
	return stats
}
