package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// ImageMultimodalCacheRepository persists deterministic OCR/caption outputs.
type ImageMultimodalCacheRepository interface {
	GetByKey(ctx context.Context, tenantID uint64, cacheKey string) (*types.ImageMultimodalCache, error)
	Upsert(ctx context.Context, cache *types.ImageMultimodalCache) error
}
