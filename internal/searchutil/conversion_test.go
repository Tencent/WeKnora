package searchutil

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestConvertWebSearchResults(t *testing.T) {
	if got := ConvertWebSearchResults(nil); len(got) != 0 {
		t.Errorf("nil input should give no results, got %d", len(got))
	}

	publishedAt := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	results := ConvertWebSearchResults([]*types.WebSearchResult{
		nil,
		{
			Title:       "Title",
			URL:         "https://example.com/page",
			Snippet:     "snippet",
			Content:     "full content",
			Source:      "duckduckgo",
			PublishedAt: &publishedAt,
		},
		{Title: "No URL", Snippet: "only a snippet"},
	}, WithSeqFunc(func(idx int) int { return idx * 10 }))

	if len(results) != 2 {
		t.Fatalf("expected 2 results (nil entry skipped), got %d", len(results))
	}

	first := results[0]
	if first.ID != "https://example.com/page" || first.KnowledgeID != "https://example.com/page" {
		t.Errorf("ID/KnowledgeID should be the URL, got %q / %q", first.ID, first.KnowledgeID)
	}
	if first.Content != "Title\n\nsnippet\n\nfull content" {
		t.Errorf("Content = %q, want title+snippet+content joined by blank lines", first.Content)
	}
	if first.KnowledgeTitle != "Title" || first.Score != 0.6 || first.Seq != 10 {
		t.Errorf("unexpected fields: KnowledgeTitle=%q Score=%v Seq=%d", first.KnowledgeTitle, first.Score, first.Seq)
	}
	if first.MatchType != types.MatchTypeWebSearch || first.ChunkType != string(types.ChunkTypeWebSearch) {
		t.Errorf("unexpected type fields: MatchType=%v ChunkType=%q", first.MatchType, first.ChunkType)
	}
	if first.KnowledgeSource != "web_search" {
		t.Errorf("KnowledgeSource = %q, want web_search", first.KnowledgeSource)
	}
	if first.EndAt != len([]rune(first.Content)) {
		t.Errorf("EndAt = %d, want rune count %d", first.EndAt, len([]rune(first.Content)))
	}
	if first.Metadata["url"] != "https://example.com/page" || first.Metadata["source"] != "duckduckgo" ||
		first.Metadata["title"] != "Title" || first.Metadata["snippet"] != "snippet" {
		t.Errorf("unexpected metadata: %v", first.Metadata)
	}
	if first.Metadata["published_at"] != "2026-11-26T10:00:00Z" {
		t.Errorf("published_at = %q, want RFC3339", first.Metadata["published_at"])
	}

	second := results[1]
	if second.ID != "web_search_2" {
		t.Errorf("empty URL should fall back to web_search_<index>, got %q", second.ID)
	}
	if second.Content != "No URL\n\nonly a snippet" {
		t.Errorf("Content = %q, want title+snippet", second.Content)
	}
	if second.Seq != 20 {
		t.Errorf("Seq = %d, want 20 from the custom seq func", second.Seq)
	}
	if _, ok := second.Metadata["published_at"]; ok {
		t.Errorf("no PublishedAt should mean no published_at metadata, got %v", second.Metadata)
	}
}

func TestConvertWebSearchResults_DefaultSeqIsOne(t *testing.T) {
	results := ConvertWebSearchResults([]*types.WebSearchResult{{Title: "T", URL: "https://example.com"}})
	if len(results) != 1 || results[0].Seq != 1 {
		t.Fatalf("default Seq should be 1, got %+v", results)
	}
}
