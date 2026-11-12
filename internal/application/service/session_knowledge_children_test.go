package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type nativeSearchTreeStub struct {
	interfaces.KnowledgeService
	interfaces.KnowledgeTreeService
	roots []types.KnowledgeTreeRoot
}

func (s *nativeSearchTreeStub) GetKnowledgeBatch(
	_ context.Context,
	tenant uint64,
	_ []string,
) ([]*types.Knowledge, error) {
	return []*types.Knowledge{
		{ID: "hit", TenantID: tenant, KnowledgeBaseID: "allowed"},
		{ID: "bad", TenantID: tenant, KnowledgeBaseID: "ungranted"},
	}, nil
}

func (s *nativeSearchTreeStub) KnowledgeChildren(
	_ context.Context,
	roots []types.KnowledgeTreeRoot,
	_ types.KnowledgeChildrenOptions,
) ([]types.KnowledgeChild, error) {
	s.roots = roots
	return []types.KnowledgeChild{
		{KnowledgeID: "child", ParentKnowledgeID: "hit", AnchorKnowledgeID: "hit", Depth: 1},
	}, nil
}

func TestNativeKnowledgeSearchExpansionUsesVerifiedTargets(t *testing.T) {
	stub := &nativeSearchTreeStub{}
	svc := &sessionService{knowledgeService: stub}
	hits := []*types.SearchResult{{ID: "chunk", KnowledgeID: "hit", Score: 0.93}}
	rows, err := svc.expandSearchKnowledgeChildren(
		context.Background(),
		types.SearchTargets{&types.SearchTarget{TenantID: 8, KnowledgeBaseID: "allowed"}},
		hits,
		&types.KnowledgeSearchOptions{ChildrenDepth: 2},
	)
	require.NoError(t, err)
	require.Equal(t, []types.KnowledgeTreeRoot{{TenantID: 8, KBID: "allowed", KnowledgeID: "hit"}}, stub.roots)
	require.Len(t, rows, 1)
	require.Equal(t, 0.93, hits[0].Score)
	require.False(t, rows[0].AlreadyHit)
}
