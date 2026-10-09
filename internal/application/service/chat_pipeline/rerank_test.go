package chatpipeline

import (
	"context"
	"errors"
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

type failingRerankModelService struct {
	interfaces.ModelService
}

func (failingRerankModelService) GetRerankModel(context.Context, string) (rerank.Reranker, error) {
	return nil, errors.New("model deleted")
}

// A rerank model that cannot be loaded degrades to retrieval order instead of
// failing the turn, like a failed rerank call.
func TestPluginRerankDegradesWhenModelUnavailable(t *testing.T) {
	plugin := &PluginRerank{modelService: failingRerankModelService{}}
	cm := rerankChatManage(0.3)

	nextCalled := false
	err := plugin.OnEvent(context.Background(), types.CHUNK_RERANK, cm, func() *PluginError {
		nextCalled = true
		return nil
	})
	if err != nil || !nextCalled {
		t.Fatalf("err=%v nextCalled=%v", err, nextCalled)
	}
	if cm.RerankDiagnostics.Outcome != types.RerankOutcomeModelUnavailable || len(cm.SearchResult) != 2 {
		t.Fatalf("diagnostics=%+v search=%d", cm.RerankDiagnostics, len(cm.SearchResult))
	}
}

// Without rerank results the merge input is cut to RerankTopK, best first.
func TestMergeFallbackKeepsRerankTopK(t *testing.T) {
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankTopK: 2},
		PipelineState: types.PipelineState{SearchResult: []*types.SearchResult{
			{ID: "low", Score: 0.1}, {ID: "high", Score: 0.9}, {ID: "mid", Score: 0.5},
		}},
	}
	got := (&PluginMerge{}).selectInputResults(context.Background(), cm)
	if len(got) != 2 || got[0].ID != "high" || got[1].ID != "mid" {
		t.Fatalf("fallback input = %+v", got)
	}
}

// Rerank drops candidates before FILTER_TOP_K ever runs — threshold
// rejection, the candidate cap and the MMR top-k all hide passages. The cut
// has to be recorded here, or a turn with a rerank model reaches the prompt
// with no record that its context is a subset, and a count question reads the
// shown passages as the whole result set.
func TestPluginRerankRecordsEarlyTruncation(t *testing.T) {
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankModelID: "rr-1", RerankTopK: 2, RerankThreshold: 0.3},
		PipelineState: types.PipelineState{
			RewriteQuery: "q",
			SearchResult: []*types.SearchResult{
				{ID: "c1", Content: "first", Score: 0.5},
				{ID: "c2", Content: "second", Score: 0.4},
				{ID: "c3", Content: "third", Score: 0.3},
				{ID: "c4", Content: "fourth", Score: 0.2},
			},
		},
	}
	plugin := &PluginRerank{modelService: &rerankOnlyModelService{
		reranker: &fixedReranker{scores: []float64{0.9, 0.8, 0.7, 0.6}},
	}}

	err := plugin.OnEvent(context.Background(), types.CHUNK_RERANK, cm, func() *PluginError { return nil })
	if err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	if len(cm.RerankResult) != 2 {
		t.Fatalf("rerank result = %d, want 2", len(cm.RerankResult))
	}
	want := types.RetrievalTruncation{Stage: types.RetrievalStageRerank, Candidates: 4}
	if cm.Truncation == nil || *cm.Truncation != want {
		t.Fatalf("truncation = %+v, want %+v", cm.Truncation, want)
	}
}

// A rerank that returned every candidate it received did not hide anything,
// so the prompt must stay free of the subset caveat.
func TestPluginRerankKeepsAllCandidatesWithoutTruncation(t *testing.T) {
	cm := rerankChatManage(0.3)
	plugin := &PluginRerank{modelService: &rerankOnlyModelService{
		reranker: &fixedReranker{scores: []float64{0.9, 0.8}},
	}}

	err := plugin.OnEvent(context.Background(), types.CHUNK_RERANK, cm, func() *PluginError { return nil })
	if err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	if len(cm.RerankResult) != 2 || cm.Truncation != nil {
		t.Fatalf("rerank result = %d, truncation = %+v", len(cm.RerankResult), cm.Truncation)
	}
}

// The merge fallback hides candidates too, and with rerank skipped it is the
// last stage that can say so: FILTER_TOP_K sees the already cut list and finds
// nothing left to truncate.
func TestMergeFallbackRecordsTruncation(t *testing.T) {
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankTopK: 2},
		PipelineState: types.PipelineState{SearchResult: []*types.SearchResult{
			{ID: "low", Score: 0.1}, {ID: "high", Score: 0.9}, {ID: "mid", Score: 0.5},
		}},
	}
	(&PluginMerge{}).selectInputResults(context.Background(), cm)
	want := types.RetrievalTruncation{Stage: types.RetrievalStageMerge, Candidates: 3}
	if cm.Truncation == nil || *cm.Truncation != want {
		t.Fatalf("truncation = %+v, want %+v", cm.Truncation, want)
	}
}

// Graph hits survive the fallback cut, so a cut that only skipped graph hits
// dropped nothing the prompt would miss and must not raise the subset caveat.
func TestMergeFallbackWithoutLossRecordsNothing(t *testing.T) {
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankTopK: 1},
		PipelineState: types.PipelineState{SearchResult: []*types.SearchResult{
			{ID: "high", Score: 0.9},
			{ID: "graph", Score: 0, MatchType: types.MatchTypeGraph},
		}},
	}
	got := (&PluginMerge{}).selectInputResults(context.Background(), cm)
	if len(got) != 2 || cm.Truncation != nil {
		t.Fatalf("fallback input = %d, truncation = %+v", len(got), cm.Truncation)
	}
}
