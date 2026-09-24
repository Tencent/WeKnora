package mastery

import "testing"

func TestComputeBoundaryColdStartReturnsNil(t *testing.T) {
	levels := map[string]int{"a": 10, "b": 0} // nothing reaches familiar
	adj := map[string][]string{"a": {"b"}, "b": {"a"}}
	if got := ComputeBoundary(levels, adj, FamiliarLevel, 10); got != nil {
		t.Fatalf("cold start should return nil, got %v", got)
	}
}

func TestComputeBoundaryHighlightsUnfamiliarNeighbors(t *testing.T) {
	// a is familiar; b and c are its neighbors; d is two hops away.
	levels := map[string]int{"a": 60, "b": 0, "c": 10, "d": 0}
	adj := map[string][]string{
		"a": {"b", "c"},
		"b": {"a", "d"},
		"c": {"a"},
		"d": {"b"},
	}
	got := ComputeBoundary(levels, adj, FamiliarLevel, 10)
	if len(got) == 0 {
		t.Fatalf("expected boundary candidates, got none")
	}
	set := make(map[string]bool, len(got))
	for _, s := range got {
		set[s.Slug] = true
	}
	if set["a"] {
		t.Fatalf("familiar node must not be a boundary candidate: %v", got)
	}
	// The one-hop unfamiliar neighbor b must rank ahead of the two-hop d.
	if !set["b"] {
		t.Fatalf("one-hop unfamiliar neighbor b should be a boundary candidate: %v", got)
	}
}

func TestComputeBoundaryExcludesAlreadyFamiliar(t *testing.T) {
	levels := map[string]int{"a": 80, "b": 50, "c": 20}
	adj := map[string][]string{
		"a": {"b"},
		"b": {"a", "c"},
		"c": {"b"},
	}
	got := ComputeBoundary(levels, adj, FamiliarLevel, 10)
	for _, s := range got {
		if levels[s.Slug] >= FamiliarLevel {
			t.Fatalf("already-familiar node %q (%d) leaked into boundary: %v", s.Slug, levels[s.Slug], got)
		}
	}
	if len(got) != 1 || got[0].Slug != "c" {
		t.Fatalf("expected only c as boundary candidate, got %v", got)
	}
}

func TestComputeBoundaryRespectsTopK(t *testing.T) {
	// One familiar hub with five unfamiliar neighbours; topK must cap the result.
	levels := map[string]int{"a": 60}
	adj := map[string][]string{"a": {}}
	for _, n := range []string{"b", "c", "d", "e", "f"} {
		levels[n] = 0
		adj["a"] = append(adj["a"], n)
		adj[n] = []string{"a"}
	}
	got := ComputeBoundary(levels, adj, FamiliarLevel, 2)
	if len(got) != 2 {
		t.Fatalf("topK=2 should return exactly 2 candidates, got %d: %v", len(got), got)
	}
}

func TestComputeBoundaryOrderIsDeterministic(t *testing.T) {
	// Candidates that tie on score must come back in the same order every run.
	levels := map[string]int{"a": 60, "b": 0, "c": 0, "d": 0}
	adj := map[string][]string{
		"a": {"b", "c", "d"},
		"b": {"a"},
		"c": {"a"},
		"d": {"a"},
	}
	first := ComputeBoundary(levels, adj, FamiliarLevel, 10)
	if len(first) == 0 {
		t.Fatalf("expected boundary candidates")
	}
	for run := 0; run < 5; run++ {
		got := ComputeBoundary(levels, adj, FamiliarLevel, 10)
		if len(got) != len(first) {
			t.Fatalf("run %d returned %d candidates, want %d", run, len(got), len(first))
		}
		for i := range got {
			if got[i].Slug != first[i].Slug {
				t.Fatalf("run %d differs at position %d: %v vs %v", run, i, got, first)
			}
		}
	}
}

func TestComputeBoundaryHandlesIsolatedFamiliarNode(t *testing.T) {
	// The only familiar node has no links at all: no frontier, and no panic.
	levels := map[string]int{"a": 90, "b": 0}
	adj := map[string][]string{"a": {}, "b": {}}
	if got := ComputeBoundary(levels, adj, FamiliarLevel, 10); len(got) != 0 {
		t.Fatalf("isolated familiar node should yield no boundary, got %v", got)
	}
}
