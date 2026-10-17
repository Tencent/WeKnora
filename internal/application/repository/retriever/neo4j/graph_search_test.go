package neo4j

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGraphSearchLabelAndDisabledBackend(t *testing.T) {
	repo := &Neo4jRepository{nodePrefix: "ENTITY"}
	label, err := repo.graphSearchLabel(types.NameSpace{KnowledgeBase: "kb-`):MATCH (x)"})
	require.NoError(t, err)
	require.Equal(t, "`ENTITYkb_``):MATCH (x)`", label)
	_, err = repo.graphSearchLabel(types.NameSpace{})
	require.Error(t, err)
	_, err = repo.graphSearchLabel(types.NameSpace{KnowledgeBase: "kb", Knowledge: "doc"})
	require.Error(t, err)
	result, err := repo.SearchGraph(context.Background(), types.NameSpace{KnowledgeBase: "kb"}, "Acme")
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestGraphSearchProjectionValidation(t *testing.T) {
	valid := map[string]any{
		"id": "n", "name": "Acme", "knowledge_id": "doc",
		"chunks": []any{"c1"}, "attributes": []any{"attr"},
	}
	record := &neo4j.Record{Keys: []string{"n"}, Values: []any{valid}}
	node, err := decodeGraphSearchProjection(record, "n")
	require.NoError(t, err)
	require.Equal(t, []string{"c1"}, node.Chunks)
	_, err = decodeGraphSearchProjection(nil, "n")
	require.Error(t, err)
	for _, key := range []string{"id", "name", "knowledge_id", "chunks", "attributes"} {
		props := map[string]any{}
		for k, v := range valid {
			props[k] = v
		}
		props[key] = 17
		_, err = decodeGraphSearchProjection(&neo4j.Record{Keys: []string{"n"}, Values: []any{props}}, "n")
		require.Error(t, err, key)
	}
}

func TestGraphSearchIntegration(t *testing.T) {
	uri := os.Getenv("WEKNORA_NEO4J_TEST_URI")
	if uri == "" {
		t.Skip("set WEKNORA_NEO4J_TEST_URI to run the live public graph-query regression")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	driver, err := neo4j.NewDriver(uri, neo4j.NoAuth())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, driver.Close(context.Background())) })
	require.NoError(t, driver.VerifyConnectivity(ctx))
	repo := NewGraphSearchRepository(driver).(*Neo4jRepository)
	// Exercise escaping against a real engine, not only a string assertion.
	ns := types.NameSpace{KnowledgeBase: fmt.Sprintf("public_graph_%d_`", time.Now().UnixNano())}
	label, err := repo.graphSearchLabel(ns)
	require.NoError(t, err)
	outsideLabel, err := repo.graphSearchLabel(types.NameSpace{KnowledgeBase: ns.KnowledgeBase + "_outside"})
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, err := neo4j.ExecuteQuery(cleanupCtx, driver, "MATCH (n:"+label+") DETACH DELETE n",
			nil, neo4j.EagerResultTransformer)
		assert.NoError(t, err)
	})
	_, err = neo4j.ExecuteQuery(ctx, driver, `
		CREATE (a:`+label+` {name:'Acme', kg:'doc1', chunks:['c1'], attributes:['attr1']}),
		       (b:`+label+` {name:'Acme Shanghai', kg:'doc1', chunks:['c1'], attributes:[]}),
		       (a2:`+label+` {name:'Acme', kg:'doc2', chunks:['c2'], attributes:['attr2']}),
		       (b2:`+label+` {name:'Acme Shanghai', kg:'doc2', chunks:['c2'], attributes:[]}),
		       (isolated:`+label+` {name:'Isolated', kg:'doc1', chunks:['c1'], attributes:[]}),
		       (outside:`+outsideLabel+` {name:'Outside', kg:'secret', chunks:['secret']}),
		       (a)-[:HEADQUARTERED_IN]->(b), (b)-[:RELATED_TO]->(a), (a)-[:SELF]->(a),
		       (a2)-[:HAS_BRANCH_IN]->(b2), (a)-[:MUST_NOT_LEAK]->(outside)
	`, nil, neo4j.EagerResultTransformer)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := neo4j.ExecuteQuery(context.Background(), driver,
			"MATCH (n:"+outsideLabel+") DETACH DELETE n", nil, neo4j.EagerResultTransformer)
		assert.NoError(t, err)
	})
	for _, query := range []string{"Acme", "Shanghai"} {
		result, err := repo.SearchGraph(ctx, ns, query)
		require.NoError(t, err)
		require.Len(t, result.Graph.Node, 4)
		expectedRelations := 4
		if query == "Shanghai" {
			expectedRelations = 3 // Self-loop is outside this one-hop neighbourhood.
		}
		require.Len(t, result.Graph.Relation, expectedRelations)
		names := map[string]string{}
		for _, node := range result.Graph.Node {
			names[node.ID] = node.Name
		}
		for _, relation := range result.Graph.Relation {
			require.NotEqual(t, "MUST_NOT_LEAK", relation.Type)
			if relation.Type == "HEADQUARTERED_IN" {
				require.Equal(t, "Acme", names[relation.SourceID])
				require.Equal(t, "Acme Shanghai", names[relation.TargetID])
			}
		}
		require.Empty(t, result.TruncationReasons)
	}
	isolated, err := repo.SearchGraph(ctx, ns, "Isolated")
	require.NoError(t, err)
	require.Len(t, isolated.Graph.Node, 1)
	require.Empty(t, isolated.Graph.Relation)
	empty, err := repo.SearchGraph(ctx, ns, "no match")
	require.NoError(t, err)
	require.Empty(t, empty.Graph.Node)
	// Full query text remains a parameter, including a Cypher-looking payload.
	empty, err = repo.SearchGraph(ctx, ns, "' RETURN 1 //")
	require.NoError(t, err)
	require.Empty(t, empty.Graph.Node)

	t.Run("seed cap and exact-name priority", func(t *testing.T) {
		_, err := neo4j.ExecuteQuery(ctx, driver, `
			UNWIND range(0, 104) AS i CREATE (n:`+label+`
				{name:'Cap '+toString(i), kg:'doc', chunks:['c1'], attributes:[]})
			WITH count(*) AS created
			CREATE (exact:`+label+` {name:'Cap', kg:'doc', chunks:['c1'], attributes:[]})`,
			nil, neo4j.EagerResultTransformer)
		require.NoError(t, err)
		result, err := repo.SearchGraph(ctx, ns, "Cap")
		require.NoError(t, err)
		require.Len(t, result.Graph.Node, types.GraphSearchMaxSeeds)
		require.Equal(t, "Cap", result.Graph.Node[0].Name)
		require.Contains(t, result.TruncationReasons, "candidate_seeds")
	})
	t.Run("row cap without duplicate edge expansion", func(t *testing.T) {
		_, err := neo4j.ExecuteQuery(ctx, driver, `
			CREATE (hub:`+label+` {name:'Hub', kg:'doc', chunks:['c1'], attributes:[]})
			WITH hub UNWIND range(0, 512) AS i
			CREATE (leaf:`+label+` {name:'Leaf '+toString(i), kg:'doc', chunks:['c1'], attributes:[]}),
			       (hub)-[:CONNECTS]->(leaf)`,
			nil, neo4j.EagerResultTransformer)
		require.NoError(t, err)
		result, err := repo.SearchGraph(ctx, ns, "Hub")
		require.NoError(t, err)
		require.Len(t, result.Graph.Relation, types.GraphSearchMaxRows)
		require.Contains(t, result.TruncationReasons, "candidate_relations")
	})
	t.Run("field projections are bounded before Bolt", func(t *testing.T) {
		_, err := neo4j.ExecuteQuery(ctx, driver, `
			CREATE (n:`+label+` {name:'Large', kg:'doc', chunks:$chunks, attributes:$attributes})`,
			map[string]any{
				"chunks":     strings.Split(strings.Repeat("c,", 600), ","),
				"attributes": []string{strings.Repeat("中", 3000)},
			}, neo4j.EagerResultTransformer)
		require.NoError(t, err)
		result, err := repo.SearchGraph(ctx, ns, "Large")
		require.NoError(t, err)
		require.Len(t, result.Graph.Node, 1)
		require.Len(t, result.Graph.Node[0].Chunks, 513)
		require.Len(t, []rune(result.Graph.Node[0].Attributes[0]), 1001)
	})
}
