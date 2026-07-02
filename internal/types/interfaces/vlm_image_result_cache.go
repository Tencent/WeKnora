package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type VLMImageResultCacheRepository interface {
	GetByKey(ctx context.Context, tenantID uint64, cacheKey string) (*types.VLMImageResultCache, error)
	PutIfAbsent(ctx context.Context, entry *types.VLMImageResultCache) (bool, error)
}
