package langfuse

import (
	"encoding/json"
	"strings"
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

func TestSummarizePreparedRetrieveOutputExcludesIDsAndContent(t *testing.T) {
	const (
		privateChunk     = "chunk-private"
		privateKnowledge = "knowledge-private"
		privateKB        = "kb-private"
		privateContent   = "content-private"
	)
	out := SummarizePreparedRetrieveOutput([]*types.RetrieveResult{
		{
			RetrieverEngineType: types.PostgresRetrieverEngineType,
			RetrieverType:       types.VectorRetrieverType,
			Results: []*types.IndexWithScore{
				{
					ChunkID:         privateChunk,
					KnowledgeID:     privateKnowledge,
					KnowledgeBaseID: privateKB,
					Content:         privateContent,
				},
			},
		},
	})
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal prepared retrieve summary: %v", err)
	}
	payload := string(encoded)
	for _, secret := range []string{
		privateChunk,
		privateKnowledge,
		privateKB,
		privateContent,
	} {
		if strings.Contains(payload, secret) {
			t.Fatalf("prepared retrieve summary leaked %q: %s", secret, payload)
		}
	}
	if out["total_hits"] != 1 || out["vector_hits"] != 1 {
		t.Fatalf("unexpected prepared retrieve counts: %#v", out)
	}
}
