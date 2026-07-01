package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// GraphExtractionCacheRepository persists deterministic GraphRAG per-chunk
// extraction outputs. It caches only the LLM result; callers still own writing
// the graph into the current knowledge namespace.
type GraphExtractionCacheRepository interface {
	GetByKey(ctx context.Context, tenantID uint64, cacheKey string) (*types.GraphExtractionCache, error)
	Upsert(ctx context.Context, cache *types.GraphExtractionCache) error
}
