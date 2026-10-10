// Package neo4j implements Bolt/Cypher graph storage for Neo4j and Memgraph.
package neo4j

import (
	"context"
	"fmt"
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
	db, err := driver.NewDriver(uri, memgraphTestAuth())
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

// TestMemgraphDeletesInMoreThanOneBatch stores more relationships than one
// delete batch holds, so the batching loop has to run more than once. A single
// unbounded DELETE would pass the emptiness check too, so this is the test that
// distinguishes "bounded batches" from "one big transaction".
func TestMemgraphDeletesInMoreThanOneBatch(t *testing.T) {
	uri := os.Getenv("WEKNORA_MEMGRAPH_URI")
	if uri == "" {
		t.Skip("WEKNORA_MEMGRAPH_URI is not set")
	}
	db, err := driver.NewDriver(uri, memgraphTestAuth())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cleanupCancel()
		if err := db.Close(cleanupCtx); err != nil {
			t.Errorf("close test driver: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := db.VerifyConnectivity(ctx); err != nil {
		t.Fatal(err)
	}

	namespace := types.NameSpace{
		KnowledgeBase: "memgraph_batch_" + time.Now().Format("150405000000"),
		Knowledge:     "knowledge",
	}
	repo := NewNeo4jRepository(db, EngineMemgraph).(*Neo4jRepository)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cleanupCancel()
		if err := repo.DelGraph(cleanupCtx, []types.NameSpace{namespace}); err != nil {
			t.Errorf("clean up test graph: %v", err)
		}
	})

	// One and a half batches of relationships, so the second batch is a short
	// one and the third comes back empty.
	const relationships = memgraphDeleteBatchSize + memgraphDeleteBatchSize/2
	graph := &types.GraphData{}
	for i := 0; i <= relationships; i++ {
		graph.Node = append(graph.Node, &types.GraphNode{
			Name:   fmt.Sprintf("node-%04d", i),
			Chunks: []string{fmt.Sprintf("chunk-%04d", i)},
		})
	}
	for i := 0; i < relationships; i++ {
		graph.Relation = append(graph.Relation, &types.GraphRelation{
			Node1: fmt.Sprintf("node-%04d", i),
			Node2: fmt.Sprintf("node-%04d", i+1),
			Type:  "RELATED_TO",
		})
	}
	if err := repo.AddGraph(ctx, namespace, []*types.GraphData{graph}); err != nil {
		t.Fatal(err)
	}
	assertNamespaceCount(ctx, t, db, repo.Label(namespace), "nodes", int64(relationships+1))
	assertNamespaceCount(ctx, t, db, repo.Label(namespace), "relationships", int64(relationships))

	if err := repo.DelGraph(ctx, []types.NameSpace{namespace}); err != nil {
		t.Fatal(err)
	}
	assertNamespaceEmpty(ctx, t, db, repo.Label(namespace))
}

// memgraphTestAuth mirrors how the application connects: empty credentials mean
// an unauthenticated connection, which is Memgraph's default configuration.
func memgraphTestAuth() driver.AuthToken {
	username := os.Getenv("WEKNORA_MEMGRAPH_USERNAME")
	password := os.Getenv("WEKNORA_MEMGRAPH_PASSWORD")
	if username == "" && password == "" {
		return driver.NoAuth()
	}
	return driver.BasicAuth(username, password, "")
}
