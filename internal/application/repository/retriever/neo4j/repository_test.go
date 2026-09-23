package neo4j

import (
	"strings"
	"testing"
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
		"LIMIT $maxSeedNodes",
		"LIMIT $maxRows",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("query is missing %q:\n%s", want, query)
		}
	}

	// The ordering key has to reach both caps: a row LIMIT without it keeps an
	// arbitrary subset of the rows.
	if got := strings.Count(query, "ORDER BY seed_rank, name_len, name"); got != 2 {
		t.Errorf("ORDER BY applied %d time(s), want 2 (seed cap and row cap):\n%s", got, query)
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
