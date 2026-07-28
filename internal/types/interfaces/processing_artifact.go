package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type ProcessingArtifactRepository interface {
	Get(ctx context.Context, key types.ProcessingArtifactKey) (*types.ProcessingArtifact, error)
	ListExpired(ctx context.Context, cutoff time.Time, afterID uint64, limit int) ([]*types.ProcessingArtifact, error)
	PutIfAbsent(ctx context.Context, artifact *types.ProcessingArtifact) (created bool, err error)
	DeleteByID(ctx context.Context, tenantID, id uint64) error
	DeleteByIDWithResult(ctx context.Context, tenantID, id uint64) (removed bool, err error)
}

type ProcessingArtifactStore interface {
	Get(ctx context.Context, key types.ProcessingArtifactKey) (value []byte, hit bool, err error)
	Invalidate(ctx context.Context, key types.ProcessingArtifactKey, observed []byte) error
	PutIfAbsent(ctx context.Context, key types.ProcessingArtifactKey, value []byte) (
		canonical []byte, created bool, err error,
	)
}

type ProcessingArtifactBatchStore interface {
	ProcessingArtifactStore
	GetMany(ctx context.Context, keys []types.ProcessingArtifactKey) (
		values map[types.ProcessingArtifactKey][]byte, err error,
	)
	PutManyIfAbsent(ctx context.Context, values map[types.ProcessingArtifactKey][]byte) (
		canonical map[types.ProcessingArtifactKey][]byte, err error,
	)
}

type ProcessingArtifactCounterRegistry interface {
	Record(stage, outcome string)
	Snapshot() []types.ProcessingArtifactCounter
}

type ProcessingArtifactRetentionService interface {
	PurgeExpired(ctx context.Context, cutoff time.Time, batchSize int) (types.ProcessingArtifactPurgeResult, error)
}
