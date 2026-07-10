package limiter

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

var (
	governorMu sync.RWMutex
	governor   ModelQuotaLimiter
	defaults   Limits
)

// SetGovernor installs the backend and the legacy default concurrency value.
func SetGovernor(l ModelQuotaLimiter, limit int) {
	governorMu.Lock()
	defer governorMu.Unlock()
	governor = l
	if l == nil {
		defaults = Limits{MaxConcurrency: limit}
		return
	}
	defaults.MaxConcurrency = limit
}

func SetGlobalLimit(limit int) {
	governorMu.Lock()
	defer governorMu.Unlock()
	defaults.MaxConcurrency = limit
}

// SetRateDefaults updates runtime defaults. A zero RPM/TPM disables that
// dimension unless a model config supplies its own positive value.
func SetRateDefaults(rpm, tpm, interactiveReserve int) {
	governorMu.Lock()
	defer governorMu.Unlock()
	defaults.RequestsPerMinute = rpm
	defaults.TokensPerMinute = tpm
	defaults.InteractiveConcurrencyReserve = interactiveReserve
}

// QuotaKey namespaces an explicit shared provider quota group by tenant.
// Without a group, the model ID retains the old per-model isolation.
func QuotaKey(tenantID uint64, modelID, group string) string {
	key := strings.TrimSpace(group)
	if key == "" {
		key = strings.TrimSpace(modelID)
	}
	return fmt.Sprintf("%d:%s", tenantID, key)
}

// Admit resolves model overrides against runtime defaults and governs both
// interactive and background calls. Background calls yield the configured
// reserve to interactive traffic.
func Admit(ctx context.Context, key string, modelLimits Limits, estimatedTokens int) (*Permit, error) {
	governorMu.RLock()
	backend, global := governor, defaults
	governorMu.RUnlock()

	resolved := modelLimits
	if resolved.MaxConcurrency == 0 {
		resolved.MaxConcurrency = global.MaxConcurrency
	}
	if resolved.RequestsPerMinute == 0 {
		resolved.RequestsPerMinute = global.RequestsPerMinute
	}
	if resolved.TokensPerMinute == 0 {
		resolved.TokensPerMinute = global.TokensPerMinute
	}
	if resolved.InteractiveConcurrencyReserve == 0 {
		resolved.InteractiveConcurrencyReserve = global.InteractiveConcurrencyReserve
	}
	// Negative values explicitly disable a global default for this model.
	if resolved.MaxConcurrency < 0 {
		resolved.MaxConcurrency = 0
	}
	if resolved.RequestsPerMinute < 0 {
		resolved.RequestsPerMinute = 0
	}
	if resolved.TokensPerMinute < 0 {
		resolved.TokensPerMinute = 0
	}
	if resolved.InteractiveConcurrencyReserve < 0 {
		resolved.InteractiveConcurrencyReserve = 0
	}
	if backend == nil || key == "" || limitsDisabled(resolved) {
		return noopPermit(), nil
	}
	return backend.Admit(ctx, key, resolved, Request{
		EstimatedTokens: estimatedTokens,
		Background:      types.IsBackgroundTask(ctx),
	})
}

// Gate/GateN retain the old background-only API for compatibility. New model
// wrappers use Admit so RPM/TPM and interactive reserve share one admission.
func Gate(ctx context.Context, modelID string) func() { return GateN(ctx, modelID, 0) }

func GateN(ctx context.Context, modelID string, modelLimit int) func() {
	if !types.IsBackgroundTask(ctx) {
		return func() {}
	}
	permit, err := Admit(ctx, modelID, Limits{MaxConcurrency: modelLimit}, 0)
	if err != nil || permit == nil {
		return func() {}
	}
	return permit.Release
}

type localBucket struct {
	balance float64
	last    time.Time
	limit   int
}

func (b *localBucket) refill(now time.Time, limit int) {
	if limit <= 0 {
		return
	}
	if b.last.IsZero() || b.limit != limit {
		if b.last.IsZero() {
			b.balance = float64(limit)
		} else if b.balance > float64(limit) {
			b.balance = float64(limit)
		}
		b.limit = limit
		b.last = now
		return
	}
	b.balance += now.Sub(b.last).Seconds() * float64(limit) / 60
	if b.balance > float64(limit) {
		b.balance = float64(limit)
	}
	b.last = now
}

