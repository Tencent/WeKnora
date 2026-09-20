package embedding

import "sync/atomic"

// CacheMetricsSnapshot is the application-level Embedding cache measurement.
// It deliberately does not reuse Provider prompt-cache counters.
type CacheMetricsSnapshot struct {
	Requests          int64 `json:"requests"`
	HitItems          int64 `json:"hit_items"`
	MissItems         int64 `json:"miss_items"`
	ProviderRequests  int64 `json:"provider_requests"`
	ProviderItems     int64 `json:"provider_items"`
	GetErrors         int64 `json:"get_errors"`
	SetErrors         int64 `json:"set_errors"`
	InvalidEntries    int64 `json:"invalid_entries"`
	SingleflightWaits int64 `json:"singleflight_waits"`
	LockWaits         int64 `json:"lock_waits"`
}

type cacheMetrics struct {
	requests          atomic.Int64
	hitItems          atomic.Int64
	missItems         atomic.Int64
	providerRequests  atomic.Int64
	providerItems     atomic.Int64
	getErrors         atomic.Int64
	setErrors         atomic.Int64
	invalidEntries    atomic.Int64
	singleflightWaits atomic.Int64
	lockWaits         atomic.Int64
}

func (m *cacheMetrics) snapshot() CacheMetricsSnapshot {
	if m == nil {
		return CacheMetricsSnapshot{}
	}
	return CacheMetricsSnapshot{
		Requests:          m.requests.Load(),
		HitItems:          m.hitItems.Load(),
		MissItems:         m.missItems.Load(),
		ProviderRequests:  m.providerRequests.Load(),
		ProviderItems:     m.providerItems.Load(),
		GetErrors:         m.getErrors.Load(),
		SetErrors:         m.setErrors.Load(),
		InvalidEntries:    m.invalidEntries.Load(),
		SingleflightWaits: m.singleflightWaits.Load(),
		LockWaits:         m.lockWaits.Load(),
	}
}

// CacheMetrics returns a point-in-time snapshot for the supplied cache. The
// helper is intentionally read-only; an API or periodic exporter can be added
// later without changing the cache wrapper contract.
func CacheMetrics(cache ResultCache) CacheMetricsSnapshot {
	if provider, ok := cache.(interface{ cacheMetrics() *cacheMetrics }); ok {
		return provider.cacheMetrics().snapshot()
	}
	return CacheMetricsSnapshot{}
}
