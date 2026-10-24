package container

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	neo4jRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/neo4j"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// This intentionally calls the application's initializer, rather than creating
// a separate driver, so the live regression covers startup verification too.
// Run against a disposable instance, once without credentials and once with a
// configured Memgraph user. The authenticated case also rejects a bad password.
func TestInitMemgraphClientIntegration(t *testing.T) {
	uri := os.Getenv("WEKNORA_MEMGRAPH_URI")
	if uri == "" {
		t.Skip("set WEKNORA_MEMGRAPH_URI to test application graph initialization")
	}
	t.Setenv("NEO4J_ENABLE", "true")
	t.Setenv("GRAPH_DATABASE_ENGINE", "memgraph")
	t.Setenv("NEO4J_URI", uri)
	t.Setenv("NEO4J_USERNAME", os.Getenv("WEKNORA_MEMGRAPH_USERNAME"))
	t.Setenv("NEO4J_PASSWORD", os.Getenv("WEKNORA_MEMGRAPH_PASSWORD"))
	driver, err := initNeo4jClient()
	if err != nil {
		t.Fatal(err)
	}
	if driver == nil {
		t.Fatal("enabled Memgraph initialization returned a nil driver")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := driver.Close(ctx); err != nil {
			t.Errorf("close graph driver: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := neo4j.ExecuteQuery(ctx, driver, "RETURN 1 AS ready", nil, neo4j.EagerResultTransformer)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Records) != 1 || len(result.Records[0].Values) != 1 {
		t.Fatal("initialized driver did not return one readiness query result")
	}
	if result.Records[0].Values[0] != int64(1) {
		t.Fatal("initialized driver did not return the readiness query result")
	}

	if username := os.Getenv("WEKNORA_MEMGRAPH_USERNAME"); username != "" {
		t.Run("reject invalid credentials", func(t *testing.T) {
			// A fresh driver ensures this must perform the Bolt authentication
			// handshake, rather than reuse the valid connection above.
			invalidPassword := "weknora-invalid-" + strconv.FormatInt(time.Now().UnixNano(), 10)
			invalidDriver, err := neo4j.NewDriver(uri, graphDatabaseAuth(
				neo4jRepo.EngineMemgraph, username, invalidPassword,
			))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				closeCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
				defer stop()
				if err := invalidDriver.Close(closeCtx); err != nil {
					t.Errorf("close invalid-credentials driver: %v", err)
				}
			})
			verifyCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			err = verifyGraphConnection(verifyCtx, invalidDriver, neo4jRepo.EngineMemgraph)
			if err == nil {
				t.Fatal("invalid credentials were accepted; configure the Memgraph user before this test")
			}
			var authErr *neo4j.Neo4jError
			if !errors.As(err, &authErr) || authErr.Code != "Memgraph.ClientError.Security.Unauthenticated" {
				t.Fatalf("expected Memgraph authentication rejection, got %v", err)
			}
		})
	}
}
