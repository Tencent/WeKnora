package container

import "testing"

func TestGraphDatabaseEngine(t *testing.T) {
	for _, test := range []struct {
		value string
		want  string
	}{
		{value: "", want: "neo4j"},
		{value: " neo4j ", want: "neo4j"},
		{value: "MEMGRAPH", want: "memgraph"},
	} {
		got, err := graphDatabaseEngine(test.value)
		if err != nil || got != test.want {
			t.Errorf("graphDatabaseEngine(%q) = %q, %v; want %q", test.value, got, err, test.want)
		}
	}
	if _, err := graphDatabaseEngine("falkordb"); err == nil {
		t.Fatal("unsupported graph database engine should be rejected")
	}
}
