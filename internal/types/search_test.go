package types

import (
	"reflect"
	"testing"
)

func TestCollectSearchResultChunkIDsSkipsSyntheticResults(t *testing.T) {
	got := CollectSearchResultChunkIDs([]*SearchResult{
		{ID: "chunk-a", MatchType: MatchTypeEmbedding, SubChunkID: []string{"chunk-a-sub", "chunk-a-sub"}},
		{ID: "web-result", MatchType: MatchTypeWebSearch, SubChunkID: []string{"web-sub"}},
		{ID: "data-result", MatchType: MatchTypeDataAnalysis},
		{ID: "history-result", MatchType: MatchTypeHistory},
		{ID: "chunk-b", MatchType: MatchTypeKeywords},
		{ID: "chunk-a", MatchType: MatchTypeEmbedding},
		nil,
	})

	want := []string{"chunk-a", "chunk-a-sub", "chunk-b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CollectSearchResultChunkIDs() = %#v, want %#v", got, want)
	}
}

func TestReferencesScanAcceptsSQLiteTextString(t *testing.T) {
	var refs References
	if err := refs.Scan(`[{"id":"chunk-1","knowledge_base_id":"kb-1"}]`); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(refs) != 1 || refs[0].ID != "chunk-1" || refs[0].KnowledgeBaseID != "kb-1" {
		t.Fatalf("refs = %#v, want chunk-1/kb-1", refs)
	}
}
