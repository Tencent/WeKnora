package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type ProcessingCacheRepository interface {
	Get(ctx context.Context, tenantID uint64, stage, cacheKey string) (*types.ProcessingCache, error)
	Upsert(ctx context.Context, cache *types.ProcessingCache) error
}
