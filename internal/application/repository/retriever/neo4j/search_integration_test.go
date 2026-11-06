package neo4j

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// integrationDriver connects to the disposable Neo4j used by integration
// tests. Authentication is optional so the same tests run against both an
// auth-disabled instance and the compose service (neo4j/password).
func integrationDriver(t *testing.T) neo4j.Driver {
	uri := os.Getenv("WEKNORA_NEO4J_TEST_URI")
	if uri == "" {
		t.Skip("set WEKNORA_NEO4J_TEST_URI to run the live Neo4j regression")
	}
	auth := neo4j.NoAuth()
	user := os.Getenv("WEKNORA_NEO4J_TEST_USERNAME")
	password := os.Getenv("WEKNORA_NEO4J_TEST_PASSWORD")
	if user != "" || password != "" {
		auth = neo4j.BasicAuth(user, password, "")
	}
	driver, err := neo4j.NewDriver(uri, auth)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, driver.Close(context.Background())) })
	return driver
}

// Run against a disposable Neo4j instance:
//
//	WEKNORA_NEO4J_TEST_URI=bolt://127.0.0.1:7687
//	WEKNORA_NEO4J_TEST_USERNAME / WEKNORA_NEO4J_TEST_PASSWORD (optional, for an authenticated instance)
//	go test ./internal/application/repository/retriever/neo4j -run Integration
func TestSearchNodeIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	driver := integrationDriver(t)
	require.NoError(t, driver.VerifyConnectivity(ctx))
	repo := NewNeo4jRepository(driver).(*Neo4jRepository)
	namespace := types.NameSpace{KnowledgeBase: fmt.Sprintf("graph_regression_%d", time.Now().UnixNano())}
	label := repo.Label(namespace)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, cleanupErr := neo4j.ExecuteQuery(cleanupCtx, driver, "MATCH (n:"+label+") DETACH DELETE n",
			nil, neo4j.EagerResultTransformer)
		assert.NoError(t, cleanupErr)
	})
	_, err := neo4j.ExecuteQuery(ctx, driver, `
		CREATE (a1:`+label+` {name: 'Acme', kg: 'doc1', chunks: ['c1'], attributes: ['doc1 attribute']}),
		       (b1:`+label+` {name: 'Shanghai', kg: 'doc1', chunks: ['c1'], attributes: []}),
		       (a2:`+label+` {name: 'Acme', kg: 'doc2', chunks: ['c2'], attributes: ['doc2 attribute']}),
		       (b2:`+label+` {name: 'Shanghai', kg: 'doc2', chunks: ['c2'], attributes: []}),
		       (a1)-[:HEADQUARTERED_IN]->(b1), (a2)-[:HAS_BRANCH_IN]->(b2),
		       (b1)-[:RELATED_TO]->(a1), (a1)-[:SELF]->(a1)
	`, nil, neo4j.EagerResultTransformer)
	require.NoError(t, err)
	t.Run("deduplicate before the row cap", func(t *testing.T) {
		query, params := graphSearchCypher(label, []string{"Acme", "Shanghai"})
		params["maxRows"] = 5 // Four stored edges; duplicate endpoint matches must not fill this fifth slot.
		result, queryErr := neo4j.ExecuteQuery(ctx, driver, query, params, neo4j.EagerResultTransformer)
		require.NoError(t, queryErr)
		require.Len(t, result.Records, 4)
		params["maxRows"] = 1
		result, queryErr = neo4j.ExecuteQuery(ctx, driver, query, params, neo4j.EagerResultTransformer)
		require.NoError(t, queryErr)
		require.Len(t, result.Records, 1)
	})
	for _, seeds := range [][]string{{"Shanghai"}, {"Acme"}, {"Acme", "Shanghai"}} {
		t.Run(fmt.Sprint(seeds), func(t *testing.T) {
			graph, searchErr := repo.SearchNode(ctx, namespace, seeds)
			require.NoError(t, searchErr)
			require.Len(t, graph.Node, 4)
			assert.Equal(t, seeds[0], graph.Node[0].Name, "the best seed still supplies evidence first")
			var sources, attributes []string
			for _, node := range graph.Node {
				if node.Name == "Acme" {
					sources = append(sources, node.KnowledgeID+":"+node.Chunks[0])
					attributes = append(attributes, node.Attributes...)
				}
			}
			assert.ElementsMatch(t, []string{"doc1:c1", "doc2:c2"}, sources)
			assert.ElementsMatch(t, []string{"doc1 attribute", "doc2 attribute"}, attributes)
			wantRelations := 4
			if len(seeds) == 1 && seeds[0] == "Shanghai" {
				wantRelations = 3 // The self-loop has no Shanghai endpoint.
			}
			require.Len(t, graph.Relation, wantRelations)
			for _, relation := range graph.Relation {
				switch relation.Type {
				case "HEADQUARTERED_IN", "HAS_BRANCH_IN":
					assert.Equal(t, "Acme", relation.Node1)
					assert.Equal(t, "Shanghai", relation.Node2)
				case "RELATED_TO":
					assert.Equal(t, "Shanghai", relation.Node1)
					assert.Equal(t, "Acme", relation.Node2)
				case "SELF":
					assert.Equal(t, relation.SourceID, relation.TargetID)
				}
			}
		})
	}
}

