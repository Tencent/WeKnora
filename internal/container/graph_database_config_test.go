package container

import (
	"testing"

	neo4jRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/neo4j"
)

func TestGraphDatabaseEngine(t *testing.T) {
	for _, test := range []struct {
		value string
		want  neo4jRepo.GraphEngine
	}{
		{value: "", want: neo4jRepo.EngineNeo4j},
		{value: " neo4j ", want: neo4jRepo.EngineNeo4j},
		{value: "MEMGRAPH", want: neo4jRepo.EngineMemgraph},
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
