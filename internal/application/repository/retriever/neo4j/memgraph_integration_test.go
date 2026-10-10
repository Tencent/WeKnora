// Package neo4j implements Bolt/Cypher graph storage for Neo4j and Memgraph.
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
	// Cleanups run in reverse registration order, so close the driver last.
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if err := db.Close(cleanupCtx); err != nil {
			t.Errorf("close test driver: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := db.VerifyConnectivity(ctx); err != nil {
		t.Fatal(err)
	}

	namespace := types.NameSpace{
		KnowledgeBase: "memgraph_test_" + time.Now().Format("150405000000"),
		Knowledge:     "knowledge",
	}
	repo := NewNeo4jRepository(db, EngineMemgraph).(*Neo4jRepository)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if err := repo.DelGraph(cleanupCtx, []types.NameSpace{namespace}); err != nil {
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
	// Deletion has to leave the namespace empty: the batched delete stops when
	// a batch comes back empty, so a surviving node or relationship means the
	// loop stopped early, not that it had nothing to do.
	assertNamespaceEmpty(ctx, t, db, repo.Label(namespace))
}

// assertNamespaceEmpty counts what is left under a label after a deletion.
// Relationships are counted without a label filter on the far endpoint so an
// edge left dangling to another namespace still shows up.
func assertNamespaceEmpty(ctx context.Context, t *testing.T, db driver.Driver, label string) {
	t.Helper()
	for _, probe := range []struct {
		what  string
		query string
	}{
		{what: "nodes", query: "MATCH (n:" + label + ") RETURN count(n) AS remaining"},
		{what: "relationships", query: "MATCH (n:" + label + ")-[r]-() RETURN count(r) AS remaining"},
	} {
		result, err := driver.ExecuteQuery(ctx, db, probe.query, nil, driver.EagerResultTransformer)
		if err != nil {
			t.Fatalf("count remaining %s: %v", probe.what, err)
		}
		if len(result.Records) != 1 {
			t.Fatalf("counting %s returned %d records, want 1", probe.what, len(result.Records))
		}
		remaining, _, err := driver.GetRecordValue[int64](result.Records[0], "remaining")
		if err != nil {
			t.Fatalf("decode remaining %s: %v", probe.what, err)
		}
		if remaining != 0 {
			t.Errorf("%d %s survived DelGraph", remaining, probe.what)
		}
	}
}
