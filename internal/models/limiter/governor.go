package limiter

import (
	"context"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
)

// Limits is the per-model background throttling budget across three orthogonal
// dimensions. A zero value in any field means "fall back to the process-wide
// default for that dimension" (see the governor's default Limits); a default
// that is itself <= 0 disables that dimension entirely.
//
//   - Concurrency: max in-flight calls (semaphore). Reacts to slow providers.
//   - RPM:         max requests per minute (token bucket). Aligns with the
//     provider's requests-per-minute quota.
//   - TPM:         max tokens per minute (token bucket). Aligns with the
//     provider's tokens-per-minute quota — the limit LLM/embedding workloads
//     hit first on large documents.
//
// The three are AND-combined: a call proceeds only once it holds a slot in
// every enabled dimension. Every dimension fails open independently.
type Limits struct {
	Concurrency int
	RPM         int
	TPM         int
}

// The concurrency governor is process-wide, shared by every model-client layer
// that fronts a provider (chat, vlm, embedding). Keeping the singleton here —
// rather than inside one client package — lets all of them gate against the
// same limiters and per-model budget without importing each other. Wired once
// at startup (see container.registerModelConcurrencyLimiter) via SetGovernor.
var (
	governorMu  sync.RWMutex
	concLimiter ModelConcurrencyLimiter
	rateLimiter RateLimiter
	defaults    Limits
)

// SetGovernor installs the process-wide background concurrency + rate limiters
// and the default per-model limits. A nil limiter (or a non-positive default in
// a given dimension) disables that dimension. Safe to call at startup.
func SetGovernor(conc ModelConcurrencyLimiter, rate RateLimiter, def Limits) {
	governorMu.Lock()
	defer governorMu.Unlock()
	concLimiter = conc
	rateLimiter = rate
	defaults = def
}

// SetGlobalLimits updates ONLY the process-wide default limits, leaving the
// installed limiter backends intact. Used by the system-settings runtime bridge
// so an operator can retune model.max_concurrency / max_rpm / max_tpm without a
// restart. A non-positive value in a dimension disables that default (models
// that carry their own value still honour it).
func SetGlobalLimits(def Limits) {
	governorMu.Lock()
	defer governorMu.Unlock()
	defaults = def
}

// resolveDim returns the model's own value when positive, otherwise the
// process-wide default (which may itself be <= 0 to mean "disabled").
func resolveDim(modelVal, defaultVal int) int {
	if modelVal > 0 {
		return modelVal
	}
	return defaultVal
}

// Reservation is a held multi-dimensional admission. Release MUST be called
// exactly once (a deferred call is safe). It is always safe to call on the
// passthrough / fail-open paths, where it is a cheap no-op.
type Reservation struct {
	release func()           // frees the concurrency slot
	refund  func(actual int) // reconciles the TPM reservation
}

// Release frees the concurrency slot and reconciles the TPM reservation.
// actualTokens is the authoritative token count from the provider's response
// (e.g. ChatResponse.Usage.TotalTokens); pass a negative value when the real
// count is unavailable (embedding / vlm APIs don't return usage) so the
// pre-charged estimate is left in place rather than being refunded.
func (r *Reservation) Release(actualTokens int) {
	if r == nil {
		return
	}
	if r.refund != nil && actualTokens >= 0 {
		r.refund(actualTokens)
	}
	if r.release != nil {
		r.release()
	}
}

// Admit acquires a per-model admission across every enabled dimension when the
// call is a background task (see types.IsBackgroundTask) and a governor is
// installed. limits are the model's own configured caps (0 = fall back to the
// process-wide default). estTokens sizes the TPM reservation; pass 0 to skip
// the TPM dimension for this call. The returned Reservation is never nil and is
// always safe to Release. Admit never blocks permanently — a limiter/Redis
// outage or a cancelled context fails open.
func Admit(ctx context.Context, modelID string, limits Limits, estTokens int) *Reservation {
	governorMu.RLock()
	conc, rate, def := concLimiter, rateLimiter, defaults
	governorMu.RUnlock()

	if modelID == "" || !types.IsBackgroundTask(ctx) {
		return &Reservation{}
	}

	concN := resolveDim(limits.Concurrency, def.Concurrency)
	rpm := resolveDim(limits.RPM, def.RPM)
	tpm := resolveDim(limits.TPM, def.TPM)

	res := &Reservation{}

	// 1) Concurrency (outermost reaction to slow providers). Acquired first so
	//    the semaphore bounds how many callers even reach the rate buckets.
	if conc != nil && concN > 0 {
		if rel, err := conc.Acquire(ctx, modelID, concN); err == nil && rel != nil {
			res.release = rel
		}
	}
	// 2) RPM: one request token per call.
	if rate != nil && rpm > 0 {
		_ = rate.Wait(ctx, "rpm:"+modelID, rpm, rpm, 1)
	}
	// 3) TPM: pre-charge the estimate; reconcile against real usage on Release.
	if rate != nil && tpm > 0 && estTokens > 0 {
		_ = rate.Wait(ctx, "tpm:"+modelID, tpm, tpm, estTokens)
		res.refund = func(actual int) {
			delta := estTokens - actual // >0 refund unused, <0 debit extra
			if delta != 0 {
				rate.Adjust(context.Background(), "tpm:"+modelID, tpm, tpm, delta)
			}
		}
	}
	return res
}

// Gate acquires a concurrency-only slot using the process-wide default limit.
// Retained for callers that only need the semaphore dimension. Equivalent to
// Admit(ctx, modelID, Limits{}, 0) but returns a bare release func.
func Gate(ctx context.Context, modelID string) func() {
	res := Admit(ctx, modelID, Limits{}, 0)
	return func() { res.Release(-1) }
}

// GateN acquires a concurrency-only slot with an explicit per-model limit.
// Retained for backward compatibility; prefer Admit for multi-dimension gating.
func GateN(ctx context.Context, modelID string, modelLimit int) func() {
	res := Admit(ctx, modelID, Limits{Concurrency: modelLimit}, 0)
	return func() { res.Release(-1) }
}

// localLimiter is an in-process (single-node) counting semaphore keyed by
// model ID. It is the Lite-mode counterpart to the Redis limiter: Lite runs a
// single process with no Redis, so a shared distributed semaphore is neither
// available nor needed — but background ingestion can still burst the whole
// worker pool against one provider, so we still cap concurrency locally.
type localLimiter struct {
	mu   sync.Mutex
	sems map[string]chan struct{}
}

// NewLocalLimiter builds an in-process per-key concurrency limiter.
func NewLocalLimiter() ModelConcurrencyLimiter {
	return &localLimiter{sems: make(map[string]chan struct{})}
}

func (l *localLimiter) Acquire(ctx context.Context, key string, limit int) (func(), error) {
	if l == nil || limit <= 0 || key == "" {
		return noop, nil
	}

	l.mu.Lock()
	sem, ok := l.sems[key]
	if !ok {
		// Capacity is fixed at first use for a key; the limit is a
		// process-wide constant, so it never changes across acquires.
		sem = make(chan struct{}, limit)
		l.sems[key] = sem
	}
	l.mu.Unlock()

	select {
	case sem <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-sem }) }, nil
	case <-ctx.Done():
		// Fail open on cancellation, mirroring the Redis limiter.
		return noop, nil
	}
}
