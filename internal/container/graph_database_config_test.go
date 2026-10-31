package container

import (
	"context"
	"errors"
	"reflect"
	"testing"

	neo4jRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/neo4j"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
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

func TestGraphDatabaseAuth(t *testing.T) {
	for _, test := range []struct {
		name     string
		engine   neo4jRepo.GraphEngine
		username string
		password string
		want     neo4j.AuthToken
	}{
		{
			name: "neo4j empty credentials stay basic", engine: neo4jRepo.EngineNeo4j,
			want: neo4j.BasicAuth("", "", ""),
		},
		{
			name: "neo4j explicit credentials", engine: neo4jRepo.EngineNeo4j,
			username: "user", password: "test-password", want: neo4j.BasicAuth("user", "test-password", ""),
		},
		{name: "memgraph no auth", engine: neo4jRepo.EngineMemgraph, want: neo4j.NoAuth()},
		{
			name: "memgraph explicit credentials", engine: neo4jRepo.EngineMemgraph,
			username: "user", password: "test-password", want: neo4j.BasicAuth("user", "test-password", ""),
		},
		{
			name: "memgraph username only stays basic", engine: neo4jRepo.EngineMemgraph,
			username: "user", want: neo4j.BasicAuth("user", "", ""),
		},
		{
			name: "memgraph password only stays basic", engine: neo4jRepo.EngineMemgraph,
			password: "test-password", want: neo4j.BasicAuth("", "test-password", ""),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := graphDatabaseAuth(test.engine, test.username, test.password)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatal("unexpected graph authentication token")
			}
		})
	}
}

type graphConnectionVerifierStub struct {
	authCalls         int
	connectivityCalls int
	err               error
}

func (s *graphConnectionVerifierStub) VerifyAuthentication(context.Context, *neo4j.AuthToken) error {
	s.authCalls++
	return s.err
}

func (s *graphConnectionVerifierStub) VerifyConnectivity(context.Context) error {
	s.connectivityCalls++
	return s.err
}

func TestVerifyGraphConnection(t *testing.T) {
	verificationErr := errors.New("verification failed")
	for _, engine := range []neo4jRepo.GraphEngine{neo4jRepo.EngineNeo4j, neo4jRepo.EngineMemgraph} {
		t.Run(string(engine), func(t *testing.T) {
			for _, wantErr := range []error{nil, verificationErr} {
				stub := &graphConnectionVerifierStub{err: wantErr}
				if err := verifyGraphConnection(context.Background(), stub, engine); !errors.Is(err, wantErr) {
					t.Fatalf("verification error = %v, want %v", err, wantErr)
				}
				if engine == neo4jRepo.EngineMemgraph {
					if stub.connectivityCalls != 1 || stub.authCalls != 0 {
						t.Fatal("Memgraph must verify connectivity without targeting Neo4j's system database")
					}
				} else if stub.authCalls != 1 || stub.connectivityCalls != 0 {
					t.Fatal("Neo4j must retain VerifyAuthentication")
				}
			}
		})
	}
}

func TestInitGraphDatabaseDisabled(t *testing.T) {
	t.Setenv("NEO4J_ENABLE", "false")
	t.Setenv("GRAPH_DATABASE_ENGINE", "invalid")
	driver, err := initNeo4jClient()
	if err != nil || driver != nil {
		t.Fatalf("disabled graph initialization = %v, %v; want nil, nil", driver, err)
	}
}

func TestInitGraphDatabaseRejectsInvalidEngine(t *testing.T) {
	t.Setenv("NEO4J_ENABLE", "true")
	t.Setenv("GRAPH_DATABASE_ENGINE", "invalid")
	driver, err := initNeo4jClient()
	if err == nil || driver != nil {
		t.Fatal("enabled graph initialization must reject an invalid engine before connecting")
	}
}
