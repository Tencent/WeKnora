package weaviate

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/weaviate/weaviate-go-client/v6/query"
)

// Weaviate certainty is (1 + cos) / 2; vector hits leave the driver as
// cosine similarity.
func TestParseSearchResultReportsCosine(t *testing.T) {
	t.Parallel()
	certainty := float32(0.9)
	result := &query.Result{Objects: []query.Object[map[string]any]{{
		Metadata: query.Metadata{Certainty: &certainty},
	}}}
	result.Objects[0].Properties = map[string]any{fieldChunkID: "c1", fieldSourceType: int64(2)}
	got := parseSearchResult(result, types.MatchTypeEmbedding)
	if len(got) != 1 || got[0].Score < 0.8-1e-6 || got[0].Score > 0.8+1e-6 {
		t.Fatalf("scores = %+v, want cosine 0.8", got)
	}
	if got[0].ChunkID != "c1" || got[0].SourceType != 2 {
		t.Fatalf("properties = %+v, want chunk c1 and source type 2", got[0])
	}
}
