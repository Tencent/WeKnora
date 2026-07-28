package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// ProcessingArtifactRepository provides immutable, tenant-scoped artifact
// manifests. Put methods never overwrite an existing successful value.
type ProcessingArtifactRepository interface {
	Get(
		ctx context.Context,
		key types.ProcessingArtifactLookup,
	) (*types.ProcessingArtifact, error)
	BatchGet(
		ctx context.Context,
		keys []types.ProcessingArtifactLookup,
	) (map[types.ProcessingArtifactLookup]*types.ProcessingArtifact, error)
	PutIfAbsent(
		ctx context.Context,
		candidate *types.ProcessingArtifact,
	) (winner *types.ProcessingArtifact, created bool, err error)
	PutManyIfAbsent(
		ctx context.Context,
		candidates []*types.ProcessingArtifact,
	) (map[types.ProcessingArtifactLookup]*types.ProcessingArtifact, error)
	DeleteCorrupt(
		ctx context.Context,
		key types.ProcessingArtifactLookup,
		observedChecksum string,
	) error
	TouchHits(ctx context.Context, keys []types.ProcessingArtifactLookup) error
}
