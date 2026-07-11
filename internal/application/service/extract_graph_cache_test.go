package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestGraphExtractionCacheKeyInvalidatesByContentModelAndConfig(t *testing.T) {
	template := &types.PromptTemplateStructured{
		Description: "extract graph",
		Tags:        []string{"person", "org"},
		Examples: []types.GraphData{
			{Text: "example", Node: []*types.GraphNode{{Name: "A"}}},
		},
	}
	contentHash := graphExtractionContentHash("chunk content")
	configHash := graphExtractionConfigHash(template)
	base := graphExtractionCacheKey(contentHash, "chat-a", configHash)
	if base == "" {
		t.Fatal("cache key is empty")
	}
	if got := graphExtractionCacheKey(contentHash, "chat-a", configHash); got != base {
		t.Fatalf("same graph extraction inputs should reuse cache key: %s != %s", got, base)
	}
	if got := graphExtractionCacheKey(graphExtractionContentHash("other content"), "chat-a", configHash); got == base {
		t.Fatal("content changes must invalidate graph extraction cache")
	}
	if got := graphExtractionCacheKey(contentHash, "chat-b", configHash); got == base {
		t.Fatal("chat model changes must invalidate graph extraction cache")
	}
	changedTemplate := &types.PromptTemplateStructured{
		Description: "extract graph",
		Tags:        []string{"person"},
		Examples:    template.Examples,
	}
	if got := graphExtractionCacheKey(contentHash, "chat-a", graphExtractionConfigHash(changedTemplate)); got == base {
		t.Fatal("extraction config changes must invalidate graph extraction cache")
	}
}

func TestGraphExtractionCacheRebindsChunkWithoutMutatingCachedGraph(t *testing.T) {
	cached := &types.GraphData{
		Text: "chunk content",
		Node: []*types.GraphNode{
			{Name: "A", Chunks: []string{"old-chunk"}, Attributes: []string{"kind=person"}},
			{Name: "B"},
		},
		Relation: []*types.GraphRelation{
			{Node1: "A", Type: "knows", Node2: "B"},
		},
	}

	forCache := graphForExtractionCache(cached)
	if len(forCache.Node[0].Chunks) != 0 {
		t.Fatal("cached graph should not retain source chunk bindings")
	}
	if cached.Node[0].Chunks[0] != "old-chunk" {
		t.Fatal("normalizing graph for cache must not mutate original graph")
	}

	bound := bindGraphToChunk(forCache, "new-chunk")
	for _, node := range bound.Node {
		if len(node.Chunks) != 1 || node.Chunks[0] != "new-chunk" {
			t.Fatalf("node %s was not rebound to current chunk: %#v", node.Name, node.Chunks)
		}
	}
	if len(forCache.Node[0].Chunks) != 0 {
		t.Fatal("binding cached graph must not mutate cached graph")
	}
}
