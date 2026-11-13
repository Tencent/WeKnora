package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type treeSearchKBService struct {
	searchKBService
	called  bool
	options types.KnowledgeChildrenOptions
	targets types.SearchTargets
}

func (s *treeSearchKBService) ExpandKnowledgeRerankCandidates(
	_ context.Context, targets types.SearchTargets, hits []*types.SearchResult, opts types.KnowledgeChildrenOptions,
) ([]*types.SearchResult, *types.KnowledgeTreeDiagnostics, error) {
	s.called, s.options, s.targets = true, opts, targets
	result := append([]*types.SearchResult(nil), hits...)
	result = append(result,
		&types.SearchResult{
			ID: "parent-chunk", KnowledgeID: "parent", Content: "parent evidence",
			MatchType: types.MatchTypeKnowledgeTree,
		},
		&types.SearchResult{
			ID: "sibling-chunk", KnowledgeID: "sibling", Content: "sibling evidence",
			MatchType: types.MatchTypeKnowledgeTree,
		})
	return result, &types.KnowledgeTreeDiagnostics{AddedKnowledge: 2, AddedPassages: 2}, nil
}

func TestSearchKnowledgeTreeCandidatesReachOneRerankBeforeTopK(t *testing.T) {
	models := newRerankModelService()
	models.reranker = &scoredReranker{scores: []float64{0.1, 0.99, 0.9}}
	hit := &types.SearchResult{ID: "child-chunk", KnowledgeID: "child", Content: "child evidence", Score: 0.8}
	svc := newSearchKnowledgeService(t, &types.RetrievalConfig{RerankModelID: "rr-1"},
		models, &fakeRetrieval{results: []*types.SearchResult{hit}})
	tree := &treeSearchKBService{}
	svc.knowledgeBaseService = tree
	got, err := svc.SearchKnowledge(searchKnowledgeCtx(), []string{"kb-1"}, nil, nil, "question",
		&types.KnowledgeSearchOptions{ChildrenDepth: 2, ChildrenLimit: 10, MatchCount: 2})
	require.NoError(t, err)
	require.True(t, tree.called)
	require.Equal(t, types.KnowledgeChildrenOptions{Depth: 2, Limit: 10}, tree.options)
	require.Equal(t, "kb-1", tree.targets[0].KnowledgeBaseID)
	require.Equal(t, 3, got.Meta.Rerank.CandidateCount)
	require.True(t, got.Meta.Rerank.Applied)
	require.Equal(t, 2, got.Meta.Tree.AddedKnowledge)
	require.True(t, got.Meta.Tree.Applied)
	require.Len(t, got.Results, 2)
	require.Equal(t, "parent", got.Results[0].KnowledgeID)
	require.Equal(t, "sibling", got.Results[1].KnowledgeID)
	require.Equal(t, "0.0000", got.Results[0].Metadata["base_score"])
	require.Zero(t, got.Results[0].VectorScore)
	require.Equal(t, 0.8, hit.Score, "expansion and scoring must not mutate the original hit")
	require.Empty(t, got.Expanded, "there is no second, unranked expansion after TopK")
}

func TestSearchKnowledgeTreeDefaultDoesNotLookup(t *testing.T) {
	models := newRerankModelService()
	models.reranker = &scoredReranker{scores: []float64{0.9}}
	svc := newSearchKnowledgeService(t, &types.RetrievalConfig{RerankModelID: "rr-1"},
		models, &fakeRetrieval{results: []*types.SearchResult{{ID: "child", Content: "evidence", Score: 0.8}}})
	tree := &treeSearchKBService{}
	svc.knowledgeBaseService = tree
	got, err := svc.SearchKnowledge(searchKnowledgeCtx(), []string{"kb-1"}, nil, nil, "question", nil)
	require.NoError(t, err)
	require.False(t, tree.called)
	require.Nil(t, got.Meta.Tree)
	require.Len(t, got.Results, 1)
}

func TestSearchKnowledgeTreeRerankErrorKeepsOnlyOriginals(t *testing.T) {
	models := newRerankModelService()
	models.reranker = &scoredReranker{err: errors.New("provider unavailable")}
	hit := &types.SearchResult{ID: "child", Content: "original evidence", Score: .8}
	svc := newSearchKnowledgeService(t, &types.RetrievalConfig{RerankModelID: "rr-1"},
		models, &fakeRetrieval{results: []*types.SearchResult{hit}})
	svc.knowledgeBaseService = &treeSearchKBService{}
	got, err := svc.SearchKnowledge(searchKnowledgeCtx(), []string{"kb-1"}, nil, nil, "question",
		&types.KnowledgeSearchOptions{ChildrenDepth: 1, MatchCount: 20})
	require.NoError(t, err)
	require.Equal(t, types.RerankOutcomeModelError, got.Meta.Rerank.Outcome)
	require.False(t, got.Meta.Tree.Applied)
	require.Equal(t, []*types.SearchResult{hit}, got.Results)
}

