package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// ContentCacheRepository persists deterministic content-processing caches.
type ContentCacheRepository interface {
	GetByKey(ctx context.Context, tenantID uint64, cacheKind, cacheKey string) (*types.ContentCacheEntry, error)
	Upsert(ctx context.Context, entry *types.ContentCacheEntry) error
}
