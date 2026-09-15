package chatpipeline

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
)

type fixedReranker struct {
	results []rerank.RankResult
}

func (f fixedReranker) Rerank(context.Context, string, []string) ([]rerank.RankResult, error) {
	return f.results, nil
}

func (f fixedReranker) GetModelName() string { return "fixed" }

func (f fixedReranker) GetModelID() string { return "fixed" }

func TestPluginRerankRerankSkipsNegativeIndexes(t *testing.T) {
	plugin := &PluginRerank{}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankThreshold: 0.1},
	}
	candidates := []*types.SearchResult{{ID: "valid", Content: "candidate"}}
	model := fixedReranker{results: []rerank.RankResult{
		{Index: -1, RelevanceScore: 0.99},
		{Index: 0, RelevanceScore: 0.8},
	}}

	got, err := plugin.rerank(context.Background(), chatManage, model, "query", []string{"candidate"}, candidates)
	if err != nil {
		t.Fatalf("rerank returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("rerank returned %d results, want 1", len(got))
	}
	if got[0].Index != 0 {
		t.Fatalf("rerank returned index %d, want 0", got[0].Index)
	}
}

func TestPluginRerankRerankDoesNotFallbackToInvalidIndex(t *testing.T) {
	plugin := &PluginRerank{}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankThreshold: 0.95},
	}
	candidates := []*types.SearchResult{{ID: "valid", Content: "candidate"}}
	model := fixedReranker{results: []rerank.RankResult{
		{Index: -1, RelevanceScore: 0.99},
		{Index: 0, RelevanceScore: 0.1},
	}}

	got, err := plugin.rerank(context.Background(), chatManage, model, "query", []string{"candidate"}, candidates)
	if err != nil {
		t.Fatalf("rerank returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("rerank returned %d results, want 0", len(got))
	}
}
