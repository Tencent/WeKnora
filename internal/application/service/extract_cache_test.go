package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestGraphChunkCacheRoundTrip(t *testing.T) {
	ctx := context.Background()
	cache := newMemoryProcessingCache()
	svc := &ChunkExtractService{cacheRepo: cache}

	graph := &types.GraphData{
		Node: []*types.GraphNode{
			{Name: "WeKnora", Attributes: []string{"project"}},
		},
		Relation: []*types.GraphRelation{
			{Node1: "WeKnora", Type: "uses", Node2: "GraphRAG"},
		},
	}
	key := processingCacheKey("chunk", "model", graphChunkVersion)
	svc.putGraphChunkCache(ctx, 7, key, graph, map[string]string{"model_id": "model"})

	got, ok := svc.getGraphChunkCache(ctx, 7, key)
	if !ok {
		t.Fatal("expected graph cache hit")
	}
	if len(got.Node) != 1 || got.Node[0].Name != "WeKnora" {
		t.Fatalf("unexpected cached graph nodes: %#v", got.Node)
	}
	if len(got.Relation) != 1 || got.Relation[0].Type != "uses" {
		t.Fatalf("unexpected cached graph relations: %#v", got.Relation)
	}

	if _, ok := svc.getGraphChunkCache(ctx, 8, key); ok {
		t.Fatal("cache must be tenant scoped")
	}
}
