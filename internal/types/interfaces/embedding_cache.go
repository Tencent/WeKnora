package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type EmbeddingCacheRepository interface {
	Get(ctx context.Context, tenantID uint64, modelKey string, inputHashes []string) (map[string][]float32, error)
	Put(ctx context.Context, entries []*types.EmbeddingCacheEntry) error
}
