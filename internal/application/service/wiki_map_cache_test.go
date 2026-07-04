package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestWikiMapCacheKeyInvalidatesByContentModelConfigAndPreviousSlugs(t *testing.T) {
	oldSlugs := map[string]bool{
		"entity/a":  true,
		"summary/x": true,
	}
	contentHash := wikiMapContentHash("document content")
	configHash := wikiMapConfigHash("Chinese", types.WikiExtractionStandard, oldSlugs)
	base := wikiMapCacheKey(contentHash, "chat-a", configHash)
	if base == "" {
		t.Fatal("cache key is empty")
	}
	if got := wikiMapCacheKey(contentHash, "chat-a", wikiMapConfigHash("Chinese", types.WikiExtractionStandard, oldSlugs)); got != base {
		t.Fatalf("same wiki map inputs should reuse cache key: %s != %s", got, base)
	}
	if got := wikiMapCacheKey(wikiMapContentHash("other content"), "chat-a", configHash); got == base {
		t.Fatal("document content changes must invalidate wiki map cache")
	}
	if got := wikiMapCacheKey(contentHash, "chat-b", configHash); got == base {
		t.Fatal("chat model changes must invalidate wiki map cache")
	}
	if got := wikiMapCacheKey(contentHash, "chat-a", wikiMapConfigHash("English", types.WikiExtractionStandard, oldSlugs)); got == base {
		t.Fatal("language changes must invalidate wiki map cache")
	}
	if got := wikiMapCacheKey(contentHash, "chat-a", wikiMapConfigHash("Chinese", types.WikiExtractionFocused, oldSlugs)); got == base {
		t.Fatal("granularity changes must invalidate wiki map cache")
	}
	if got := wikiMapCacheKey(contentHash, "chat-a", wikiMapConfigHash("Chinese", types.WikiExtractionStandard, map[string]bool{"entity/b": true})); got == base {
		t.Fatal("previous entity/concept slugs changes must invalidate wiki map cache")
	}
}
