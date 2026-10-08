package weaviate

import (
	"slices"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// Weaviate certainty is (1 + cos) / 2; vector hits leave the driver as
// cosine similarity.
func TestParseGraphQLResponseReportsCosine(t *testing.T) {
	t.Parallel()
	items := []interface{}{
		map[string]interface{}{
			"_additional": map[string]interface{}{"id": "p1", "certainty": 0.9},
			"chunk_id":    "c1",
		},
	}
	got := parseGraphQLResponse(items, "Coll", types.MatchTypeEmbedding)
	if len(got) != 1 || got[0].Score < 0.8-1e-9 || got[0].Score > 0.8+1e-9 {
		t.Fatalf("scores = %+v, want cosine 0.8", got)
	}
}

func TestImageChunkAllowListIsAPositiveFilter(t *testing.T) {
	repo := &weaviateRepository{}
	filter := repo.getBaseFilter(types.RetrieveParams{ChunkIDs: []string{"image-only"}}).Build()
	for _, operand := range filter.Operands {
		if slices.Equal(operand.Path, []string{fieldChunkID}) {
			if operand.Operator != "ContainsAny" || !slices.Equal(operand.ValueTextArray, []string{"image-only"}) {
				t.Fatalf("incorrect positive image filter: %+v", operand)
			}
			return
		}
	}
	t.Fatal("missing positive image chunk restriction")
}
