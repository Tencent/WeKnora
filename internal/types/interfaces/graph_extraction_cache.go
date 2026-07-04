package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// GraphExtractionCacheRepository persists deterministic per-chunk GraphRAG extraction outputs.
type GraphExtractionCacheRepository interface {
	GetByKey(ctx context.Context, tenantID uint64, cacheKey string) (*types.GraphExtractionCache, error)
	Upsert(ctx context.Context, cache *types.GraphExtractionCache) error
}
