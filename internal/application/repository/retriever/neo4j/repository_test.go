package neo4j

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGraphSearchCypherBoundsAndOrders pins the three review points on #3550:
// relation-less seeds are excluded, exact name matches outrank substring-only
// hits, and both LIMITs are preceded by the same ordering key so a truncated
// result keeps the neighbourhoods of the highest-ranked seeds.
func TestGraphSearchCypherBoundsAndOrders(t *testing.T) {
	query, params := graphSearchCypher(EngineNeo4j, "ENTITY_kb", []string{"安恒"})

	for _, want := range []string{
		"EXISTS { (n)--() }",
		"CASE WHEN n.name IN $nodes THEN 0 ELSE 1 END AS seed_rank",
		"ORDER BY seed_rank, name_len, name",
		"WITH collect(n) AS seeds",
		"UNWIND range(0, size(seeds) - 1) AS seed_index",
		"WHERE NOT m IN earlier_seeds",
		"ORDER BY seed_index, elementId(r)",
		"LIMIT $maxSeedNodes",
		"LIMIT $maxRows",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("query is missing %q:\n%s", want, query)
		}
	}

	// Collect only the capped seeds, never the unbounded relationship candidates.
	collectAt := strings.Index(query, "WITH collect(n) AS seeds")
	if collectAt < strings.Index(query, "LIMIT $maxSeedNodes") || strings.Count(query, "collect(") != 1 {
		t.Errorf("only the bounded seed list may be collected:\n%s", query)
	}
	if !strings.Contains(query, "name, n.kg, elementId(n)") {
		t.Errorf("same-named document instances need stable tie breakers:\n%s", query)
	}
	if strings.Index(query, "WHERE NOT m IN earlier_seeds") > strings.Index(query, "LIMIT $maxRows") {
		t.Errorf("duplicate endpoint matches must be removed before the row cap:\n%s", query)
	}
	if strings.Index(query, "LIMIT $maxSeedNodes") > strings.Index(query, "LIMIT $maxRows") {
		t.Errorf("the seed cap must be applied before the row cap:\n%s", query)
	}
	// A relation-less seed expands to nothing, so it must not survive the first
	// cap (the check has to sit in the first WHERE, before the seed ordering).
	whereAt := strings.Index(query, "EXISTS { (n)--() }")
	seedCapAt := strings.Index(query, "LIMIT $maxSeedNodes")
	if whereAt < 0 || seedCapAt < 0 || whereAt > seedCapAt {
		t.Errorf("the relationship check must precede the seed cap:\n%s", query)
	}

	if params["maxSeedNodes"] != graphSearchMaxSeedNodes {
		t.Errorf("maxSeedNodes = %v, want %d", params["maxSeedNodes"], graphSearchMaxSeedNodes)
	}
	if params["maxRows"] != graphSearchMaxRows {
		t.Errorf("maxRows = %v, want %d", params["maxRows"], graphSearchMaxRows)
	}
	if got, ok := params["nodes"].([]string); !ok || len(got) != 1 || got[0] != "安恒" {
		t.Errorf("nodes param = %#v, want []string{\"安恒\"}", params["nodes"])
	}

	// The label expression is interpolated rather than parameterised, so it has
	// to reach every match in the query.
	if !strings.Contains(query, "MATCH (n:ENTITY_kb)") || !strings.Contains(query, "MATCH (n)-[r]-(m:ENTITY_kb)") {
		t.Errorf("label expression not applied to both matches:\n%s", query)
	}
}

type graphRecordResult struct {
	neo4j.Result
	records []*neo4j.Record
	index   int
	err     error
}

func (r *graphRecordResult) Next(context.Context) bool {
	if r.index >= len(r.records) {
		return false
	}
	r.index++
	return true
}

func (r *graphRecordResult) Record() *neo4j.Record { return r.records[r.index-1] }
func (r *graphRecordResult) Err() error            { return r.err }

func graphTestNode(id, name, document, chunk string, attributes ...string) neo4j.Node {
	attrs := make([]any, len(attributes))
	for i, attribute := range attributes {
		attrs[i] = attribute
	}
	return neo4j.Node{ElementId: id, Props: map[string]any{
		"name": name, "kg": document, "chunks": []any{chunk}, "attributes": attrs,
	}}
}

func graphTestRecord(n, m neo4j.Node, r neo4j.Relationship) *neo4j.Record {
	return &neo4j.Record{Keys: []string{"n", "r", "m"}, Values: []any{n, r, m}}
}

