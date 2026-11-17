package neo4j

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	driver "github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchemaGraphSearchParametersAndScope(t *testing.T) {
	repo := NewNeo4jRepository(nil).(*Neo4jRepository)
	scope := types.NameSpace{KnowledgeBase: "kb", Knowledge: "doc"}
	hints := []types.GraphQuery{{Name: "Alex' MATCH (n)", EntityType: "Person", RelationType: "WORKS_AT",
		Direction: "out", TargetType: "Company"}}
	label := repo.quotedGraphLabel(scope)
	query, params := schemaGraphSearchCypher(label, hints)
	require.Equal(t, "`ENTITYkb`:`ENTITYdoc`", label)
	require.Contains(t, query, "MATCH (n:"+label+")")
	require.Contains(t, query, "(m:"+label+")")
	require.Contains(t, query, "startNode(r) = n")
	require.Contains(t, query, "endNode(r) = n")
	require.Less(t, strings.Index(query, "AND EXISTS"), strings.Index(query, "LIMIT $maxSeedNodes"),
		"only seeds with a qualifying relationship can occupy the seed budget")
	require.NotContains(t, query, hints[0].Name)
	require.NotContains(t, query, hints[0].RelationType)
	require.Equal(t, graphSearchMaxSeedNodes, params["maxSeedNodes"])
	require.Equal(t, graphSearchMaxRows, params["maxRows"])
	rows := params["queries"].([]map[string]interface{})
	require.Equal(t, hints[0].Name, rows[0]["name"])
	require.Equal(t, "out", rows[0]["direction"])
	require.Equal(t, "Person", rows[0]["entity_type"])
	require.Equal(t, "TYPE_Person", rows[0]["entity_label"])
	require.Equal(t, "TYPE_Company", rows[0]["target_label"])
	require.Contains(t, query, "q.entity_label IN labels(n)")
	require.Contains(t, query, "q.target_label IN labels(m)")
	require.Contains(t, query, "NONE(typeLabel IN labels(n) WHERE typeLabel STARTS WITH 'TYPE_')")
	hostileType := "\u4f01\u4e1a` MATCH (other) DETACH DELETE other //"
	hostileQuery, hostileParams := schemaGraphSearchCypher(label, []types.GraphQuery{
		{Name: "Alex", EntityType: hostileType, TargetType: hostileType},
	})
	require.NotContains(t, hostileQuery, hostileType)
	hostileRows := hostileParams["queries"].([]map[string]interface{})
	require.Equal(t, "TYPE_"+hostileType, hostileRows[0]["entity_label"])
	require.Equal(t, "TYPE_"+hostileType, hostileRows[0]["target_label"])
	_, err := repo.SearchNodeWithSchema(context.Background(), types.NameSpace{}, hints)
	require.Error(t, err)
}

func TestSchemaGraphSearchDeduplicatesIdenticalHintsBeforeSeedBudget(t *testing.T) {
	hint := types.GraphQuery{Name: "Alex", EntityType: "Person", RelationType: "WORKS_AT",
		Direction: "out", TargetType: "Company"}
	var repeated []types.GraphQuery
	for i := 0; i < 8; i++ {
		repeated = append(repeated, hint)
	}
	query, params := schemaGraphSearchCypher("`ENTITYkb`", repeated)
	singleQuery, singleParams := schemaGraphSearchCypher("`ENTITYkb`", []types.GraphQuery{hint})
	require.Equal(t, singleQuery, query)
	require.Equal(t, singleParams, params, "duplicate hints must not consume additional seed rows")

	different := hint
	different.Direction = "in"
	_, params = schemaGraphSearchCypher("`ENTITYkb`", append(repeated, different))
	rows := params["queries"].([]map[string]interface{})
	require.Len(t, rows, 2, "distinct directional filters must remain separate")
	require.Equal(t, "out", rows[0]["direction"])
	require.Equal(t, "in", rows[1]["direction"])
}

