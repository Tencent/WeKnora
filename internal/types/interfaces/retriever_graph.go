package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// RetrieveGraphRepository is a repository for retrieving graphs
type RetrieveGraphRepository interface {
	// AddGraph adds a graph to the repository
	AddGraph(ctx context.Context, namespace types.NameSpace, graphs []*types.GraphData) error
	// FenceGraphAttempt prevents older extraction tasks from publishing after reconciliation starts.
	FenceGraphAttempt(ctx context.Context, namespace types.NameSpace, attempt int) error
	// RecoverGraphNamespace completes an interrupted full namespace deletion.
	RecoverGraphNamespace(ctx context.Context, namespace types.NameSpace) error
	// ReplaceGraphChunk atomically replaces one chunk's graph ownership.
	ReplaceGraphChunk(
		ctx context.Context,
		namespace types.NameSpace,
		chunkID string,
		attempt int,
		graph *types.GraphData,
	) error
	// DelGraphChunks retracts graph ownership for removed chunks.
	DelGraphChunks(ctx context.Context, namespace types.NameSpace, chunkIDs []string) error
	// DelGraph deletes a graph from the repository
	DelGraph(ctx context.Context, namespace []types.NameSpace) error
	// SearchNode searches for nodes in the repository
	SearchNode(ctx context.Context, namespace types.NameSpace, nodes []string) (*types.GraphData, error)
}