func TestDecodeGraphSearchDirectionAndIdentity(t *testing.T) {
	a := graphTestNode("a", "Acme", "doc1", "c1")
	b := graphTestNode("b", "Shanghai", "doc1", "c1")
	r := neo4j.Relationship{ElementId: "r", StartElementId: "a", EndElementId: "b", Type: "LOCATED_IN"}
	reverse := neo4j.Relationship{ElementId: "reverse", StartElementId: "b", EndElementId: "a", Type: "LOCATED_IN"}
	self := neo4j.Relationship{ElementId: "self", StartElementId: "a", EndElementId: "a", Type: "RELATED_TO"}
	for _, tt := range []struct {
		name      string
		records   []*neo4j.Record
		nodeCount int
		firstNode string
		relations [][3]string
	}{
		{"outgoing", []*neo4j.Record{graphTestRecord(a, b, r)}, 2, "a", [][3]string{{"r", "a", "b"}}},
		{"incoming", []*neo4j.Record{graphTestRecord(b, a, r)}, 2, "b", [][3]string{{"r", "a", "b"}}},
		{
			"both seeds",
			[]*neo4j.Record{graphTestRecord(b, a, r), graphTestRecord(a, b, r)},
			2, "b",
			[][3]string{{"r", "a", "b"}},
		},
		{
			"bidirectional",
			[]*neo4j.Record{graphTestRecord(a, b, r), graphTestRecord(a, b, reverse)},
			2, "a",
			[][3]string{{"r", "a", "b"}, {"reverse", "b", "a"}},
		},
		{
			"self loop",
			[]*neo4j.Record{graphTestRecord(a, a, self), graphTestRecord(a, a, self)},
			1, "a",
			[][3]string{{"self", "a", "a"}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			graph, err := decodeGraphSearchResult(context.Background(), &graphRecordResult{records: tt.records})
			require.NoError(t, err)
			require.Len(t, graph.Node, tt.nodeCount)
			assert.Equal(t, tt.firstNode, graph.Node[0].ID, "seed evidence priority must not change")
			require.Len(t, graph.Relation, len(tt.relations))
			for i, want := range tt.relations {
				got := graph.Relation[i]
				assert.Equal(t, want, [3]string{got.ID, got.SourceID, got.TargetID})
				byID := map[string]string{"a": "Acme", "b": "Shanghai"}
				assert.Equal(t, byID[want[1]], got.Node1)
				assert.Equal(t, byID[want[2]], got.Node2)
			}
		})
	}
}

func TestDecodeGraphSearchPreservesSameNamedDocumentSources(t *testing.T) {
	a1 := graphTestNode("a1", "Acme", "doc1", "c1", "doc1 attribute")
	b1 := graphTestNode("b1", "Shanghai", "doc1", "c1")
	a2 := graphTestNode("a2", "Acme", "doc2", "c2", "doc2 attribute")
	b2 := graphTestNode("b2", "Shanghai", "doc2", "c2")
	r1 := neo4j.Relationship{ElementId: "r1", StartElementId: "a1", EndElementId: "b1", Type: "LOCATED_IN"}
	r2 := neo4j.Relationship{ElementId: "r2", StartElementId: "a2", EndElementId: "b2", Type: "LOCATED_IN"}
	for _, records := range [][]*neo4j.Record{
		{graphTestRecord(b1, a1, r1), graphTestRecord(b2, a2, r2), graphTestRecord(a1, b1, r1)},
		{graphTestRecord(b2, a2, r2), graphTestRecord(b1, a1, r1), graphTestRecord(a2, b2, r2)},
	} {
		graph, err := decodeGraphSearchResult(context.Background(), &graphRecordResult{records: records})
		require.NoError(t, err)
		require.Len(t, graph.Node, 4)
		require.Len(t, graph.Relation, 2, "same names and type do not make two document edges identical")
		var sources, attributes []string
		for _, node := range graph.Node {
			if node.Name == "Acme" {
				sources = append(sources, node.KnowledgeID+":"+node.Chunks[0])
				attributes = append(attributes, node.Attributes...)
			}
		}
		assert.ElementsMatch(t, []string{"doc1:c1", "doc2:c2"}, sources)
		assert.ElementsMatch(t, []string{"doc1 attribute", "doc2 attribute"}, attributes)
	}
}

