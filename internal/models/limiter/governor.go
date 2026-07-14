package limiter

import (
	"context"
	"sort"
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

// SetGlobalLimit updates ONLY the process-wide default per-model limit,
// leaving the installed limiter backend intact. Used by the system-settings
// runtime bridge so an operator can retune model.max_concurrency without a
// restart. A non-positive value disables the default (models that carry their
// own MaxConcurrency still honour it).
func SetGlobalLimit(limit int) {
	governorMu.Lock()
	defer governorMu.Unlock()
	governorN = limit
}

// Gate acquires a per-model concurrency slot using the process-wide default
// limit. Equivalent to GateN(ctx, modelID, 0).
func Gate(ctx context.Context, modelID string) func() {
	return GateN(ctx, modelID, 0)
}

// GateN acquires a per-model concurrency slot when the call is a background task
// (see types.IsBackgroundTask) and a governor is installed. modelLimit is the
// model's own configured cap; a value <= 0 means "fall back to the process-wide
// default" (governorN). It returns a release func that is ALWAYS safe to call:
// on the passthrough / fail-open paths it is a cheap no-op. The gate never
// blocks a call permanently — a limiter/Redis outage or a cancelled context
// fails open.
func GateN(ctx context.Context, modelID string, modelLimit int) func() {
	return GateNamedN(ctx, modelID, "", modelLimit)
}

func GateNamedN(ctx context.Context, modelID, modelName string, modelLimit int) func() {
	governorMu.RLock()
	l, defaultLimit := governor, governorN
	governorMu.RUnlock()

	limit := modelLimit
	if limit <= 0 {
		limit = defaultLimit
	}
	if l == nil || limit <= 0 || !types.IsBackgroundTask(ctx) {
		return noop
	}
	if named, ok := l.(interface{ SetModelName(string, string) }); ok {
		named.SetModelName(modelID, modelName)
	}
	release, err := l.Acquire(ctx, modelID, limit)
	if err != nil || release == nil {
		return noop
	}
	return release
}

// RuntimeStats returns the semaphores observed by this process. Redis-backed
// limiters report cluster-wide active holders; waiting callers remain local to
// this instance by design.
func RuntimeStats(ctx context.Context) ([]RuntimeStat, bool, error) {
	governorMu.RLock()
	l := governor
	governorMu.RUnlock()
	inspector, ok := l.(runtimeInspectable)
	if !ok || inspector == nil {
		return []RuntimeStat{}, false, nil
	}
	stats, err := inspector.RuntimeStats(ctx)
	if stats == nil {
		stats = []RuntimeStat{}
	}
	return stats, true, err
}

// localLimiter is an in-process (single-node) counting semaphore keyed by
// model ID. It is the Lite-mode counterpart to the Redis limiter: Lite runs a
// single process with no Redis, so a shared distributed semaphore is neither
// available nor needed — but background ingestion can still burst the whole
// worker pool against one provider, so we still cap concurrency locally.
type localLimiter struct {
	mu      sync.Mutex
	sems    map[string]chan struct{}
	tracked map[string]*trackedSemaphore
}

// NewLocalLimiter builds an in-process per-key concurrency limiter.
func NewLocalLimiter() ModelConcurrencyLimiter {
	return &localLimiter{sems: make(map[string]chan struct{}), tracked: make(map[string]*trackedSemaphore)}
}

func (l *localLimiter) Acquire(ctx context.Context, key string, limit int) (func(), error) {
	if l == nil || limit <= 0 || key == "" {
		return noop, nil
	}

	l.mu.Lock()
	sem, ok := l.sems[key]
	tracked := l.tracked[key]
	if tracked == nil {
		tracked = &trackedSemaphore{}
		l.tracked[key] = tracked
	}
	tracked.limit.Store(int64(limit))
	if !ok {
		// Capacity is fixed at first use for a key; the limit is a
		// process-wide constant, so it never changes across acquires.
		sem = make(chan struct{}, limit)
		l.sems[key] = sem
	}
	l.mu.Unlock()
	tracked.waiting.Add(1)
	defer tracked.waiting.Add(-1)

	select {
	case sem <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-sem }) }, nil
	case <-ctx.Done():
		// Fail open on cancellation, mirroring the Redis limiter.
		return noop, nil
	}
}

func (l *localLimiter) RuntimeStats(_ context.Context) ([]RuntimeStat, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	stats := make([]RuntimeStat, 0, len(l.sems))
	for modelID, sem := range l.sems {
		tracked := l.tracked[modelID]
		name, _ := tracked.name.Load().(string)
		stats = append(stats, RuntimeStat{ModelID: modelID, Name: name, Active: int64(len(sem)), Waiting: tracked.waiting.Load(), Limit: int(tracked.limit.Load())})
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].ModelID < stats[j].ModelID })
	return stats, nil
}

func (l *localLimiter) SetModelName(modelID, name string) {
	if modelID == "" || name == "" {
		return
	}
	l.mu.Lock()
	tracked := l.tracked[modelID]
	if tracked == nil {
		tracked = &trackedSemaphore{}
		l.tracked[modelID] = tracked
	}
	l.mu.Unlock()
	tracked.name.Store(name)
}
