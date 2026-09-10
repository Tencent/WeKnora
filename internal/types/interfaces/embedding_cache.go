package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// EmbeddingCacheRepository persists embedding vectors for reuse.
type EmbeddingCacheRepository interface {
	Get(ctx context.Context, key *types.EmbeddingCacheKey) ([]float32, bool, error)
	Set(ctx context.Context, key *types.EmbeddingCacheKey, vector []float32) error
	IncrementHit(ctx context.Context, key *types.EmbeddingCacheKey) error
}
