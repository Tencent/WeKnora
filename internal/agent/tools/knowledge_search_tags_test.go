package tools

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type tagSearchKnowledgeBaseService struct {
	interfaces.KnowledgeBaseService
	params *types.SearchParams
}

func (s *tagSearchKnowledgeBaseService) GetKnowledgeBasesByIDsOnly(context.Context, []string) ([]*types.KnowledgeBase, error) {
	return []*types.KnowledgeBase{{ID: "kb-1", IndexingStrategy: types.DefaultIndexingStrategy()}}, nil
}

func (s *tagSearchKnowledgeBaseService) ResolveEmbeddingModelKeys(context.Context, []*types.KnowledgeBase) map[string]string {
	return nil
}

func (s *tagSearchKnowledgeBaseService) HybridSearch(_ context.Context, _ string, params types.SearchParams) ([]*types.SearchResult, error) {
	s.params = &params
	return nil, nil
}

func TestAgentSearchPreservesDocumentTagPredicates(t *testing.T) {
	for _, ids := range [][]string{nil, {"doc-1"}} {
		svc := &tagSearchKnowledgeBaseService{}
		tool := &KnowledgeSearchTool{knowledgeBaseService: svc}
		target := &types.SearchTarget{
			Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1", TenantID: 100,
			TagIDs: []string{"tag-a", "tag-b"}, KnowledgeIDs: ids, DisableRecallThresholds: true,
		}
		if len(ids) > 0 {
			target.Type = types.SearchTargetTypeKnowledge
		}
		tool.concurrentSearchByTargets(context.Background(), []string{"query"}, types.SearchTargets{target}, 5, 0.6, 0.5, nil)
		require.NotNil(t, svc.params)
		require.Equal(t, target.TagIDs, svc.params.TagIDs)
		require.Equal(t, ids, svc.params.KnowledgeIDs)
		require.Zero(t, svc.params.VectorThreshold)
		require.Zero(t, svc.params.KeywordThreshold)
	}
}
