package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// EmbeddingCacheRepository provides content-addressed embedding reuse.
type EmbeddingCacheRepository interface {
	GetEmbeddingsByHashes(
		ctx context.Context,
		tenantID uint64,
		modelID string,
		dimension int,
		hashes []string,
	) (map[string][]float32, error)
	UpsertEmbeddings(ctx context.Context, entries []*types.EmbeddingCache) error
}

// GenerationCacheRepository provides deterministic artifact reuse for LLM/VLM
// steps where the full cache key is known by the caller.
type GenerationCacheRepository interface {
	Get(ctx context.Context, tenantID uint64, namespace, scopeID, modelID, inputHash, promptVersion, promptHash string) (*types.GenerationCache, bool, error)
	Upsert(ctx context.Context, entry *types.GenerationCache) error
}
