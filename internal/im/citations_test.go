package im

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestBuildReplyCitationsUsesOnlyCitedSources(t *testing.T) {
	refs := []*types.SearchResult{
		{
			ID:             "chunk-1",
			KnowledgeID:    "doc-1",
			KnowledgeTitle: "Product Guide",
			Metadata:       map[string]string{types.MetadataKeySourceURL: "https://feishu.cn/wiki/doc-1"},
		},
		{
			ID:             "chunk-2",
			KnowledgeID:    "doc-2",
			KnowledgeTitle: "Uncited Guide",
			Metadata:       map[string]string{types.MetadataKeySourceURL: "https://feishu.cn/wiki/doc-2"},
		},
	}

	answer := `Answer <kb doc="Product Guide" chunk_id="chunk-1" kb_id="kb-1" /> and <kb doc="Product Guide" chunk_id="chunk-1" kb_id="kb-1" />.`
	got := BuildReplyCitations(answer, refs)

	if len(got) != 1 {
		t.Fatalf("got %d citations, want one: %#v", len(got), got)
	}
	if got[0].Label != "S1" || got[0].Title != "Product Guide" || got[0].URL != "https://feishu.cn/wiki/doc-1" {
		t.Fatalf("citation = %#v, want S1/Product Guide/source URL", got[0])
	}
}

func TestBuildReplyCitationsSupportsWebLinksAndRejectsUnsafeURLs(t *testing.T) {
	answer := `<web title="Official docs" url="https://example.com/docs" /> <web title="Local" url="javascript:alert(1)" />`
	got := BuildReplyCitations(answer, nil)

	if len(got) != 1 {
		t.Fatalf("got %d citations, want one safe citation: %#v", len(got), got)
	}
	if got[0].Label != "S1" || got[0].Title != "Official docs" || got[0].URL != "https://example.com/docs" {
		t.Fatalf("citation = %#v, want safe web citation", got[0])
	}
}

func TestFormatReplyCitationsUsesPlatformHeading(t *testing.T) {
	citations := []ReplyCitation{{Label: "S1", Title: "Product [Guide]", URL: "https://example.com/docs?a=1)"}}

	feishu := FormatReplyCitations(PlatformFeishu, citations)
	if feishu != "\n\n引用来源:\n[S1] [Product \\[Guide\\]](https://example.com/docs?a=1%29)" {
		t.Fatalf("Feishu citations = %q", feishu)
	}

	lark := FormatReplyCitations(PlatformLark, citations)
	if lark != "\n\nSources:\n[S1] [Product \\[Guide\\]](https://example.com/docs?a=1%29)" {
		t.Fatalf("Lark citations = %q", lark)
	}
}
