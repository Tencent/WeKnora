package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// RetrieveGraphRepository is a repository for retrieving graphs
type RetrieveGraphRepository interface {
	// AddGraph adds a graph to the repository
	AddGraph(ctx context.Context, namespace types.NameSpace, graphs []*types.GraphData) error
	// DelGraph deletes a graph from the repository
	DelGraph(ctx context.Context, namespace []types.NameSpace) error
	// SearchNode searches for nodes in the repository
	SearchNode(ctx context.Context, namespace types.NameSpace, nodes []string) (*types.GraphData, error)
}

// GraphContributionRepository is the desired-state graph publishing extension.
// A contribution belongs to one stable chunk and one attempt. Implementations
// must ignore a replace/delete from an attempt older than the latest marker for
// that contribution.
type GraphContributionRepository interface {
	ReplaceGraphContribution(
		ctx context.Context,
		namespace types.NameSpace,
		chunkID string,
		attempt int,
		graph *types.GraphData,
	) (applied bool, err error)
	DeleteGraphContributions(
		ctx context.Context,
		namespace types.NameSpace,
		chunkIDs []string,
		attempt int,
	) error
}
