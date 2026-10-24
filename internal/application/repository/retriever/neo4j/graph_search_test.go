package neo4j

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGraphSearchRepositoryEngine(t *testing.T) {
	for input, want := range map[GraphEngine]GraphEngine{
		"": EngineNeo4j, EngineNeo4j: EngineNeo4j, EngineMemgraph: EngineMemgraph,
	} {
		repo := NewGraphSearchRepository(nil, input).(*Neo4jRepository)
		require.Equal(t, want, repo.engine)
		require.Equal(t, "ENTITY", repo.nodePrefix)
	}
}

func TestPublicGraphSearchCypherDialects(t *testing.T) {
	for _, engine := range []GraphEngine{"", EngineNeo4j, EngineMemgraph} {
		t.Run(string(engine), func(t *testing.T) {
			idFunc := "elementId"
			if engine == EngineMemgraph {
				idFunc = "id"
			}
			label := "`ENTITYkb`"
			seedQuery := graphSearchSeedQuery(engine, label)
			neighbourQuery := graphSearchNeighbourQuery(engine, label)
			nameLimit, sourceLimit, attributeLimit := 513, 129, 1001
			if engine == EngineMemgraph {
				nameLimit, sourceLimit, attributeLimit = 2052, 516, 4004
			}
			for _, query := range []string{seedQuery, neighbourQuery} {
				require.Contains(t, query, "MATCH (n:"+label+")")
				require.Contains(t, query, "LIMIT $probeLimit")
				for _, field := range []string{
					fmt.Sprintf("substring(n.name, 0, %d)", nameLimit),
					fmt.Sprintf("substring(n.kg, 0, %d)", sourceLimit),
					"n.chunks[..513]", fmt.Sprintf("substring(c, 0, %d)", sourceLimit),
					"n.attributes[..21]", fmt.Sprintf("substring(a, 0, %d)", attributeLimit),
				} {
					require.Contains(t, query, field)
				}
				if engine == EngineMemgraph {
					require.NotContains(t, query, "elementId(")
					require.Contains(t, query, "id: toString(id(n))")
				} else {
					require.Contains(t, query, "id: elementId(n)")
					require.NotContains(t, query, "toString(")
				}
			}
			require.Contains(t, seedQuery, "WHERE n.name CONTAINS $query")
			require.Contains(t, seedQuery, "CASE WHEN n.name = $query THEN 0 ELSE 1 END")
			require.Contains(t, seedQuery, "size(n.name), n.name, n.kg, "+idFunc+"(n)")
			require.NotContains(t, seedQuery, "EXISTS", "relation-less seeds must remain visible")
			require.Contains(t, neighbourQuery, "WHERE "+idFunc+"(n) = $seeds[seed_index]")
			require.Contains(t, neighbourQuery, "MATCH (n)-[r]-(m:"+label+")")
			dedup := "WHERE NOT " + idFunc + "(m) IN $seeds[0..seed_index]"
			require.Contains(t, neighbourQuery, dedup)
			require.Less(t, strings.Index(neighbourQuery, dedup), strings.Index(neighbourQuery, "LIMIT $probeLimit"))
			require.Contains(t, neighbourQuery, "ORDER BY seed_index, "+idFunc+"(r)")
			for expression, alias := range map[string]string{
				"r": "id", "startNode(r)": "source_id", "endNode(r)": "target_id",
			} {
				identity := idFunc + "(" + expression + ")"
				if engine == EngineMemgraph {
					identity = "toString(" + identity + ")"
				}
				require.Contains(t, neighbourQuery, identity+" AS "+alias)
			}
			require.Contains(t, neighbourQuery, fmt.Sprintf("substring(type(r), 0, %d) AS type", sourceLimit))
		})
	}
}

