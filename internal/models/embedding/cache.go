package embedding

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/Tencent/WeKnora/internal/types"
)

// EmbeddingCache is the narrow interface cachedEmbedder uses.
type EmbeddingCache interface {
	Get(ctx context.Context, key *types.EmbeddingCacheKey) ([]float32, bool, error)
	Set(ctx context.Context, key *types.EmbeddingCacheKey, vector []float32) error
	IncrementHit(ctx context.Context, key *types.EmbeddingCacheKey) error
}

var (
	cacheMu            sync.RWMutex
	globalCache        EmbeddingCache
	statsHits          atomic.Int64
	statsMisses        atomic.Int64
	statsProviderCalls atomic.Int64
	modelStatsMu       sync.Mutex
	modelStats         = map[string]*modelCacheStats{}
)

type modelCacheStats struct {
	modelID       string
	modelName     string
	hits          int64
	misses        int64
	providerCalls int64
}

// SetEmbeddingCache installs the process-wide embedding cache. Tests may
// replace or clear it.
func SetEmbeddingCache(c EmbeddingCache) {
	cacheMu.Lock()
	globalCache = c
	cacheMu.Unlock()
}

// GetEmbeddingCache returns the installed cache, or nil when disabled.
func GetEmbeddingCache() EmbeddingCache {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	return globalCache
}

// CacheStats returns process-level hit/miss counters.
func CacheStats() types.EmbeddingCacheStats {
	stats := types.EmbeddingCacheStats{
		Enabled:       GetEmbeddingCache() != nil,
		Hits:          statsHits.Load(),
		Misses:        statsMisses.Load(),
		ProviderCalls: statsProviderCalls.Load(),
	}
	modelStatsMu.Lock()
	defer modelStatsMu.Unlock()
	for _, model := range modelStats {
		stats.Models = append(stats.Models, types.EmbeddingCacheModelStats{
			ModelID:       model.modelID,
			ModelName:     model.modelName,
			Hits:          model.hits,
			Misses:        model.misses,
			ProviderCalls: model.providerCalls,
		})
	}
	sort.Slice(stats.Models, func(i, j int) bool {
		if stats.Models[i].ModelID != stats.Models[j].ModelID {
			return stats.Models[i].ModelID < stats.Models[j].ModelID
		}
		return stats.Models[i].ModelName < stats.Models[j].ModelName
	})
	return stats
}

// ResetCacheStats clears hit/miss counters (used by tests and demos).
func ResetCacheStats() {
	statsHits.Store(0)
	statsMisses.Store(0)
	statsProviderCalls.Store(0)
	modelStatsMu.Lock()
	modelStats = map[string]*modelCacheStats{}
	modelStatsMu.Unlock()
}

func recordCacheHit(modelID, modelName string) {
	statsHits.Add(1)
	recordModelStat(modelID, modelName, func(s *modelCacheStats) { s.hits++ })
}

func recordCacheMiss(modelID, modelName string) {
	statsMisses.Add(1)
	recordModelStat(modelID, modelName, func(s *modelCacheStats) { s.misses++ })
}

func recordProviderCall(modelID, modelName string) {
	statsProviderCalls.Add(1)
	recordModelStat(modelID, modelName, func(s *modelCacheStats) { s.providerCalls++ })
}

func recordModelStat(modelID, modelName string, mutate func(*modelCacheStats)) {
	modelStatsMu.Lock()
	defer modelStatsMu.Unlock()
	model := modelStats[modelID]
	if model == nil {
		model = &modelCacheStats{modelID: modelID, modelName: modelName}
		modelStats[modelID] = model
	}
	mutate(model)
}
