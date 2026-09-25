package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
)

// scoredReranker returns one canned score per passage, or an error.
type scoredReranker struct {
	scores []float64
	err    error
}

func (r *scoredReranker) Rerank(_ context.Context, _ string, documents []string) ([]rerank.RankResult, error) {
	if r.err != nil {
		return nil, r.err
	}
	out := make([]rerank.RankResult, len(documents))
	for i := range documents {
		out[i] = rerank.RankResult{Index: i, RelevanceScore: r.scores[i]}
	}
	return out, nil
}

func (r *scoredReranker) GetModelName() string { return "scored" }
func (r *scoredReranker) GetModelID() string   { return "scored" }

// rerankModelService serves one reranker (or a load error) on top of
// stubModelService's model lookups.
type rerankModelService struct {
	stubModelService
	reranker rerank.Reranker
	loadErr  error
}

func (s *rerankModelService) GetRerankModel(context.Context, string) (rerank.Reranker, error) {
	return s.reranker, s.loadErr
}

func newRerankModelService() *rerankModelService {
	return &rerankModelService{stubModelService: stubModelService{
		modelsByID: map[string]*types.Model{
			"rr-1":   {ID: "rr-1", Type: types.ModelTypeRerank},
			"chat-1": {ID: "chat-1", Type: types.ModelTypeKnowledgeQA},
		},
		availableModels: []*types.Model{
			{ID: "chat-1", Type: types.ModelTypeKnowledgeQA},
			{ID: "rr-auto", Type: types.ModelTypeRerank},
		},
	}}
}

func TestResolveRerankModelID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	models := newRerankModelService()
	tenantRC := &types.RetrievalConfig{RerankModelID: "rr-tenant"}

	id, source, err := resolveRerankModelID(ctx, models, "rr-1", tenantRC)
	require.NoError(t, err)
	assert.Equal(t, "rr-1", id)
	assert.Equal(t, types.RerankModelSourceRequest, source)

	id, source, err = resolveRerankModelID(ctx, models, "", tenantRC)
	require.NoError(t, err)
	assert.Equal(t, "rr-tenant", id)
	assert.Equal(t, types.RerankModelSourceTenant, source)

	id, source, err = resolveRerankModelID(ctx, models, "", nil)
	require.NoError(t, err)
	assert.Equal(t, "rr-auto", id)
	assert.Equal(t, types.RerankModelSourceAuto, source)

	models.availableModels = nil
	id, source, err = resolveRerankModelID(ctx, models, "", nil)
	require.NoError(t, err)
	assert.Empty(t, id)
	assert.Empty(t, source)

	// A requested model never silently falls back to another one.
	for _, requested := range []string{"missing", "chat-1"} {
		_, _, err = resolveRerankModelID(ctx, models, requested, tenantRC)
		appErr, ok := apperrors.IsAppError(err)
		require.True(t, ok, "requested %q: %v", requested, err)
		assert.Equal(t, apperrors.ErrBadRequest, appErr.Code, "requested %q", requested)
	}
}

func rerankTestCandidates() []*types.SearchResult {
	return []*types.SearchResult{
		{ID: "c1", Content: "first body", Score: 0.9},
		{ID: "c2", Content: "second body", Score: 0.8},
		{ID: "c3", Content: "third body", Score: 0.7},
	}
}

func TestRerankCandidates_reranksAndKeepsModelSource(t *testing.T) {
	t.Parallel()
	models := newRerankModelService()
	models.reranker = &scoredReranker{scores: []float64{0.1, 0.9, 0.5}}
	s := &knowledgeBaseService{modelService: models}
	diag := &types.RerankDiagnostics{ModelSource: types.RerankModelSourceRequest}

	got := s.rerankCandidates(context.Background(), "rr-1", "q", rerankTestCandidates(), 0.3, 2, diag)

	require.Len(t, got, 2)
	assert.Equal(t, "c2", got[0].ID)
	assert.Equal(t, "c3", got[1].ID)
	assert.Equal(t, types.RerankOutcomeOK, diag.Outcome)
	assert.True(t, diag.Applied)
	assert.Equal(t, "rr-1", diag.ModelID)
	assert.Equal(t, types.RerankModelSourceRequest, diag.ModelSource)
	assert.Equal(t, 0.9, diag.TopScore)
}

func TestRerankCandidates_degradesToRetrievalOrder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		modelID string
		model   rerank.Reranker
		loadErr error
		want    types.RerankOutcome
	}{
		{name: "no model", modelID: "", want: types.RerankOutcomeNoModel},
		{
			name: "model unavailable", modelID: "rr-1", loadErr: errors.New("bad credentials"),
			want: types.RerankOutcomeModelUnavailable,
		},
		{
			name: "model error", modelID: "rr-1", model: &scoredReranker{err: errors.New("upstream 500")},
			want: types.RerankOutcomeModelError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			models := newRerankModelService()
			models.reranker, models.loadErr = tt.model, tt.loadErr
			s := &knowledgeBaseService{modelService: models}
			diag := &types.RerankDiagnostics{}

			got := s.rerankCandidates(context.Background(), tt.modelID, "q", rerankTestCandidates(), 0.3, 2, diag)

			require.Len(t, got, 2)
			assert.Equal(t, "c1", got[0].ID)
			assert.Equal(t, "c2", got[1].ID)
			assert.Equal(t, tt.want, diag.Outcome)
			assert.False(t, diag.Applied)
		})
	}
}

func TestApplyKnowledgeSearchOverrides(t *testing.T) {
	t.Parallel()
	base := func() *types.ChatManage {
		return &types.ChatManage{PipelineRequest: types.PipelineRequest{
			EmbeddingTopK: 50, VectorThreshold: 0.15, KeywordThreshold: 0.3, RerankTopK: 10, RerankThreshold: 0.2,
		}}
	}

	cm := base()
	applyKnowledgeSearchOverrides(cm, &types.KnowledgeSearchOptions{})
	assert.Equal(t, base().PipelineRequest, cm.PipelineRequest, "no overrides keep the tenant config")

	vector, keyword, threshold := 0.5, 0.6, -1.5
	cm = base()
	applyKnowledgeSearchOverrides(cm, &types.KnowledgeSearchOptions{
		VectorThreshold: &vector, KeywordThreshold: &keyword, MatchCount: 80,
	})
	assert.Equal(t, 0.5, cm.VectorThreshold)
	assert.Equal(t, 0.6, cm.KeywordThreshold)
	assert.Equal(t, 80, cm.RerankTopK, "match_count is the final result count")
	assert.Equal(t, 80, cm.EmbeddingTopK, "recall must be deep enough to reach match_count")

	cm = base()
	applyKnowledgeSearchOverrides(cm, &types.KnowledgeSearchOptions{
		MatchCount: 20,
		Rerank:     &types.RerankOptions{TopK: 5, Threshold: &threshold},
	})
	assert.Equal(t, 5, cm.RerankTopK, "rerank.top_k wins over match_count")
	assert.Equal(t, -1.5, cm.RerankThreshold)
	assert.Equal(t, 50, cm.EmbeddingTopK)
}
