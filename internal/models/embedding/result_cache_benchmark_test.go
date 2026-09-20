package embedding

import (
	"context"
	"fmt"
	"testing"
)

// BenchmarkResultCacheModes compares the three application-level cache
// modes using the same deterministic provider and request shape. It reports
// provider calls per logical request so a warm-cache run can be compared with
// the disabled and cold-cache baselines without treating cache metrics as
// Provider usage events.
//
// Run with:
//
//	go test ./internal/models/embedding -run '^$' -bench BenchmarkResultCacheModes -benchmem
func BenchmarkResultCacheModes(b *testing.B) {
	scenarios := []struct {
		name      string
		cache     func() ResultCache
		requestID func(int) string
	}{
		{
			name: "disabled",
			cache: func() ResultCache {
				return noopResultCache{}
			},
			requestID: func(index int) string {
				return fmt.Sprintf("text-%d", index)
			},
		},
		{
			name: "cold",
			cache: func() ResultCache {
				return newMemoryResultCache(defaultEmbeddingCacheSize)
			},
			requestID: func(index int) string {
				return fmt.Sprintf("text-%d", index)
			},
		},
		{
			name: "warm",
			cache: func() ResultCache {
				return newMemoryResultCache(1)
			},
			requestID: func(int) string {
				return "same-text"
			},
		},
	}

	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			inner := &cacheTestEmbedder{}
			cache := scenario.cache()
			wrapped := WrapResultCache(inner, cache, testCacheConfig(), 1)

			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				if _, err := wrapped.Embed(context.Background(), scenario.requestID(index)); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()

			inner.mu.Lock()
			providerCalls := inner.embedCalls
			inner.mu.Unlock()
			b.ReportMetric(float64(providerCalls)/float64(b.N), "provider_calls/op")
			metrics := CacheMetrics(cache)
			if metrics.Requests > 0 {
				b.ReportMetric(float64(metrics.HitItems)/float64(metrics.Requests), "hit_items/request")
			}
		})
	}
}
