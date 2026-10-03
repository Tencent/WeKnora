package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// KnowledgeBaseProfileService derives a knowledge-base description from the
// structured profiles of its documents. The aggregation is deterministic and
// cheap; only the final short text costs one small model call, and that call
// is skipped when the aggregate has not changed since the last generation.
type KnowledgeBaseProfileService interface {
	// BuildAggregate computes the live aggregate (counts, samples, hash) for
	// the knowledge base without calling a model.
	BuildAggregate(ctx context.Context, kb *types.KnowledgeBase) (*types.KnowledgeBaseProfileAggregate, error)
	// GenerateKnowledgeBaseProfile rebuilds the generated description. With
	// force=false it returns the stored profile untouched when the aggregate
	// hash still matches. The returned profile is also written to kb.
	GenerateKnowledgeBaseProfile(
		ctx context.Context, kb *types.KnowledgeBase, force bool,
	) (*types.KnowledgeBaseProfile, error)
	// RequestKnowledgeBaseProfileRefresh enqueues an asynchronous rebuild.
	// It is a no-op when automatic generation is disabled unless force is set.
	RequestKnowledgeBaseProfileRefresh(ctx context.Context, kb *types.KnowledgeBase, force bool) error
	// Handle is the asynq entry point for types.TypeKnowledgeBaseProfile.
	Handle(ctx context.Context, task *asynq.Task) error
}
