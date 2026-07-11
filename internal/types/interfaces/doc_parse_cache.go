package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type DocParseCacheRepository interface {
	GetByKey(ctx context.Context, tenantID uint64, cacheKey string) (*types.DocParseCache, error)
	Upsert(ctx context.Context, cache *types.DocParseCache) error
}
