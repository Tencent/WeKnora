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
	query, params := graphSearchCypher("ENTITY_kb", []string{"安恒"})

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
