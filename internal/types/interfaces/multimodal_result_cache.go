package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// MultimodalResultCacheRepository persists successful VLM OCR/caption outputs.
type MultimodalResultCacheRepository interface {
	GetByKey(ctx context.Context, tenantID uint64, cacheKey string) (*types.MultimodalResultCache, error)
	Upsert(ctx context.Context, cache *types.MultimodalResultCache) error
}
