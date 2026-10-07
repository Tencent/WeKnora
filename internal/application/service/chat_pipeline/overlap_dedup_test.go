package chatpipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// Near-duplicates of similar size collapse to the better-scored one, but a
// short chunk is not dropped just because a much longer text on the same
// topic happens to contain most of its words.
func TestRemovePartialOverlapsSizeGuard(t *testing.T) {
	near := []*types.SearchResult{
		{ID: "a", Content: "alpha beta gamma delta epsilon zeta eta theta", Score: 0.9},
		{ID: "b", Content: "alpha beta gamma delta epsilon zeta eta theta iota", Score: 0.5},
	}
	if got := removePartialOverlaps(context.Background(), near); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("near-duplicates not collapsed: %+v", got)
	}

	var page strings.Builder
	for i := 0; i < 20; i++ {
		page.WriteString("alpha beta gamma delta epsilon zeta eta theta kappa lambda mu nu xi omicron pi rho ")
		page.WriteString("filler" + string(rune('a'+i)) + " ")
	}
	mixed := []*types.SearchResult{
		{ID: "chunk", Content: "alpha beta gamma delta epsilon zeta eta theta sigma", Score: 0.4},
		{ID: "page", Content: page.String() + " word1 word2 word3 word4 word5 word6 word7 word8 word9 word10" +
			" word11 word12 word13 word14 word15 word16 word17 word18 word19 word20", Score: 0.9},
	}
	if got := removePartialOverlaps(context.Background(), mixed); len(got) != 2 {
		t.Fatalf("short chunk dropped against a much longer text: %+v", got)
	}
}
