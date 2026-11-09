package searchutil

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// The default keeps the historic 0.6; WithBaseScore carries the workspace
// retrieval config through, and a non-positive override falls back.
func TestConvertWebSearchResultsBaseScore(t *testing.T) {
	in := []*types.WebSearchResult{{URL: "https://example.com", Title: "t", Snippet: "s"}}

	if got := ConvertWebSearchResults(in)[0].Score; got != types.DefaultWebSearchBaseScore {
		t.Fatalf("default score = %v, want %v", got, types.DefaultWebSearchBaseScore)
	}
	if got := ConvertWebSearchResults(in, WithBaseScore(0.25))[0].Score; got != 0.25 {
		t.Fatalf("override score = %v, want 0.25", got)
	}
	if got := ConvertWebSearchResults(in, WithBaseScore(0))[0].Score; got != types.DefaultWebSearchBaseScore {
		t.Fatalf("zero override score = %v, want default %v", got, types.DefaultWebSearchBaseScore)
	}
}
