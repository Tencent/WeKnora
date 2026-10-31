// The Neo4j counterpart of the Memgraph integration test: it exercises the APOC
// import and apoc.periodic.iterate deletion that existing deployments run.
// It needs a Neo4j with APOC installed, so it has its own environment variable:
// WEKNORA_NEO4J_TEST_URI drives the search regression and must keep working
// against a plain Neo4j without plugins.
package neo4j

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// Set WEKNORA_NEO4J_APOC_URI (and optionally WEKNORA_NEO4J_USERNAME /
// WEKNORA_NEO4J_PASSWORD) to run against a live Neo4j that has APOC installed.
func TestNeo4jGraphRepositoryWithAPOC(t *testing.T) {
	uri := os.Getenv("WEKNORA_NEO4J_APOC_URI")
	if uri == "" {
		t.Skip("WEKNORA_NEO4J_APOC_URI is not set")
	}
	username := os.Getenv("WEKNORA_NEO4J_USERNAME")
	password := os.Getenv("WEKNORA_NEO4J_PASSWORD")
	auth := neo4j.NoAuth()
	if username != "" || password != "" {
		auth = neo4j.BasicAuth(username, password, "")
	}
	db, err := neo4j.NewDriver(uri, auth)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if err := db.Close(cleanupCtx); err != nil {
			t.Errorf("close test driver: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := db.VerifyConnectivity(ctx); err != nil {
		t.Fatal(err)
	}

	namespace := types.NameSpace{
		KnowledgeBase: "neo4j_apoc_" + time.Now().Format("150405000000"),
		Knowledge:     "knowledge",
	}
	repo := NewNeo4jRepository(db, EngineNeo4j).(*Neo4jRepository)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
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
	// apoc.coll.union merges chunk lists, so importing the same graph twice must
	// not duplicate nodes or relationships.
	if err := repo.AddGraph(ctx, namespace, []*types.GraphData{graph}); err != nil {
		t.Fatal(err)
	}
	assertNamespaceCount(ctx, t, db, repo.Label(namespace), "nodes", 2)
	assertNamespaceCount(ctx, t, db, repo.Label(namespace), "relationships", 1)

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
	assertNamespaceEmpty(ctx, t, db, repo.Label(namespace))
}