func TestSearchKnowledgeTreeDisabledDoesNotLookup(t *testing.T) {
	models := newRerankModelService()
	hit := &types.SearchResult{ID: "child", Content: "original evidence", Score: .8}
	svc := newSearchKnowledgeService(t, nil, models, &fakeRetrieval{results: []*types.SearchResult{hit}})
	tree := &treeSearchKBService{}
	svc.knowledgeBaseService = tree
	disabled := false
	got, err := svc.SearchKnowledge(searchKnowledgeCtx(), []string{"kb-1"}, nil, nil, "question",
		&types.KnowledgeSearchOptions{ChildrenDepth: 1, Rerank: &types.RerankOptions{Enabled: &disabled}})
	require.NoError(t, err)
	require.False(t, tree.called)
	require.NotNil(t, got.Meta.Tree)
	require.False(t, got.Meta.Tree.Applied)
	require.Equal(t, []*types.SearchResult{hit}, got.Results)
}

func TestSearchKnowledgeTreeUnavailableModelReportsSkippedExpansion(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		models := newRerankModelService()
		models.availableModels = nil
		var config *types.RetrievalConfig
		want := types.RerankOutcomeNoModel
		if unavailable {
			config = &types.RetrievalConfig{RerankModelID: "rr-1"}
			models.loadErr = errors.New("model unavailable")
			want = types.RerankOutcomeModelUnavailable
		}
		hit := &types.SearchResult{ID: "child", Content: "original evidence", Score: .8}
		svc := newSearchKnowledgeService(t, config, models, &fakeRetrieval{results: []*types.SearchResult{hit}})
		tree := &treeSearchKBService{}
		svc.knowledgeBaseService = tree
		got, err := svc.SearchKnowledge(searchKnowledgeCtx(), []string{"kb-1"}, nil, nil, "question",
			&types.KnowledgeSearchOptions{ChildrenDepth: 1})
		require.NoError(t, err)
		require.False(t, tree.called)
		require.NotNil(t, got.Meta.Tree)
		require.False(t, got.Meta.Tree.Applied)
		require.Zero(t, got.Meta.Tree.AddedPassages)
		require.Equal(t, want, got.Meta.Rerank.Outcome)
		require.Equal(t, []*types.SearchResult{hit}, got.Results)
	}
}

func TestNativeKnowledgeHybridEnrichmentKeepsScoredTreePassage(t *testing.T) {
	ks, db := newKnowledgeSharedAccessService(t, nil)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}))
	k := &types.Knowledge{
		ID: "parent", TenantID: 1, KnowledgeBaseID: "kb", Type: "file", ParseStatus: types.ParseStatusCompleted,
	}
	seedKnowledge(t, db, k)
	chunk := &types.Chunk{
		ID: "parent-chunk", TenantID: 1, KnowledgeID: k.ID, KnowledgeBaseID: "kb",
		Content: "stored parent evidence", ChunkType: types.ChunkTypeText, IndexStatus: "ready", IsEnabled: true,
	}
	require.NoError(t, db.Create(chunk).Error)
	svc := &knowledgeBaseService{kgRepo: ks.repo, chunkRepo: repository.NewChunkRepository(db)}
	pool := []*types.IndexWithScore{{
		ChunkID: chunk.ID, KnowledgeID: k.ID, KnowledgeBaseID: "kb", MatchType: types.MatchTypeKnowledgeTree,
	}}
	scored := &types.SearchResult{
		ID: chunk.ID, KnowledgeID: k.ID, KnowledgeBaseID: "kb", Content: chunk.Content,
		Score: .91, MatchType: types.MatchTypeKnowledgeTree, Metadata: map[string]string{"model_score": ".9"},
	}
	rows, err := svc.enrichRerankedResults(searchKnowledgeCtx(), pool, []*types.SearchResult{scored})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Same(t, scored, rows[0], "pool hydration must not replace model scores or lose new tree IDs")
	require.Equal(t, .91, rows[0].Score)
}