type localEvent struct {
	reserved int
}

type localQuotaState struct {
	inflight   int
	background int
	requests   localBucket
	tokens     localBucket
	events     map[string]localEvent
}

type localLimiter struct {
	mu     sync.Mutex
	states map[string]*localQuotaState
	poll   time.Duration
}

func NewLocalLimiter() ModelQuotaLimiter {
	return &localLimiter{states: make(map[string]*localQuotaState), poll: 50 * time.Millisecond}
}

func (l *localLimiter) Acquire(ctx context.Context, key string, limit int) (func(), error) {
	permit, err := l.Admit(ctx, key, Limits{MaxConcurrency: limit}, Request{})
	if err != nil {
		return nil, err
	}
	return permit.Release, nil
}

func (l *localLimiter) Admit(ctx context.Context, key string, limits Limits, req Request) (*Permit, error) {
	if l == nil || key == "" || limitsDisabled(limits) {
		return noopPermit(), nil
	}
	if limits.TokensPerMinute > 0 && req.EstimatedTokens > limits.TokensPerMinute {
		return nil, fmt.Errorf("%w: estimated=%d tpm=%d", ErrRequestExceedsTPM, req.EstimatedTokens, limits.TokensPerMinute)
	}
	ticker := time.NewTicker(l.poll)
	defer ticker.Stop()
	for {
		now := time.Now()
		l.mu.Lock()
		state := l.states[key]
		if state == nil {
			state = &localQuotaState{events: make(map[string]localEvent)}
			l.states[key] = state
		}
		state.requests.refill(now, limits.RequestsPerMinute)
		state.tokens.refill(now, limits.TokensPerMinute)
		allowed := localConcurrencyAllowed(state, limits, req.Background)
		if limits.RequestsPerMinute > 0 && state.requests.balance < 1 {
			allowed = false
		}
		if limits.TokensPerMinute > 0 && state.tokens.balance < float64(req.EstimatedTokens) {
			allowed = false
		}
		if allowed {
			id := uuid.NewString()
			if limits.MaxConcurrency > 0 {
				state.inflight++
				if req.Background {
					state.background++
				}
			}
			if limits.RequestsPerMinute > 0 {
				state.requests.balance--
			}
			if limits.TokensPerMinute > 0 {
				state.tokens.balance -= float64(req.EstimatedTokens)
			}
			state.events[id] = localEvent{reserved: req.EstimatedTokens}
			l.mu.Unlock()
			return &Permit{complete: func(actual int) {
				l.complete(key, id, limits, req.Background, actual)
			}}, nil
		}
		l.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func localConcurrencyAllowed(state *localQuotaState, limits Limits, background bool) bool {
	if limits.MaxConcurrency <= 0 {
		return true
	}
	if state.inflight >= limits.MaxConcurrency {
		return false
	}
	bgLimit := limits.MaxConcurrency
	if limits.InteractiveConcurrencyReserve > 0 && limits.MaxConcurrency > 1 {
		reserve := limits.InteractiveConcurrencyReserve
		if reserve >= limits.MaxConcurrency {
			reserve = limits.MaxConcurrency - 1
		}
		bgLimit -= reserve
	}
	return !background || state.background < bgLimit
}

func (l *localLimiter) complete(key, id string, limits Limits, background bool, actual int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	state := l.states[key]
	if state == nil {
		return
	}
	event, ok := state.events[id]
	if !ok {
		return
	}
	delete(state.events, id)
	if limits.MaxConcurrency > 0 && state.inflight > 0 {
		state.inflight--
		if background && state.background > 0 {
			state.background--
		}
	}
	if limits.TokensPerMinute > 0 && actual > 0 {
		state.tokens.balance += float64(event.reserved - actual)
		if state.tokens.balance > float64(limits.TokensPerMinute) {
			state.tokens.balance = float64(limits.TokensPerMinute)
		}
	}
}
