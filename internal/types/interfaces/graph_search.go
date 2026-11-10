package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// GraphSearchRepository is separate from the agent retrieval contract: the
// public API needs isolated entities, bounded fields and explicit cap metadata.
type GraphSearchRepository interface {
	SearchGraph(context.Context, types.NameSpace, string) (*types.GraphSearchCandidates, error)
}

// GraphSearchEvidenceRepository reads validated, bounded source content.
type GraphSearchEvidenceRepository interface {
	// GetGraphSourceChunks joins live chunks/documents in exactly one owner
	// tenant and KB, projecting bounded text in the same SQL read as validation.
	GetGraphSourceChunks(context.Context, uint64, string, []string) ([]types.GraphSearchSourceChunk, error)
}

// GraphSearchService requires an exact KB grant before any graph/database read.
type GraphSearchService interface {
	Search(context.Context, *types.KnowledgeBase, types.GraphSearchRequest) (*types.GraphSearchResult, error)
}
