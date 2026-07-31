package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// KnowledgeDeleteCoordinatorRepository coordinates durable delete state changes.
type KnowledgeDeleteCoordinatorRepository interface {
	BeginKnowledgeDelete(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		knowledgeID string,
	) (*types.Knowledge, error)
	FinalizeKnowledgeDelete(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		knowledgeID string,
	) error
}
