package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// ProcessingArtifactRepository persists reusable ingestion artifacts.
type ProcessingArtifactRepository interface {
	GetByCacheKey(
		ctx context.Context,
		tenantID uint64,
		kind types.ProcessingArtifactKind,
		cacheKey string,
	) (*types.ProcessingArtifact, error)
	Acquire(
		ctx context.Context,
		candidate *types.ProcessingArtifact,
		leaseOwner string,
		leaseUntil time.Time,
	) (*types.ProcessingArtifact, bool, error)
	MarkReady(ctx context.Context, artifact *types.ProcessingArtifact) error
	MarkFailed(ctx context.Context, artifactID, leaseOwner, detail string) error
	TouchHit(ctx context.Context, artifactID string, accessedAt time.Time) error
}