func TestGraphSearchSeedIDs(t *testing.T) {
	for _, engine := range []GraphEngine{"", EngineNeo4j} {
		ids := []string{"4:neo4j-database:0", "4:neo4j-database:1"}
		got, err := graphSearchSeedIDs(engine, ids)
		require.NoError(t, err)
		require.Equal(t, ids, got)
	}
	got, err := graphSearchSeedIDs(EngineMemgraph, []string{"0", "42", "9007199254740993", "9223372036854775807"})
	require.NoError(t, err)
	require.Equal(t, []int64{0, 42, 9007199254740993, 9223372036854775807}, got)
	for _, id := range []string{"", "not-an-id", "-1", "1.5", "9223372036854775808"} {
		got, err := graphSearchSeedIDs(EngineMemgraph, []string{id})
		require.Error(t, err, id)
		require.Nil(t, got)
	}
}

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
	for _, key := range []string{"id", "name", "knowledge_id", "chunks", "attributes"} {
		props := map[string]any{}
		for k, v := range valid {
			props[k] = v
		}
		if key == "chunks" || key == "attributes" {
			props[key] = []any{"\xff"}
		} else {
			props[key] = "\xff"
		}
		_, err = decodeGraphSearchProjection(&neo4j.Record{Keys: []string{"n"}, Values: []any{props}}, "n")
		require.Error(t, err, "malformed UTF-8 must not be normalized into a different identity or value")
	}
	// Memgraph's numeric identities cross Bolt as strings, including zero.
	valid["id"] = "0"
	node, err = decodeGraphSearchProjection(record, "n")
	require.NoError(t, err)
	require.Equal(t, "0", node.ID)
	valid["id"] = int64(0)
	_, err = decodeGraphSearchProjection(record, "n")
	require.Error(t, err, "native numeric IDs must be converted by the projection")
}

func TestGraphSearchProjectionUnicode(t *testing.T) {
	for _, character := range []string{"a", "é", "中", "😀"} {
		t.Run(character, func(t *testing.T) {
			// Simulate the maximum bytes returned by Memgraph, including a
			// partial final rune when the UTF-8 width does not divide the cap.
			project := func(text string, limit int) string {
				if len(text) > utf8.UTFMax*limit {
					return text[:utf8.UTFMax*limit]
				}
				return text
			}
			props := map[string]any{
				"id":           "0",
				"name":         project(strings.Repeat(character, 3000), 513),
				"knowledge_id": project(strings.Repeat(character, 3000), 129),
				"chunks":       []any{project(strings.Repeat(character, 3000), 129)},
				"attributes":   []any{project(strings.Repeat(character, 5000), 1001)},
			}
			node, err := decodeGraphSearchProjection(&neo4j.Record{Keys: []string{"n"}, Values: []any{props}}, "n")
			require.NoError(t, err)
			require.Equal(t, strings.Repeat(character, 513), node.Name)
			require.Equal(t, strings.Repeat(character, 129), node.KnowledgeID)
			require.Equal(t, []string{strings.Repeat(character, 129)}, node.Chunks)
			require.Equal(t, []string{strings.Repeat(character, 1001)}, node.Attributes)
			for _, value := range []string{node.Name, node.KnowledgeID, node.Chunks[0], node.Attributes[0]} {
				require.True(t, utf8.ValidString(value))
			}
			require.Greater(t, len(node.KnowledgeID), 128, "oversized source IDs must remain ineligible")
			require.Greater(t, len(node.Chunks[0]), 128, "oversized source IDs must not become trusted prefixes")
		})
	}
	for _, id := range []string{
		strings.Repeat("a", 128), strings.Repeat("é", 64),
		strings.Repeat("中", 42) + "ab", strings.Repeat("😀", 32),
		strings.Repeat("a", 127) + "中", // Oversized in bytes, even though below the rune cap.
	} {
		require.Equal(t, id, trimGraphSearchProjection(id, 129), "source identities must stay exact")
	}
	for _, text := range []string{"", "é中😀", strings.Repeat("😀", 129)} {
		require.Equal(t, text, trimGraphSearchProjection(text, 129))
	}
}

