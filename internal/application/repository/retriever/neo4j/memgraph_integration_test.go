package neo4j

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	driver "github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// Set WEKNORA_MEMGRAPH_URI (and optionally WEKNORA_MEMGRAPH_USERNAME /
// WEKNORA_MEMGRAPH_PASSWORD) to run against a live Memgraph instance.
func TestMemgraphGraphRepository(t *testing.T) {
	uri := os.Getenv("WEKNORA_MEMGRAPH_URI")
	if uri == "" {
		t.Skip("WEKNORA_MEMGRAPH_URI is not set")
	}
	username := os.Getenv("WEKNORA_MEMGRAPH_USERNAME")
	password := os.Getenv("WEKNORA_MEMGRAPH_PASSWORD")
	db, err := driver.NewDriver(uri, driver.BasicAuth(username, password, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := db.VerifyConnectivity(ctx); err != nil {
		t.Fatal(err)
	}

	namespace := types.NameSpace{
		KnowledgeBase: "memgraph_test_" + time.Now().Format("150405000000"),
		Knowledge:     "knowledge",
	}
	repo := NewNeo4jRepository(db).(*Neo4jRepository)
	t.Cleanup(func() {
		if err := repo.DelGraph(context.Background(), []types.NameSpace{namespace}); err != nil {
			t.Errorf("clean up test graph: %v", err)
		}
	})
	graph := &types.GraphData{
		Node: []*types.GraphNode{
			{Name: "source", Attributes: []string{"left"}, Chunks: []string{"chunk-1"}},
			{Name: "target", Attributes: []string{"right"}, Chunks: []string{"chunk-2"}},
		},
		Relation: []*types.GraphRelation{{Node1: "source", Node2: "target", Type: "RELATED_TO"}},
	}
	if err := repo.AddGraph(ctx, namespace, []*types.GraphData{graph}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.SearchNode(ctx, namespace, []string{"source"})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.Relation) != 1 || got.Relation[0].Type != "RELATED_TO" {
		t.Fatalf("unexpected graph search result: %#v", got)
	}
	if err := repo.DelGraph(ctx, []types.NameSpace{namespace}); err != nil {
		t.Fatal(err)
	}
}