func TestDecodeGraphSearchReportsInvalidRecordsAndStreamErrors(t *testing.T) {
	a := graphTestNode("a", "Acme", "doc", "c")
	b := graphTestNode("b", "Shanghai", "doc", "c")
	r := neo4j.Relationship{ElementId: "r", StartElementId: "a", EndElementId: "b", Type: "LOCATED_IN"}
	streamErr := errors.New("stream interrupted")
	graph, err := decodeGraphSearchResult(context.Background(), &graphRecordResult{
		records: []*neo4j.Record{graphTestRecord(a, b, r)}, err: streamErr,
	})
	assert.Nil(t, graph, "a failed stream must not return a successful partial graph")
	assert.ErrorIs(t, err, streamErr)

	missingName := neo4j.Node{ElementId: "a", Props: map[string]any{}}
	missingID := graphTestNode("", "Acme", "doc", "c")
	wrongEndpoint := r
	wrongEndpoint.EndElementId = "elsewhere"
	missingRelationID := r
	missingRelationID.ElementId = ""
	for _, record := range []*neo4j.Record{
		nil,
		{Keys: []string{"n"}, Values: []any{"not a node"}},
		graphTestRecord(missingName, b, r),
		graphTestRecord(missingID, b, r),
		graphTestRecord(a, b, wrongEndpoint),
		graphTestRecord(a, b, missingRelationID),
	} {
		result := &graphRecordResult{records: []*neo4j.Record{record}}
		graph, err := decodeGraphSearchResult(context.Background(), result)
		assert.Error(t, err)
		assert.Nil(t, graph)
	}

	// Optional properties may be absent in legacy graph data.
	delete(a.Props, "attributes")
	graph, err = decodeGraphSearchResult(context.Background(), &graphRecordResult{records: []*neo4j.Record{
		graphTestRecord(a, b, r),
	}})
	require.NoError(t, err)
	assert.Empty(t, graph.Node[0].Attributes)
}

func TestNeo4jEngineKeepsAPOCQueries(t *testing.T) {
	// The Neo4j path is what existing deployments run: it must keep using the
	// APOC procedures, and keep letting APOC do the delete batching.
	for _, want := range []string{"apoc.merge.node(row.labels", "apoc.coll.union(node.chunks"} {
		if !strings.Contains(neo4jNodeImportQuery, want) {
			t.Errorf("node import query no longer uses %q: %s", want, neo4jNodeImportQuery)
		}
	}
	for _, want := range []string{"apoc.merge.node(row.source_labels", "apoc.merge.relationship(source"} {
		if !strings.Contains(neo4jRelationshipImportQuery, want) {
			t.Errorf("relationship import query no longer uses %q: %s", want, neo4jRelationshipImportQuery)
		}
	}
	deleteRels, deleteNodes := neo4jDeleteQueries("ENTITY_kb:ENTITY_doc")
	for _, query := range []string{deleteRels, deleteNodes} {
		for _, want := range []string{"apoc.periodic.iterate(", "batchSize: 1000"} {
			if !strings.Contains(query, want) {
				t.Errorf("delete query no longer uses %q: %s", want, query)
			}
		}
		if !strings.Contains(query, "ENTITY_kb:ENTITY_doc") {
			t.Errorf("label expression not applied: %s", query)
		}
	}
	// Nodes are deleted after their relationships, so a plain DELETE suffices
	// and a stray DETACH would silently widen the blast radius.
	if strings.Contains(deleteNodes, "DETACH DELETE") {
		t.Errorf("the Neo4j node deletion must stay a plain DELETE: %s", deleteNodes)
	}
}

func TestNewNeo4jRepositoryDefaultsToNeo4jEngine(t *testing.T) {
	// An unset GRAPH_DATABASE_ENGINE must not route an existing deployment onto
	// the Memgraph queries.
	for value, want := range map[GraphEngine]GraphEngine{
		"":             EngineNeo4j,
		EngineNeo4j:    EngineNeo4j,
		EngineMemgraph: EngineMemgraph,
	} {
		repo := NewNeo4jRepository(nil, value).(*Neo4jRepository)
		if repo.engine != want {
			t.Errorf("NewNeo4jRepository(_, %q).engine = %q, want %q", value, repo.engine, want)
		}
	}
}