// TestDelGraphIntegration verifies the #5220 cleanup path against a live
// Neo4j: more than one APOC batch is deleted, other namespaces survive, and
// a retry after the graph is already gone stays idempotent.
func TestDelGraphIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	driver := integrationDriver(t)
	require.NoError(t, driver.VerifyConnectivity(ctx))
	_, err := neo4j.ExecuteQuery(ctx, driver, "RETURN apoc.version() AS version", nil, neo4j.EagerResultTransformer)
	require.NoError(t, err, "APOC must be installed for DelGraph batching")

	repo := NewNeo4jRepository(driver).(*Neo4jRepository)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	target := types.NameSpace{KnowledgeBase: "cleanup_kb_" + suffix, Knowledge: "cleanup_doc_" + suffix}
	other := types.NameSpace{KnowledgeBase: target.KnowledgeBase, Knowledge: "other_doc_" + suffix}
	targetLabel, otherLabel := repo.Label(target), repo.Label(other)
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		_, cleanupErr := neo4j.ExecuteQuery(cleanupCtx, driver,
			"MATCH (n:"+targetLabel+") DETACH DELETE n", nil, neo4j.EagerResultTransformer)
		assert.NoError(t, cleanupErr)
		_, cleanupErr = neo4j.ExecuteQuery(cleanupCtx, driver,
			"MATCH (n:"+otherLabel+") DETACH DELETE n", nil, neo4j.EagerResultTransformer)
		assert.NoError(t, cleanupErr)
	})

	// 1200 nodes/relationships spans more than one APOC batch (batchSize=1000).
	_, err = neo4j.ExecuteQuery(ctx, driver,
		"UNWIND range(1, 1200) AS i CREATE (n:"+targetLabel+" {kg: $kid, name: 'n'+toString(i)})",
		map[string]any{"kid": target.Knowledge}, neo4j.EagerResultTransformer)
	require.NoError(t, err)
	_, err = neo4j.ExecuteQuery(ctx, driver,
		"MATCH (n:"+targetLabel+" {kg: $kid}) CREATE (n)-[:SELF]->(n)",
		map[string]any{"kid": target.Knowledge}, neo4j.EagerResultTransformer)
	require.NoError(t, err)
	_, err = neo4j.ExecuteQuery(ctx, driver,
		"CREATE (n:"+otherLabel+" {kg: $kid, name: 'keep'})",
		map[string]any{"kid": other.Knowledge}, neo4j.EagerResultTransformer)
	require.NoError(t, err)

	countNodes := func(label, kid string) int64 {
		result, queryErr := neo4j.ExecuteQuery(ctx, driver,
			"MATCH (n:"+label+" {kg: $kid}) RETURN count(n) AS c",
			map[string]any{"kid": kid}, neo4j.EagerResultTransformer)
		require.NoError(t, queryErr)
		require.Len(t, result.Records, 1)
		value, _, valueErr := neo4j.GetRecordValue[int64](result.Records[0], "c")
		require.NoError(t, valueErr)
		return value
	}
	require.Equal(t, int64(1200), countNodes(targetLabel, target.Knowledge))
	require.Equal(t, int64(1), countNodes(otherLabel, other.Knowledge))

	require.NoError(t, repo.DelGraph(ctx, []types.NameSpace{target}))
	require.Zero(t, countNodes(targetLabel, target.Knowledge))
	require.Equal(t, int64(1), countNodes(otherLabel, other.Knowledge), "other namespace must survive")

	// A graph cleanup retry task may run after the rows/graph are already gone.
	require.NoError(t, repo.DelGraph(ctx, []types.NameSpace{target}))
}
