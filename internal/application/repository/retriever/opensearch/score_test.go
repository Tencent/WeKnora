package opensearch

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// The k-NN plugin reports (1 + cos) / 2; vector hits leave the driver as
// cosine similarity, keyword hits keep their BM25 score.
func TestWrapResultsReportsCosineForVectorHits(t *testing.T) {
	t.Parallel()
	hits := []hit{{ID: "c1", Score: 0.9}}
	hits[0].Source.ChunkID = "c1"

	vector := wrapResults(context.Background(), hits, types.VectorRetrieverType, types.MatchTypeEmbedding)
	if got := vector[0].Results[0].Score; got < 0.8-1e-9 || got > 0.8+1e-9 {
		t.Fatalf("vector score = %v, want cosine 0.8", got)
	}
	keyword := wrapResults(context.Background(), hits, types.KeywordsRetrieverType, types.MatchTypeKeywords)
	if got := keyword[0].Results[0].Score; got != 0.9 {
		t.Fatalf("keyword score = %v, want 0.9 unchanged", got)
	}
}
