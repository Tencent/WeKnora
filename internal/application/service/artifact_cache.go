package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// artifactCacheService implements interfaces.ArtifactCacheService.
// Every method is best-effort: failures are logged and swallowed so
// callers never branch on cache availability.
type artifactCacheService struct {
	repo interfaces.ArtifactCacheRepository
}

// NewArtifactCacheService creates the best-effort cache service.
func NewArtifactCacheService(repo interfaces.ArtifactCacheRepository) interfaces.ArtifactCacheService {
	return &artifactCacheService{repo: repo}
}

// Lookup retrieves a cached artifact.  Returns nil on miss or error —
// the caller should fall through to the original computation.
func (s *artifactCacheService) Lookup(
	ctx context.Context,
	tenantID uint64,
	cacheKey, cacheType, inputHash, configHash string,
) *types.ArtifactCache {
	entry, err := s.repo.GetByKey(ctx, tenantID, cacheKey, cacheType, inputHash, configHash)
	if err != nil {
		logger.Warnf(ctx, "[artifact_cache] lookup error (key=%s type=%s): %v", cacheKey, cacheType, err)
		return nil
	}
	if entry == nil {
		return nil
	}
	// Honor explicit TTLs.
	if entry.ExpiresAt != nil && time.Now().After(*entry.ExpiresAt) {
		return nil
	}
	return entry
}

// Store persists a computation result.  Errors are logged only — the
// caller already has the result and can continue.
func (s *artifactCacheService) Store(ctx context.Context, cache *types.ArtifactCache) {
	if cache == nil {
		return
	}
	cache.ComputedAt = time.Now()
	if err := s.repo.Upsert(ctx, cache); err != nil {
		logger.Warnf(ctx, "[artifact_cache] store error (key=%s type=%s): %v",
			cache.CacheKey, cache.CacheType, err)
	}
}

// InvalidateByKnowledge removes cache entries whose cache_key starts with
// "knowledge:<knowledgeID>".  Best-effort: errors are logged.
func (s *artifactCacheService) InvalidateByKnowledge(
	ctx context.Context, tenantID uint64, knowledgeID string,
) {
	prefix := fmt.Sprintf("knowledge:%s:", knowledgeID)
	if err := s.repo.DeleteByKeyPrefix(ctx, tenantID, prefix); err != nil {
		logger.Warnf(ctx, "[artifact_cache] invalidate knowledge %s: %v", knowledgeID, err)
	}
}

// Ensure interface compliance.
var _ interfaces.ArtifactCacheService = (*artifactCacheService)(nil)