func TestGraphSearchIntegration(t *testing.T) {
	uri := os.Getenv("WEKNORA_NEO4J_TEST_URI")
	if uri == "" {
		t.Skip("set WEKNORA_NEO4J_TEST_URI to run the live public graph-query regression")
	}
	testGraphSearchIntegration(t, uri, neo4j.NoAuth(), EngineNeo4j)
}

func TestMemgraphGraphSearchIntegration(t *testing.T) {
	uri := os.Getenv("WEKNORA_MEMGRAPH_URI")
	if uri == "" {
		t.Skip("set WEKNORA_MEMGRAPH_URI to run the live Memgraph public graph-query regression")
	}
	testGraphSearchIntegration(t, uri, memgraphTestAuth(), EngineMemgraph)
}

func testGraphSearchIntegration(t *testing.T, uri string, auth neo4j.AuthToken, engine GraphEngine) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	driver, err := neo4j.NewDriver(uri, auth)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, driver.Close(context.Background())) })
	require.NoError(t, driver.VerifyConnectivity(ctx))
	repo := NewGraphSearchRepository(driver, engine).(*Neo4jRepository)
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
	t.Run("deduplicate before the row cap", func(t *testing.T) {
		seeds, err := neo4j.ExecuteQuery(ctx, driver, graphSearchSeedQuery(engine, label),
			map[string]any{"query": "Acme", "probeLimit": types.GraphSearchMaxSeeds + 1},
			neo4j.EagerResultTransformer)
		require.NoError(t, err)
		ids := make([]string, 0, len(seeds.Records))
		for _, record := range seeds.Records {
			node, err := decodeGraphSearchProjection(record, "n")
			require.NoError(t, err)
			ids = append(ids, node.ID)
		}
		nativeIDs, err := graphSearchSeedIDs(engine, ids)
		require.NoError(t, err)
		params := map[string]any{"seeds": nativeIDs, "probeLimit": 5}
		rows, err := neo4j.ExecuteQuery(ctx, driver, graphSearchNeighbourQuery(engine, label),
			params, neo4j.EagerResultTransformer)
		require.NoError(t, err)
		require.Len(t, rows.Records, 4, "duplicate endpoint matches must not consume the fifth slot")
		params["probeLimit"] = 1
		rows, err = neo4j.ExecuteQuery(ctx, driver, graphSearchNeighbourQuery(engine, label),
			params, neo4j.EagerResultTransformer)
		require.NoError(t, err)
		require.Len(t, rows.Records, 1)
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
			require.NotEmpty(t, node.ID)
			if engine == EngineMemgraph {
				_, err := strconv.ParseInt(node.ID, 10, 64)
				require.NoError(t, err, "Memgraph node identities must be decimal strings")
			}
			names[node.ID] = node.Name
		}
		relationIDs := map[string]bool{}
		for _, relation := range result.Graph.Relation {
			require.NotEmpty(t, relation.ID)
			require.False(t, relationIDs[relation.ID], "stored relationships must not be returned twice")
			relationIDs[relation.ID] = true
			if engine == EngineMemgraph {
				_, err := strconv.ParseInt(relation.ID, 10, 64)
				require.NoError(t, err, "Memgraph relationship identities must be decimal strings")
			}
			require.NotEqual(t, "MUST_NOT_LEAK", relation.Type)
			switch relation.Type {
			case "HEADQUARTERED_IN", "HAS_BRANCH_IN":
				require.Equal(t, "Acme", names[relation.SourceID])
				require.Equal(t, "Acme Shanghai", names[relation.TargetID])
			case "RELATED_TO":
				require.Equal(t, "Acme Shanghai", names[relation.SourceID])
				require.Equal(t, "Acme", names[relation.TargetID])
			case "SELF":
				require.Equal(t, relation.SourceID, relation.TargetID)
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
	t.Run("unicode projection caps and source identities", func(t *testing.T) {
		for _, tc := range []struct{ name, character string }{
			{"ascii", "a"}, {"two_bytes", "é"}, {"three_bytes", "中"}, {"four_bytes", "😀"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				prefix := "UnicodeProjection_" + tc.name + " "
				name := prefix + strings.Repeat(tc.character, 3000)
				sourceID := strings.Repeat(tc.character, 600)
				attribute := strings.Repeat(tc.character, 5000)
				relationType := strings.Repeat(tc.character, 600)
				chunks, attributes := make([]string, 600), make([]string, 25)
				for i := range chunks {
					chunks[i] = sourceID
				}
				for i := range attributes {
					attributes[i] = attribute
				}
				_, err := neo4j.ExecuteQuery(ctx, driver, `
					CREATE (n:`+label+` {name:$name, kg:$sourceID, chunks:$chunks, attributes:$attributes}),
					       (m:`+label+` {name:'Unicode neighbour', kg:'doc', chunks:['c1'], attributes:[]}),
					       (n)-[:`+"`"+relationType+"`"+`]->(m)`,
					map[string]any{"name": name, "sourceID": sourceID, "chunks": chunks, "attributes": attributes},
					neo4j.EagerResultTransformer)
				require.NoError(t, err)

				// Inspect un-decoded records so Go-side trimming cannot hide an
				// unbounded wire payload or changed list cardinality.
				seeds, err := neo4j.ExecuteQuery(ctx, driver, graphSearchSeedQuery(engine, label),
					map[string]any{"query": prefix, "probeLimit": types.GraphSearchMaxSeeds + 1},
					neo4j.EagerResultTransformer)
				require.NoError(t, err)
				require.Len(t, seeds.Records, 1)
				value, _ := seeds.Records[0].Get("n")
				props := value.(map[string]any)
				assertWireBound := func(got any, original string, runeLimit int) {
					t.Helper()
					text := got.(string)
					require.LessOrEqual(t, len(text), utf8.UTFMax*runeLimit)
					if engine == EngineMemgraph {
						require.Equal(t, original[:utf8.UTFMax*runeLimit], text)
					} else {
						require.Equal(t, string([]rune(original)[:runeLimit]), text)
					}
				}
				assertWireBound(props["name"], name, 513)
				assertWireBound(props["knowledge_id"], sourceID, 129)
				projectedChunks, projectedAttributes := props["chunks"].([]any), props["attributes"].([]any)
				require.Len(t, projectedChunks, 513)
				require.Len(t, projectedAttributes, 21)
				assertWireBound(projectedChunks[0], sourceID, 129)
				assertWireBound(projectedAttributes[0], attribute, 1001)

				result, err := repo.SearchGraph(ctx, ns, prefix)
				require.NoError(t, err)
				require.Len(t, result.Graph.Node, 2)
				node := result.Graph.Node[0]
				require.Equal(t, string([]rune(name)[:513]), node.Name)
				require.Equal(t, strings.Repeat(tc.character, 129), node.KnowledgeID)
				require.Len(t, node.Chunks, 513)
				require.Len(t, node.Attributes, 21)
				require.Equal(t, strings.Repeat(tc.character, 129), node.Chunks[0])
				require.Equal(t, strings.Repeat(tc.character, 1001), node.Attributes[0])
				require.Len(t, result.Graph.Relation, 1)
				require.Equal(t, strings.Repeat(tc.character, 129), result.Graph.Relation[0].Type)
				for _, value := range []string{
					node.Name, node.KnowledgeID, node.Chunks[0], node.Attributes[0], result.Graph.Relation[0].Type,
				} {
					require.True(t, utf8.ValidString(value))
				}
				require.Greater(t, len(node.KnowledgeID), 128)
				require.Greater(t, len(node.Chunks[0]), 128)
			})
		}
	})
}
