package types

import (
	"context"
	"strings"
	"sync"
)

// LLMTokenPricing is a user-configured price snapshot expressed per one
// million tokens. Currency is an ISO-style code such as USD or CNY.
type LLMTokenPricing struct {
	Enabled              bool    `json:"enabled"`
	Currency             string  `json:"currency,omitempty"`
	InputPerMillion      float64 `json:"input_per_million"`
	OutputPerMillion     float64 `json:"output_per_million"`
	CacheReadPerMillion  float64 `json:"cache_read_per_million"`
	CacheWritePerMillion float64 `json:"cache_write_per_million"`
}

// EstimateLLMCallCost calculates a call cost from provider-reported usage.
// Cached read/write tokens are removed from regular input tokens so no token
// is charged twice. Negative or inconsistent counters are clamped safely.
func EstimateLLMCallCost(usage TokenUsage, pricing LLMTokenPricing) float64 {
	if !pricing.Enabled {
		return 0
	}
	promptTokens := max(usage.PromptTokens, 0)
	cacheReadTokens := min(max(usage.CacheReadTokens, 0), promptTokens)
	remaining := promptTokens - cacheReadTokens
	cacheWriteTokens := min(max(usage.CacheWriteTokens, 0), remaining)
	regularInputTokens := remaining - cacheWriteTokens
	completionTokens := max(usage.CompletionTokens, 0)

	cost := float64(regularInputTokens)*max(pricing.InputPerMillion, 0) +
		float64(completionTokens)*max(pricing.OutputPerMillion, 0) +
		float64(cacheReadTokens)*max(pricing.CacheReadPerMillion, 0) +
		float64(cacheWriteTokens)*max(pricing.CacheWritePerMillion, 0)
	return cost / 1_000_000
}

// Normalize fills the default currency and removes surrounding whitespace.
func (p LLMTokenPricing) Normalize() LLMTokenPricing {
	p.Currency = strings.ToUpper(strings.TrimSpace(p.Currency))
	if p.Enabled && p.Currency == "" {
		p.Currency = "USD"
	}
	return p
}

// LLMCallObservation is the provider-independent telemetry emitted for one
// completed model call. Prompt content is intentionally excluded; only the
// stable prefix fingerprint is retained for cache analysis.
type LLMCallObservation struct {
	TenantID                uint64
	ModelType               ModelType
	ModelID                 string
	ModelName               string
	Purpose                 string
	PromptPrefixFingerprint string
	Usage                   TokenUsage
	Pricing                 LLMTokenPricing
	EstimatedCost           float64
	DurationMS              int64
	Success                 bool
	Error                   string
}

// LLMCallObserver receives completed model-call telemetry. Implementations
// must be safe for concurrent use because evaluation items run in parallel.
type LLMCallObserver interface {
	ObserveLLMCall(observation LLMCallObservation)
}

type llmCallObserverContextKey struct{}

var processLLMCallObserver struct {
	sync.RWMutex
	observer LLMCallObserver
}

// SetGlobalLLMCallObserver sets the fallback observer for model calls that do
// not already have a request-scoped observer. Passing nil disables it.
func SetGlobalLLMCallObserver(observer LLMCallObserver) {
	processLLMCallObserver.Lock()
	processLLMCallObserver.observer = observer
	processLLMCallObserver.Unlock()
}

// GlobalLLMCallObserver returns the process-wide fallback observer, if set.
func GlobalLLMCallObserver() (LLMCallObserver, bool) {
	processLLMCallObserver.RLock()
	defer processLLMCallObserver.RUnlock()
	return processLLMCallObserver.observer, processLLMCallObserver.observer != nil
}

// DispatchLLMCallObservation prefers a request-scoped observer and otherwise
// uses the process observer only when the context carries a real tenant.
func DispatchLLMCallObservation(ctx context.Context, observation LLMCallObservation) {
	observer, requestScoped := LLMCallObserverFromContext(ctx)
	if !requestScoped {
		var ok bool
		observer, ok = GlobalLLMCallObserver()
		if !ok {
			return
		}
	}
	tenantID, tenantScoped := TenantIDFromContext(ctx)
	if !requestScoped && !tenantScoped {
		return
	}
	observation.TenantID = tenantID
	observer.ObserveLLMCall(observation)
}

// WithLLMCallObserver attaches a request-scoped model-call observer.
func WithLLMCallObserver(ctx context.Context, observer LLMCallObserver) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, llmCallObserverContextKey{}, observer)
}

// LLMCallObserverFromContext returns the observer attached to ctx, if any.
func LLMCallObserverFromContext(ctx context.Context) (LLMCallObserver, bool) {
	if ctx == nil {
		return nil, false
	}
	observer, ok := ctx.Value(llmCallObserverContextKey{}).(LLMCallObserver)
	return observer, ok && observer != nil
}
