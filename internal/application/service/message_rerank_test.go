package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type fixedMessageRerankModelService struct {
	interfaces.ModelService
	reranker rerank.Reranker
}

func (s fixedMessageRerankModelService) GetRerankModel(context.Context, string) (rerank.Reranker, error) {
	return s.reranker, nil
}

type fixedMessageReranker struct {
	results []rerank.RankResult
}

func (f fixedMessageReranker) Rerank(context.Context, string, []string) ([]rerank.RankResult, error) {
	return f.results, nil
}

func (f fixedMessageReranker) GetModelName() string { return "fixed" }

func (f fixedMessageReranker) GetModelID() string { return "fixed" }

func TestMessageServiceRerankResultsSkipsNegativeIndexes(t *testing.T) {
	service := &messageService{
		modelService: fixedMessageRerankModelService{reranker: fixedMessageReranker{
			results: []rerank.RankResult{
				{Index: -1, RelevanceScore: 0.99},
				{Index: 0, RelevanceScore: 0.8},
			},
		}},
	}
	config := &types.RetrievalConfig{
		RerankModelID:   "model-1",
		RerankThreshold: 0.1,
		RerankTopK:      2,
	}

	got := service.rerankResults(context.Background(), config, "query", []*types.SearchResult{{ID: "valid", Content: "candidate"}})
	if len(got) != 1 {
		t.Fatalf("rerank returned %d results, want 1", len(got))
	}
	if got[0].ID != "valid" {
		t.Fatalf("rerank returned result ID %q, want %q", got[0].ID, "valid")
	}
}
