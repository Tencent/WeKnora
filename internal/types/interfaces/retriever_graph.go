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
	// DelGraphChunks removes only contributions attributed to the supplied
	// chunk IDs, preserving entities still referenced by current chunks.
	DelGraphChunks(ctx context.Context, namespace types.NameSpace, chunkIDs []string) error
	// ReplaceGraphChunk atomically swaps one chunk's graph contribution.
	ReplaceGraphChunk(ctx context.Context, namespace types.NameSpace, chunkID string, graph *types.GraphData) error
	// SearchNode searches for nodes in the repository
	SearchNode(ctx context.Context, namespace types.NameSpace, nodes []string) (*types.GraphData, error)
}
