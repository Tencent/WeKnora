package langfuse

import (
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestSummarizeRetrieveOutput_countsAndPreviews(t *testing.T) {
	out := SummarizeRetrieveOutput([]*types.RetrieveResult{
		{
			RetrieverEngineType: types.PostgresRetrieverEngineType,
			RetrieverType:       types.VectorRetrieverType,
			Results: []*types.IndexWithScore{
				{ChunkID: "c1", KnowledgeID: "k1", Score: 0.9, Content: "alpha"},
				{ChunkID: "c2", KnowledgeID: "k1", Score: 0.5, Content: "beta"},
			},
		},
		{
			RetrieverEngineType: types.PostgresRetrieverEngineType,
			RetrieverType:       types.KeywordsRetrieverType,
			Results: []*types.IndexWithScore{
				{ChunkID: "c3", KnowledgeID: "k2", Score: 0.7, Content: "gamma"},
			},
		},
	})

	if out["total_hits"] != 3 {
		t.Fatalf("total_hits = %v, want 3", out["total_hits"])
	}
	if out["vector_hits"] != 2 || out["keyword_hits"] != 1 {
		t.Fatalf("vector/keyword hits = %v/%v", out["vector_hits"], out["keyword_hits"])
	}
	hits := out["top_hits"].([]map[string]interface{})
	if len(hits) != 3 || hits[0]["chunk_id"] != "c1" {
		t.Fatalf("unexpected top_hits: %#v", hits)
	}
	if hits[0]["retriever"] != string(types.VectorRetrieverType) ||
		hits[1]["retriever"] != string(types.KeywordsRetrieverType) {
		t.Fatalf("top_hits should retain both retriever groups: %#v", hits)
	}
	if out["top_hits_strategy"] != "round_robin_by_retriever" {
		t.Fatalf("unexpected top_hits strategy: %v", out["top_hits_strategy"])
	}
}

func TestSummarizeRetrieveOutput_doesNotLetOneScoreScaleHideAnotherRetriever(t *testing.T) {
	vector := make([]*types.IndexWithScore, 0, 5)
	keyword := make([]*types.IndexWithScore, 0, 5)
	for i := 0; i < 5; i++ {
		vector = append(vector, &types.IndexWithScore{
			ChunkID:   fmt.Sprintf("vector-%d", i),
			Score:     0.9 - float64(i)*0.1,
			MatchType: types.MatchTypeEmbedding,
		})
		keyword = append(keyword, &types.IndexWithScore{
			ChunkID:   fmt.Sprintf("keyword-%d", i),
			Score:     1.0,
			MatchType: types.MatchTypeKeywords,
		})
	}

	out := SummarizeRetrieveOutput([]*types.RetrieveResult{
		{RetrieverType: types.VectorRetrieverType, Results: vector},
		{RetrieverType: types.KeywordsRetrieverType, Results: keyword},
	})
	hits := out["top_hits"].([]map[string]interface{})
	seen := map[string]bool{}
	for _, hit := range hits {
		seen[hit["retriever"].(string)] = true
	}
	if !seen[string(types.VectorRetrieverType)] || !seen[string(types.KeywordsRetrieverType)] {
		t.Fatalf("top_hits lost a retriever group: %#v", hits)
	}

	groups := out["top_hits_by_retriever"].([]map[string]interface{})
	if len(groups) != 2 {
		t.Fatalf("top_hits_by_retriever groups = %d, want 2", len(groups))
	}
}

func TestSummarizeSearchResults_sortsByScore(t *testing.T) {
	out := SummarizeSearchResults([]*types.SearchResult{
		{ID: "low", Score: 0.2, Content: "low"},
		{ID: "high", Score: 0.9, Content: "high"},
	}, 10)
	hits := out["top_hits"].([]map[string]interface{})
	if len(hits) != 2 || hits[0]["chunk_id"] != "high" {
		t.Fatalf("unexpected hits: %#v", hits)
	}
}
