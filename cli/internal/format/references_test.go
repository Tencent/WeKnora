package format

import (
	"testing"

	sdk "github.com/Tencent/WeKnora/client"
)

func TestIndexReferences_ProjectsLookupFieldsWithoutMutation(t *testing.T) {
	refs := []*sdk.SearchResult{
		{ID: "c1", Content: "bulky passage one", KnowledgeBaseID: "kb1", ParentChunkID: "p1", KnowledgeTitle: "Doc One", Score: 0.5},
		{ID: "c2", Content: "bulky passage two", ParentChunkID: "p2"},
		nil,
	}
	got := IndexReferences(refs, "fallback")

	if len(got) != 2 {
		t.Fatalf("indexes=%d, want 2", len(got))
	}
	if got[0].KBID != "kb1" || got[0].ChunkID != "c1" || got[0].ParentChunkID != "p1" {
		t.Errorf("first index=%+v", got[0])
	}
	if got[1].KBID != "fallback" || got[1].ChunkID != "c2" {
		t.Errorf("fallback index=%+v", got[1])
	}
	if refs[0].Content != "bulky passage one" || refs[1].Content != "bulky passage two" {
		t.Errorf("source references mutated: %+v", refs)
	}
}

func TestIndexReferences_NilSafe(t *testing.T) {
	if got := IndexReferences(nil, ""); len(got) != 0 {
		t.Errorf("nil indexes=%+v", got)
	}
	if got := IndexReferences([]*sdk.SearchResult{nil, nil}, ""); len(got) != 0 {
		t.Errorf("nil-entry indexes=%+v", got)
	}
}