func TestSchemaGraphIntegration(t *testing.T) {
	uri := os.Getenv("WEKNORA_NEO4J_TEST_URI")
	if uri == "" {
		t.Skip("set WEKNORA_NEO4J_TEST_URI to a disposable Neo4j with APOC and auth disabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	auth := driver.NoAuth()
	if password := os.Getenv("WEKNORA_NEO4J_TEST_PASSWORD"); password != "" {
		auth = driver.BasicAuth(os.Getenv("WEKNORA_NEO4J_TEST_USER"), password, "")
	}
	db, err := driver.NewDriver(uri, auth)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close(context.Background())) })
	require.NoError(t, db.VerifyConnectivity(ctx))
	repo := NewNeo4jRepository(db).(*Neo4jRepository)
	kb := fmt.Sprintf("schema_regression_%d", time.Now().UnixNano())
	label := repo.quotedGraphLabel(types.NameSpace{KnowledgeBase: kb})
	t.Cleanup(func() {
		_, cleanupErr := driver.ExecuteQuery(context.Background(), db, "MATCH (n:"+label+") DETACH DELETE n",
			nil, driver.EagerResultTransformer)
		assert.NoError(t, cleanupErr)
	})
	graph := &types.GraphData{
		Node: []*types.GraphNode{
			{Name: "Alex", EntityType: "Person", Chunks: []string{"c"}},
			{Name: "Alex", EntityType: "Company", Chunks: []string{"c"}},
		},
		Relation: []*types.GraphRelation{
			{Node1: "Alex", SourceType: "Person", Node2: "Alex", TargetType: "Company", Type: "WORKS_AT"},
		},
	}
	for _, doc := range []string{"doc1", "doc2"} {
		require.NoError(t, repo.AddGraph(ctx, types.NameSpace{KnowledgeBase: kb, Knowledge: doc}, []*types.GraphData{graph, graph}))
	}
	for _, tc := range []struct {
		name      string
		doc       string
		hint      types.GraphQuery
		wantNodes int
	}{
		{"whole KB", "", types.GraphQuery{Name: "Alex", EntityType: "Person", Direction: "out", RelationType: "WORKS_AT"}, 4},
		{"single doc", "doc1", types.GraphQuery{Name: "Alex", EntityType: "Person", Direction: "out", TargetType: "Company"}, 2},
		{"incoming", "doc2", types.GraphQuery{Name: "Alex", EntityType: "Company", Direction: "in", TargetType: "Person"}, 2},
		{"wrong direction", "", types.GraphQuery{Name: "Alex", EntityType: "Company", Direction: "out"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := repo.SearchNodeWithSchema(ctx, types.NameSpace{KnowledgeBase: kb, Knowledge: tc.doc},
				[]types.GraphQuery{tc.hint})
			require.NoError(t, err)
			require.Len(t, result.Node, tc.wantNodes)
			require.Len(t, result.Relation, tc.wantNodes/2)
			for _, relation := range result.Relation {
				require.Equal(t, "Person", relation.SourceType)
				require.Equal(t, "Company", relation.TargetType)
				require.NotEqual(t, relation.SourceID, relation.TargetID)
			}
		})
	}
	t.Run("duplicate seeds retain each edge once", func(t *testing.T) {
		result, err := repo.SearchNodeWithSchema(ctx, types.NameSpace{KnowledgeBase: kb}, []types.GraphQuery{
			{Name: "Alex"}, {Name: "Alex", EntityType: "Person", Direction: "out"},
			{Name: "Alex", EntityType: "Company", Direction: "in"},
		})
		require.NoError(t, err)
		require.Len(t, result.Node, 4)
		require.Len(t, result.Relation, 2)
	})
	t.Run("duplicate hints do not exhaust the seed budget", func(t *testing.T) {
		for i := 0; i < 30; i++ {
			require.NoError(t, repo.AddGraph(ctx, types.NameSpace{
				KnowledgeBase: kb, Knowledge: fmt.Sprintf("budget-doc-%02d", i),
			}, []*types.GraphData{{
				Node: []*types.GraphNode{
					{Name: "SeedPerson", EntityType: "Person", Chunks: []string{"c"}},
					{Name: "SeedCompany", EntityType: "Company", Chunks: []string{"c"}},
				},
				Relation: []*types.GraphRelation{{
					Node1: "SeedPerson", SourceType: "Person", Node2: "SeedCompany",
					TargetType: "Company", Type: "WORKS_AT",
				}},
			}}))
		}
		hint := types.GraphQuery{Name: "SeedPerson", EntityType: "Person", RelationType: "WORKS_AT"}
		hints := make([]types.GraphQuery, 8)
		for i := range hints {
			hints[i] = hint
		}
		found, err := repo.SearchNodeWithSchema(ctx, types.NameSpace{KnowledgeBase: kb}, hints)
		require.NoError(t, err)
		require.Len(t, found.Relation, 30)
		require.Len(t, found.Node, 60)
	})
	scope := types.NameSpace{KnowledgeBase: kb, Knowledge: "doc1"}
	require.NoError(t, repo.AddGraph(ctx, scope, []*types.GraphData{{
		Node: []*types.GraphNode{{Name: "Alex", Chunks: []string{"legacy"}, Attributes: []string{"retained"}}},
	}}))
	result, err := driver.ExecuteQuery(ctx, db, "MATCH (n:"+label+" {kg:'doc1', name:'Alex'}) RETURN n",
		nil, driver.EagerResultTransformer)
	require.NoError(t, err)
	require.Len(t, result.Records, 3, "untyped identity cannot merge into either typed entity")
	for _, record := range result.Records {
		node, _, decodeErr := driver.GetRecordValue[driver.Node](record, "n")
		require.NoError(t, decodeErr)
		require.Contains(t, node.Labels, "ENTITY"+kb)
		require.Contains(t, node.Labels, "ENTITYdoc1")
		entityType := node.Props["entity_type"].(string)
		if entityType == "" {
			for _, nodeLabel := range node.Labels {
				require.False(t, strings.HasPrefix(nodeLabel, graphTypeLabelPrefix),
					"legacy entities must remain untyped")
			}
		} else {
			require.Contains(t, node.Labels, graphTypeLabel(entityType))
		}
	}

	t.Run("property-only nodes remain readable and upgrade in place", func(t *testing.T) {
		old, err := driver.ExecuteQuery(ctx, db, `
			MATCH (n:`+repo.quotedGraphLabel(scope)+` {name:'Alex', entity_type:'Person'})
			CALL apoc.create.removeLabels(n, $labels) YIELD node
			SET node.chunks = ['old']
			REMOVE node.schema_scope
			RETURN elementId(node) AS id`, map[string]any{"labels": []string{"TYPE_Person", schemaEntityLabel}},
			driver.EagerResultTransformer)
		require.NoError(t, err)
		require.Len(t, old.Records, 1)
		typed, err := repo.SearchNodeWithSchema(ctx, scope, []types.GraphQuery{{
			Name: "Alex", EntityType: "Person", Direction: "out", TargetType: "Company",
		}})
		require.NoError(t, err)
		require.Len(t, typed.Relation, 1, "pre-label records retain typed retrieval")
		require.NoError(t, repo.AddGraph(ctx, scope, []*types.GraphData{graph, graph}))
		upgraded, err := driver.ExecuteQuery(ctx, db, `
			MATCH (n:`+repo.quotedGraphLabel(scope)+` {name:'Alex', entity_type:'Person'})
			RETURN n`, nil, driver.EagerResultTransformer)
		require.NoError(t, err)
		require.Len(t, upgraded.Records, 1, "adding a label must not create another identity")
		node, _, err := driver.GetRecordValue[driver.Node](upgraded.Records[0], "n")
		require.NoError(t, err)
		require.Equal(t, old.Records[0].Values[0], node.ElementId)
		require.Contains(t, node.Labels, "TYPE_Person")
		require.ElementsMatch(t, []any{"old", "c"}, node.Props["chunks"])
		require.Contains(t, node.Labels, schemaEntityLabel)
		require.Equal(t, kb, node.Props["schema_scope"])
	})

	t.Run("concurrent chunk writes across repository instances merge typed identities", func(t *testing.T) {
		concurrentScope := types.NameSpace{KnowledgeBase: kb, Knowledge: "concurrent"}
		const workers = 12
		var wg sync.WaitGroup
		errs := make(chan error, workers)
		start := make(chan struct{})
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				chunk := fmt.Sprintf("chunk%d", i)
				workerRepo := NewNeo4jRepository(db)
				errs <- workerRepo.AddGraph(ctx, concurrentScope, []*types.GraphData{{
					Node: []*types.GraphNode{
						{Name: "Alex", EntityType: "Person", Chunks: []string{chunk}},
						{Name: "Alex", EntityType: "Company", Chunks: []string{chunk}},
					},
					Relation: graph.Relation,
				}})
			}(i)
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		found, err := driver.ExecuteQuery(ctx, db, "MATCH (n:"+repo.quotedGraphLabel(concurrentScope)+") RETURN n",
			nil, driver.EagerResultTransformer)
		require.NoError(t, err)
		require.Len(t, found.Records, 2, "same name but different types remain distinct, each type merges across chunks")
		for _, record := range found.Records {
			node, _, err := driver.GetRecordValue[driver.Node](record, "n")
			require.NoError(t, err)
			require.Len(t, node.Props["chunks"], workers, "all chunk evidence is retained")
		}
		foundEdges, err := repo.SearchNodeWithSchema(ctx, concurrentScope, []types.GraphQuery{{Name: "Alex"}})
		require.NoError(t, err)
		require.Len(t, foundEdges.Relation, 1, "repeated chunk relationships merge too")
		_, err = driver.ExecuteQuery(ctx, db, `CREATE (:WEKNORA_SCHEMA_ENTITY {
			schema_scope:$kb, kg:'concurrent', name:'Alex', entity_type:'Person'})`,
			map[string]any{"kb": kb}, driver.EagerResultTransformer)
		require.Error(t, err, "database constraint rejects duplicate typed identities independently of the application")
	})

	t.Run("mismatched type labels cannot satisfy typed hints", func(t *testing.T) {
		conflictScope := types.NameSpace{KnowledgeBase: kb, Knowledge: "conflicting"}
		require.NoError(t, repo.AddGraph(ctx, conflictScope, []*types.GraphData{graph}))
		_, err := driver.ExecuteQuery(ctx, db, `
			MATCH (n:`+repo.quotedGraphLabel(conflictScope)+` {entity_type:'Person'})
			CALL apoc.create.removeLabels(n, ['TYPE_Person']) YIELD node
			CALL apoc.create.addLabels(node, ['TYPE_Company']) YIELD node AS conflicting
			RETURN conflicting`, nil, driver.EagerResultTransformer)
		require.NoError(t, err)
		for _, hint := range []types.GraphQuery{
			{Name: "Alex", EntityType: "Person", Direction: "out"},
			{Name: "Alex", EntityType: "Company", Direction: "in", TargetType: "Person"},
		} {
			found, err := repo.SearchNodeWithSchema(ctx, conflictScope, []types.GraphQuery{hint})
			require.NoError(t, err)
			require.Empty(t, found.Relation, "both seed and neighbor type labels must be checked")
		}
	})

	t.Run("relationship-only import labels endpoints with exact type names", func(t *testing.T) {
		endpointScope := types.NameSpace{KnowledgeBase: kb, Knowledge: "endpoints"}
		specialType := "\u4f01\u4e1a` MATCH (other) DETACH DELETE other //"
		require.NoError(t, repo.AddGraph(ctx, endpointScope, []*types.GraphData{{
			Relation: []*types.GraphRelation{{Node1: "OnlySource", SourceType: specialType,
				Node2: "OnlyTarget", TargetType: "Company", Type: "WORKS_AT"}},
		}}))
		nodes, err := driver.ExecuteQuery(ctx, db, "MATCH (n:"+repo.quotedGraphLabel(endpointScope)+") RETURN n",
			nil, driver.EagerResultTransformer)
		require.NoError(t, err)
		require.Len(t, nodes.Records, 2)
		for _, record := range nodes.Records {
			node, _, err := driver.GetRecordValue[driver.Node](record, "n")
			require.NoError(t, err)
			require.Contains(t, node.Labels, graphTypeLabel(node.Props["entity_type"].(string)))
		}
		found, err := repo.SearchNodeWithSchema(ctx, endpointScope, []types.GraphQuery{{
			Name: "OnlySource", EntityType: specialType, Direction: "out", TargetType: "Company",
		}})
		require.NoError(t, err)
		require.Len(t, found.Relation, 1)
		require.Equal(t, specialType, found.Relation[0].SourceType)
	})

	t.Run("type labels do not replace knowledge base scope", func(t *testing.T) {
		otherScope := types.NameSpace{KnowledgeBase: kb + "_other", Knowledge: "doc1"}
		otherLabel := repo.quotedGraphLabel(otherScope)
		t.Cleanup(func() {
			_, err := driver.ExecuteQuery(context.Background(), db, "MATCH (n:"+otherLabel+") DETACH DELETE n",
				nil, driver.EagerResultTransformer)
			assert.NoError(t, err)
		})
		require.NoError(t, repo.AddGraph(ctx, otherScope, []*types.GraphData{graph}))
		found, err := repo.SearchNodeWithSchema(ctx, scope, []types.GraphQuery{{
			Name: "Alex", EntityType: "Person", Direction: "out", TargetType: "Company",
		}})
		require.NoError(t, err)
		require.Len(t, found.Relation, 1)
		require.Len(t, found.Node, 2)
	})

	// Nonqualifying exact matches must not starve a valid substring seed.
	_, err = driver.ExecuteQuery(ctx, db, `
		UNWIND range(1, $count) AS i
		CREATE (n:`+label+` {name:'Budget', entity_type:'Person', kg:toString(i)})
		CREATE (m:`+label+` {name:'Other', entity_type:'Company', kg:toString(i)})
		CREATE (n)-[:IGNORED]->(m)`, map[string]any{"count": graphSearchMaxSeedNodes + 1},
		driver.EagerResultTransformer)
	require.NoError(t, err)
	require.NoError(t, repo.AddGraph(ctx, types.NameSpace{KnowledgeBase: kb, Knowledge: "budget"}, []*types.GraphData{{
		Node: []*types.GraphNode{
			{Name: "Budget Person", EntityType: "Person", Chunks: []string{"budget"}},
			{Name: "Employer", EntityType: "Company", Chunks: []string{"budget"}},
		},
		Relation: []*types.GraphRelation{{
			Node1: "Budget Person", SourceType: "Person", Node2: "Employer", TargetType: "Company", Type: "WORKS_AT",
		}},
	}}))
	filtered, err := repo.SearchNodeWithSchema(ctx, types.NameSpace{KnowledgeBase: kb}, []types.GraphQuery{{
		Name: "Budget", EntityType: "Person", RelationType: "WORKS_AT", Direction: "out", TargetType: "Company",
	}})
	require.NoError(t, err)
	require.Len(t, filtered.Relation, 1)
	require.Equal(t, "Budget Person", filtered.Relation[0].Node1)
}