func TestMemgraphCompatibleCypherAvoidsAPOC(t *testing.T) {
	nodeQuery := memgraphNodeImportQuery("ENTITY_kb:ENTITY_doc")
	if !strings.Contains(nodeQuery, "MERGE (node:ENTITY_kb:ENTITY_doc") {
		t.Errorf("node query missing the label expression: %s", nodeQuery)
	}

	query, err := memgraphRelationshipImportQuery("ENTITY_kb:ENTITY_doc", "MENTIONS")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"MERGE (source:ENTITY_kb:ENTITY_doc", "MERGE (source)-[rel:`MENTIONS`]->(target)"} {
		if !strings.Contains(query, want) {
			t.Errorf("relationship query missing %q: %s", want, query)
		}
	}
	deleteRels, deleteNodes := memgraphDeleteQueries("ENTITY_kb:ENTITY_doc", memgraphDeleteBatchSize)
	for _, query := range []string{nodeQuery, query, deleteRels, deleteNodes} {
		if strings.Contains(strings.ToLower(query), "apoc.") {
			t.Fatalf("Memgraph query must not depend on APOC: %s", query)
		}
	}

	escaped, err := memgraphRelationshipImportQuery("ENTITY_kb", "TYPE WITH `TICK`")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(escaped, "[rel:`TYPE WITH ``TICK```]") {
		t.Fatalf("relationship type was not escaped as an identifier: %s", escaped)
	}
	if _, err := memgraphRelationshipImportQuery("ENTITY_kb", "\n"); err == nil {
		t.Fatal("expected an empty relationship type to be rejected")
	}
}

// Memgraph has no elementId(); #3950 added it to the search ordering as the
// final tie breaker, which made the shared query fail on Memgraph with
// "Function 'elementId' doesn't exist".
func TestGraphSearchCypherUsesTheEngineIdentityFunction(t *testing.T) {
	neo4jQuery, _ := graphSearchCypher(EngineNeo4j, "ENTITY_kb", []string{"Acme"})
	for _, want := range []string{"n.kg, elementId(n)", "ORDER BY seed_index, elementId(r)"} {
		if !strings.Contains(neo4jQuery, want) {
			t.Errorf("the Neo4j ordering must keep %q: %s", want, neo4jQuery)
		}
	}

	memgraphQuery, _ := graphSearchCypher(EngineMemgraph, "ENTITY_kb", []string{"Acme"})
	for _, want := range []string{"n.kg, id(n)", "ORDER BY seed_index, id(r)"} {
		if !strings.Contains(memgraphQuery, want) {
			t.Errorf("the Memgraph ordering must use %q: %s", want, memgraphQuery)
		}
	}
	if strings.Contains(memgraphQuery, "elementId(") {
		t.Errorf("Memgraph has no elementId(): %s", memgraphQuery)
	}
	// Everything but the identity function has to stay shared; a second copy of
	// the query would drift the next time the ordering changes.
	if strings.ReplaceAll(memgraphQuery, "id(", "elementId(") != neo4jQuery {
		t.Errorf("the two queries differ by more than the identity function:\n%s\n%s",
			neo4jQuery, memgraphQuery)
	}
}

// Memgraph has no apoc.periodic.iterate, so the batching lives in Go: each
// batch is capped and reports its row count, and the caller commits one batch
// per transaction until a batch comes back empty.
func TestMemgraphDeleteQueriesAreBoundedAndCounted(t *testing.T) {
	deleteRels, deleteNodes := memgraphDeleteQueries("ENTITY_kb", 1000)
	for _, query := range []string{deleteRels, deleteNodes} {
		for _, want := range []string{"LIMIT 1000", "RETURN count(*) AS deleted"} {
			if !strings.Contains(query, want) {
				t.Errorf("delete batch missing %q: %s", want, query)
			}
		}
		if strings.Index(query, "LIMIT 1000") > strings.Index(query, "DELETE") {
			t.Errorf("the cap must be applied before the delete: %s", query)
		}
	}
	// Relationships are matched undirected, so the same relationship can arrive
	// twice; without DISTINCT a batch would spend its cap on duplicates.
	if !strings.Contains(deleteRels, "WITH DISTINCT r LIMIT") {
		t.Errorf("relationship batches must be deduplicated: %s", deleteRels)
	}
	// The node batch runs after the relationship batches but must not depend on
	// them having removed every edge: a node with a surviving edge would make a
	// plain DELETE fail and leave the namespace behind.
	if !strings.Contains(deleteNodes, "DETACH DELETE n") {
		t.Errorf("node batches must detach: %s", deleteNodes)
	}
	if other, _ := memgraphDeleteQueries("ENTITY_kb", 7); !strings.Contains(other, "LIMIT 7") {
		t.Errorf("batch size is not applied: %s", other)
	}
}
