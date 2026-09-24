package chatpipeline

import (
	"context"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type fixedReranker struct{ scores []float64 }

func (r *fixedReranker) Rerank(_ context.Context, _ string, documents []string) ([]rerank.RankResult, error) {
	out := make([]rerank.RankResult, len(documents))
	for i := range documents {
		out[i] = rerank.RankResult{Index: i, RelevanceScore: r.scores[i]}
	}
	return out, nil
}

func (r *fixedReranker) GetModelName() string { return "fixed" }
func (r *fixedReranker) GetModelID() string   { return "fixed" }

type rerankOnlyModelService struct {
	interfaces.ModelService
	reranker rerank.Reranker
}

func (s *rerankOnlyModelService) GetRerankModel(context.Context, string) (rerank.Reranker, error) {
	return s.reranker, nil
}

func rerankChatManage(threshold float64) *types.ChatManage {
	return &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankModelID: "rr-1", RerankTopK: 5, RerankThreshold: threshold},
		PipelineState: types.PipelineState{
			RewriteQuery: "q",
			SearchResult: []*types.SearchResult{
				{ID: "c1", Content: "first", Score: 0.5},
				{ID: "c2", Content: "second", Score: 0.4},
			},
		},
	}
}

func TestPluginRerankRecordsDiagnostics(t *testing.T) {
	plugin := &PluginRerank{modelService: &rerankOnlyModelService{
		reranker: &fixedReranker{scores: []float64{0.2, 0.8}},
	}}
	cm := rerankChatManage(0.3)

	next := func() *PluginError { return nil }
	if err := plugin.OnEvent(context.Background(), types.CHUNK_RERANK, cm, next); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	if len(cm.RerankResult) != 1 || cm.RerankResult[0].ID != "c2" {
		t.Fatalf("rerank result = %+v", cm.RerankResult)
	}
	d := cm.RerankDiagnostics
	if d == nil || d.Outcome != types.RerankOutcomeOK || d.ModelID != "rr-1" ||
		d.TopScore != 0.8 || d.ResultCount != 1 {
		t.Fatalf("diagnostics = %+v", d)
	}
}

// An all-rejecting rerank ends the search with ErrSearchNothing, and the
// diagnostics say why so the knowledge-search API can explain the empty
// result instead of returning a bare [].
func TestPluginRerankExplainsEmptyResult(t *testing.T) {
	plugin := &PluginRerank{modelService: &rerankOnlyModelService{
		reranker: &fixedReranker{scores: []float64{0.05, 0.1}},
	}}
	cm := rerankChatManage(0.3)

	err := plugin.OnEvent(context.Background(), types.CHUNK_RERANK, cm, func() *PluginError { return nil })
	if err != ErrSearchNothing {
		t.Fatalf("expected ErrSearchNothing, got %v", err)
	}
	d := cm.RerankDiagnostics
	if d == nil || d.Outcome != types.RerankOutcomeAllBelowThreshold || d.TopScore != 0.1 || d.CandidateCount != 2 {
		t.Fatalf("diagnostics = %+v", d)
	}
}

func TestPluginRerankWithoutModelReportsNoModel(t *testing.T) {
	cm := rerankChatManage(0.3)
	cm.RerankModelID = ""
	nextCalled := false
	err := (&PluginRerank{}).OnEvent(context.Background(), types.CHUNK_RERANK, cm, func() *PluginError {
		nextCalled = true
		return nil
	})
	if err != nil || !nextCalled {
		t.Fatalf("err=%v next=%v", err, nextCalled)
	}
	if cm.RerankDiagnostics == nil || cm.RerankDiagnostics.Outcome != types.RerankOutcomeNoModel {
		t.Fatalf("diagnostics = %+v", cm.RerankDiagnostics)
	}
}

// recordingSearchKnowledgeBaseService records every HybridSearch call.
type recordingSearchKnowledgeBaseService struct {
	interfaces.KnowledgeBaseService
	mu         sync.Mutex
	params     []types.SearchParams
	embedCalls int
}

func (s *recordingSearchKnowledgeBaseService) GetKnowledgeBasesByIDsOnly(
	context.Context, []string,
) ([]*types.KnowledgeBase, error) {
	return []*types.KnowledgeBase{{
		ID: "kb-1", Type: types.KnowledgeBaseTypeDocument, EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.DefaultIndexingStrategy(),
	}}, nil
}

func (s *recordingSearchKnowledgeBaseService) ResolveEmbeddingModelKeys(
	context.Context, []*types.KnowledgeBase,
) map[string]string {
	return map[string]string{"kb-1": "model|endpoint"}
}

func (s *recordingSearchKnowledgeBaseService) GetQueryEmbedding(context.Context, string, string) ([]float32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.embedCalls++
	return []float32{0.1}, nil
}

func (s *recordingSearchKnowledgeBaseService) HybridSearch(
	_ context.Context, _ string, params types.SearchParams,
) ([]*types.SearchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.params = append(s.params, params)
	return []*types.SearchResult{{ID: "c1", Content: "hit", Score: 0.5}}, nil
}

func TestSearchHonorsRecallPathSwitches(t *testing.T) {
	tests := []struct {
		name          string
		disableVector bool
		disableKw     bool
		wantEmbed     int
	}{
		{name: "vector only", disableKw: true, wantEmbed: 1},
		{name: "keyword only", disableVector: true, wantEmbed: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &recordingSearchKnowledgeBaseService{}
			plugin := &PluginSearch{knowledgeBaseService: svc}
			cm := &types.ChatManage{
				PipelineRequest: types.PipelineRequest{
					SearchTargets: types.SearchTargets{{
						Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1",
					}},
					EmbeddingTopK:        10,
					DisableVectorMatch:   tt.disableVector,
					DisableKeywordsMatch: tt.disableKw,
				},
				PipelineState: types.PipelineState{RewriteQuery: "q"},
			}
			if err := plugin.OnEvent(context.Background(), types.CHUNK_SEARCH, cm, func() *PluginError {
				return nil
			}); err != nil {
				t.Fatalf("OnEvent: %v", err)
			}
			if len(svc.params) != 1 {
				t.Fatalf("expected one HybridSearch call, got %d", len(svc.params))
			}
			p := svc.params[0]
			if p.DisableVectorMatch != tt.disableVector || p.DisableKeywordsMatch != tt.disableKw {
				t.Fatalf("params = %+v", p)
			}
			if svc.embedCalls != tt.wantEmbed {
				t.Fatalf("embedding calls = %d, want %d", svc.embedCalls, tt.wantEmbed)
			}
		})
	}
}
