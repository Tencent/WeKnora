package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// SubKnowledgeRepository keeps sub_knowledge ingestion transactional without
// requiring unrelated knowledge repository adapters to implement sub_knowledge storage.
type SubKnowledgeRepository interface {
	CreateSubKnowledge(
		context.Context,
		*types.Knowledge,
		*types.SubKnowledgeMapping,
		...string,
	) (*types.Knowledge, bool, error)
	UpdateSubKnowledge(context.Context, *types.Knowledge, *types.SubKnowledgeInput, string) error
	SetSubKnowledge(context.Context, uint64, string, string, *types.SubKnowledgeInput) error
	GetSubKnowledgeMapping(context.Context, uint64, string) (*types.SubKnowledgeMapping, error)
	ListSubKnowledge(context.Context, uint64, string, string, string, string, int) (*types.SubKnowledgeGroup, error)
}

// SubKnowledgeService manages and reads external knowledge source associations.
type SubKnowledgeService interface {
	GetSubKnowledge(context.Context, string, string, string, int) (*types.SubKnowledgeGroup, error)
	SetSubKnowledge(context.Context, string, string, *types.SubKnowledgeInput) error
}
