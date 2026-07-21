package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// ArtifactCacheRepository defines persistence operations for the
// content-addressed artifact cache.  Every operation is best-effort:
// callers should treat a miss / write failure as a signal to run the
// original computation, not as a hard error.
type ArtifactCacheRepository interface {
	// GetByKey returns the cached entry matching the compound unique key.
	// Returns nil, nil on miss (row not found or soft-deleted).
	GetByKey(ctx context.Context, tenantID uint64, cacheKey, cacheType, inputHash, configHash string) (*types.ArtifactCache, error)

	// Upsert writes a cache entry using ON CONFLICT DO UPDATE semantics.
	// On conflict the existing row's output, computed_at and updated_at
	// are refreshed.  This is safe for concurrent initial fills.
	Upsert(ctx context.Context, cache *types.ArtifactCache) error

	// DeleteByKeyPrefix permanently removes all entries whose cache_key starts
	// with the given prefix. Used for per-knowledge invalidation.
	DeleteByKeyPrefix(ctx context.Context, tenantID uint64, cacheKeyPrefix string) error
}

// ArtifactCacheService provides a high-level, best-effort cache facade
// for use by service-layer callers.  Every method swallows errors so
// callers do not branch on cache availability.
type ArtifactCacheService interface {
	// Lookup retrieves a cached artifact.  Returns nil on miss or error.
	Lookup(ctx context.Context, tenantID uint64, cacheKey, cacheType, inputHash, configHash string) *types.ArtifactCache

	// Store persists a computation result.  Errors are logged only.
	Store(ctx context.Context, cache *types.ArtifactCache)

	// InvalidateByKnowledge removes cache entries whose key starts with
	// the given knowledge ID prefix.  Best-effort: errors are logged.
	InvalidateByKnowledge(ctx context.Context, tenantID uint64, knowledgeID string)
}
