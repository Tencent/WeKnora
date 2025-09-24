package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type RetrieveGraphRepository interface {
	AddGraph(ctx context.Context, namespace types.NameSpace, graphs []*types.GraphData) error
	DelGraph(ctx context.Context, namespace []types.NameSpace) error
	SearchNode(ctx context.Context, namespace types.NameSpace, nodes []string) (*types.GraphData, error)
}
