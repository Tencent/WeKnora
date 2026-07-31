package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// DocumentParseCacheRepository persists resolved parser artifacts used by
// reparses of the same knowledge.
type DocumentParseCacheRepository interface {
	GetByKey(ctx context.Context, tenantID uint64, knowledgeID, cacheKey string) (*types.DocumentParseCache, error)
	Upsert(ctx context.Context, cache *types.DocumentParseCache) error
}
