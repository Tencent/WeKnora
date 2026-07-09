package types

import "testing"

func TestCollectSearchResultChunkIDsIncludesSubChunks(t *testing.T) {
	results := []*SearchResult{
		{ID: "chunk-a", SubChunkID: []string{"chunk-b", "chunk-c"}},
		{ID: "chunk-b", SubChunkID: []string{"chunk-d", "chunk-c"}},
		nil,
		{ID: ""},
	}

	got := CollectSearchResultChunkIDs(results)
	want := []string{"chunk-a", "chunk-b", "chunk-c", "chunk-d"}

	if len(got) != len(want) {
		t.Fatalf("CollectSearchResultChunkIDs() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("CollectSearchResultChunkIDs() = %v, want %v", got, want)
		}
	}
}
