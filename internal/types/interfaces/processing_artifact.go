package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type ProcessingArtifactRepository interface {
	Get(ctx context.Context, key types.ProcessingArtifactKey) (*types.ProcessingArtifact, error)
	PutIfAbsent(ctx context.Context, artifact *types.ProcessingArtifact) (created bool, err error)
	DeleteByID(ctx context.Context, tenantID, id uint64) error
}

type ProcessingArtifactStore interface {
	Get(ctx context.Context, key types.ProcessingArtifactKey) (value []byte, hit bool, err error)
	PutIfAbsent(ctx context.Context, key types.ProcessingArtifactKey, value []byte) (
		canonical []byte, created bool, err error,
	)
}
