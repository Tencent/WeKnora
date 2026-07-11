package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type WikiMapCacheRepository interface {
	GetByKey(ctx context.Context, tenantID uint64, cacheKey string) (*types.WikiMapCache, error)
	Upsert(ctx context.Context, cache *types.WikiMapCache) error
}
