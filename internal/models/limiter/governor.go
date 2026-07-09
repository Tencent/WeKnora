package limiter

import (
	"context"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
)

// The concurrency governor is process-wide, shared by every model-client layer
// that fronts a provider (chat, vlm). Keeping the singleton here — rather than
// inside one client package — lets all of them gate against the same limiter
// and per-model limit without importing each other. Wired once at startup (see
// container.registerModelConcurrencyLimiter) via SetGovernor.
var (
	governorMu sync.RWMutex
	governor   ModelConcurrencyLimiter
	governorN  int
)

// SetGovernor installs the process-wide background concurrency governor and the
// default per-model limit. Passing a nil limiter or a non-positive limit
// disables governance (all calls pass through). Safe to call at startup.
func SetGovernor(l ModelConcurrencyLimiter, limit int) {
	governorMu.Lock()
	defer governorMu.Unlock()
	governor = l
	governorN = limit
}

// Gate acquires a per-model concurrency slot when the call is a background task
// (see types.IsBackgroundTask) and a governor is installed. It returns a
// release func that is ALWAYS safe to call: on the passthrough / fail-open
// paths it is a cheap no-op. The gate never blocks a call permanently — a
// limiter/Redis outage or a cancelled context fails open.
func Gate(ctx context.Context, modelID string) func() {
	governorMu.RLock()
	l, limit := governor, governorN
	governorMu.RUnlock()

	if l == nil || limit <= 0 || !types.IsBackgroundTask(ctx) {
		return noop
	}
	release, err := l.Acquire(ctx, modelID, limit)
	if err != nil || release == nil {
		return noop
	}
	return release
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
