package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type EmbeddingCacheRepository interface {
	Get(ctx context.Context, tenantID uint64, cacheKey string) (*types.EmbeddingCache, error)
	Upsert(ctx context.Context, entry *types.EmbeddingCache) error
}

type ImageMultimodalCacheRepository interface {
	Get(ctx context.Context, tenantID uint64, cacheKey string) (*types.ImageMultimodalCache, error)
	Upsert(ctx context.Context, entry *types.ImageMultimodalCache) error
}

type GraphExtractionCacheRepository interface {
	Get(ctx context.Context, tenantID uint64, cacheKey string) (*types.GraphExtractionCache, error)
	Upsert(ctx context.Context, entry *types.GraphExtractionCache) error
}

type WikiMapCacheRepository interface {
	Get(ctx context.Context, tenantID uint64, cacheKey string) (*types.WikiMapCache, error)
	Upsert(ctx context.Context, entry *types.WikiMapCache) error
}

type ReparseArtifactCacheRepository interface {
	Get(ctx context.Context, tenantID uint64, cacheKey string) (*types.ReparseArtifactCache, error)
	Upsert(ctx context.Context, entry *types.ReparseArtifactCache) error
}
