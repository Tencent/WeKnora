package neo4j

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	driver "github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type graphWriteDriver struct {
	driver.Driver
	session *graphWriteSession
}

func (d *graphWriteDriver) NewSession(context.Context, driver.SessionConfig) driver.Session {
	return d.session
}

type graphWriteSession struct {
	driver.Session
	tx          *graphWriteTransaction
	callbackErr error
	closed      bool
}

func (s *graphWriteSession) ExecuteWrite(_ context.Context, work driver.ManagedTransactionWork,
	_ ...func(*driver.TransactionConfig),
) (any, error) {
	value, err := work(s.tx)
	s.callbackErr = err
	return value, err
}

func (s *graphWriteSession) Close(context.Context) error {
	s.closed = true
	return nil
}

type graphWriteTransaction struct {
	driver.ManagedTransaction
	failStatement int
	failOnConsume bool
	failure       error
	calls         int
	consumed      int
	params        []map[string]any
	queries       []string
}

func (tx *graphWriteTransaction) Run(_ context.Context, query string, params map[string]any) (driver.Result, error) {
	tx.calls++
	tx.params = append(tx.params, params)
	tx.queries = append(tx.queries, query)
	var err error
	if tx.calls == tx.failStatement {
		err = tx.failure
	}
	if err != nil && !tx.failOnConsume {
		return nil, err
	}
	return &graphWriteResult{tx: tx, err: err}, nil
}

type graphWriteResult struct {
	driver.Result
	tx  *graphWriteTransaction
	err error
}

func (r *graphWriteResult) Consume(context.Context) (driver.ResultSummary, error) {
	r.tx.consumed++
	return nil, r.err
}

func TestAddGraphPreservesTransactionFailures(t *testing.T) {
	for _, failure := range []struct {
		name      string
		err       error
		retryable bool
	}{
		{"deadlock", &driver.Neo4jError{Code: "Neo.TransientError.Transaction.DeadlockDetected"}, true},
		{"invalid query", &driver.Neo4jError{Code: "Neo.ClientError.Statement.SyntaxError"}, false},
		{"cancelled", context.Canceled, false},
	} {
		for _, statement := range []int{1, 2, 3} {
			for _, consume := range []bool{false, true} {
				phase := "run"
				if consume {
					phase = "consume"
				}
				name := []string{"backfill", "nodes", "relationships"}[statement-1]
				t.Run(failure.name+"/"+name+"/"+phase, func(t *testing.T) {
					tx := &graphWriteTransaction{failStatement: statement, failOnConsume: consume, failure: failure.err}
					session := &graphWriteSession{tx: tx}
					repo := NewNeo4jRepository(&graphWriteDriver{session: session})
					err := repo.AddGraph(context.Background(), types.NameSpace{KnowledgeBase: "kb", Knowledge: "doc"},
						[]*types.GraphData{{}, {}},
					)
					require.Error(t, err)
					assert.ErrorIs(t, err, failure.err)
					// ExecuteWrite must receive a recognizable transient error so its own
					// bounded retry can reuse the graph, without re-running LLM extraction.
					assert.Equal(t, failure.retryable, driver.IsRetryable(session.callbackErr))
					if failure.name == "deadlock" {
						var neoErr *driver.Neo4jError
						require.True(t, errors.As(session.callbackErr, &neoErr))
						assert.Same(t, failure.err, neoErr)
					}
					assert.Equal(t, statement, tx.calls, "stop on the failed statement, before the next graph")
					wantConsumed := statement - 1
					if consume {
						wantConsumed++
					}
					assert.Equal(t, wantConsumed, tx.consumed)
					assert.True(t, session.closed)
				})
			}
		}
	}
}

func TestAddGraphConsumesAllWriteResults(t *testing.T) {
	tx := &graphWriteTransaction{}
	session := &graphWriteSession{tx: tx}
	repo := NewNeo4jRepository(&graphWriteDriver{session: session})
	require.NoError(t, repo.AddGraph(context.Background(), types.NameSpace{}, []*types.GraphData{{}}))
	assert.Equal(t, 3, tx.calls)
	assert.Equal(t, 3, tx.consumed)
	assert.True(t, session.closed)
}

func TestAddGraphKeepsTypedIdentityAndOmitsProperties(t *testing.T) {
	tx := &graphWriteTransaction{}
	repo := NewNeo4jRepository(&graphWriteDriver{session: &graphWriteSession{tx: tx}})
	require.NoError(t, repo.AddGraph(context.Background(), types.NameSpace{KnowledgeBase: "kb", Knowledge: "doc"},
		[]*types.GraphData{{
			Node: []*types.GraphNode{
				{Name: "Alex", EntityType: "Person", Attributes: []string{"ignored"}, Chunks: []string{"chunk"}},
				{Name: "Alex", EntityType: "Company"},
				{Name: "Legacy", Attributes: []string{"retained"}},
			},
			Relation: []*types.GraphRelation{{
				Node1: "Alex", Node2: "Alex", Type: "WORKS_AT",
				SourceType: "Person", TargetType: "Company",
			}},
		}}))
	assert.Equal(t, schemaEntityConstraintQuery, tx.queries[0])
	assert.Contains(t, tx.queries[2], "SET n:WEKNORA_SCHEMA_ENTITY, n.schema_scope = $scope")
	nodes := tx.params[3]["data"].([]map[string]any)
	assert.NotEqual(t, nodes[0]["identity"], nodes[1]["identity"])
	assert.Empty(t, nodes[0]["props"])
	assert.Equal(t, map[string]any{"attributes": []string{"retained"}}, nodes[2]["props"])
	assert.Equal(t, []string{"ENTITYkb", "ENTITYdoc", schemaEntityLabel}, nodes[0]["labels"],
		"type labels must not participate in merge matching for pre-label records")
	assert.Equal(t, nodes[0]["labels"], nodes[1]["labels"])
	assert.Equal(t, []string{"TYPE_Person"}, nodes[0]["type_labels"])
	assert.Equal(t, []string{"TYPE_Company"}, nodes[1]["type_labels"])
	assert.Equal(t, []string{}, nodes[2]["type_labels"])
	assert.Contains(t, tx.queries[3], "apoc.merge.node(row.labels, row.identity")
	assert.Contains(t, tx.queries[3], "apoc.create.addLabels(node, row.type_labels)")
	relations := tx.params[4]["data"].([]map[string]any)
	assert.Equal(t, nodes[0]["identity"], relations[0]["source_identity"])
	assert.Equal(t, nodes[1]["identity"], relations[0]["target_identity"])
	assert.Equal(t, nodes[0]["labels"], relations[0]["source_labels"])
	assert.Equal(t, nodes[1]["labels"], relations[0]["target_labels"])
	assert.Equal(t, nodes[0]["type_labels"], relations[0]["source_type_labels"])
	assert.Equal(t, nodes[1]["type_labels"], relations[0]["target_type_labels"])
	assert.Contains(t, tx.queries[4], "apoc.create.addLabels(source, row.source_type_labels)")
	assert.Contains(t, tx.queries[4], "apoc.create.addLabels(target, row.target_type_labels)")
	assert.Equal(t, "doc", tx.params[1]["kg"], "legacy backfill stays in the document")
	assert.Equal(t, "kb", nodes[0]["identity"].(map[string]any)["schema_scope"])
	assert.NotContains(t, nodes[2]["identity"], "schema_scope")
}

func TestSchemaConstraintInitializationRetriesAndCachesOnlySuccess(t *testing.T) {
	tx := &graphWriteTransaction{failStatement: 1, failure: context.Canceled}
	repo := NewNeo4jRepository(&graphWriteDriver{session: &graphWriteSession{tx: tx}}).(*Neo4jRepository)
	require.ErrorIs(t, repo.ensureSchemaConstraint(context.Background()), context.Canceled)
	require.False(t, repo.schemaConstraintReady)
	require.NoError(t, repo.ensureSchemaConstraint(context.Background()))
	require.True(t, repo.schemaConstraintReady)
	require.NoError(t, repo.ensureSchemaConstraint(context.Background()))
	assert.Equal(t, 2, tx.calls)
}

func TestTypedGraphWriteFailuresStopBeforeNextStatement(t *testing.T) {
	for statement := 1; statement <= 5; statement++ {
		for _, consume := range []bool{false, true} {
			failure := &driver.Neo4jError{Code: "Neo.TransientError.Transaction.DeadlockDetected"}
			tx := &graphWriteTransaction{failStatement: statement, failOnConsume: consume, failure: failure}
			session := &graphWriteSession{tx: tx}
			repo := NewNeo4jRepository(&graphWriteDriver{session: session})
			err := repo.AddGraph(context.Background(), types.NameSpace{KnowledgeBase: "kb", Knowledge: "doc"},
				[]*types.GraphData{{Node: []*types.GraphNode{{Name: "Alex", EntityType: "Person"}}}})
			require.ErrorIs(t, err, failure)
			assert.True(t, driver.IsRetryable(session.callbackErr))
			assert.Equal(t, statement, tx.calls)
		}
	}
}

func TestTypedGraphIdentityScope(t *testing.T) {
	scope := types.NameSpace{KnowledgeBase: "kb", Knowledge: "doc"}
	person := graphWriteIdentity("Alex", "Person", scope)
	assert.Equal(t, person, graphWriteIdentity("Alex", "Person", scope))
	assert.NotEqual(t, person, graphWriteIdentity("Alex", "Company", scope))
	assert.NotEqual(t, person, graphWriteIdentity("Alex", "Person",
		types.NameSpace{KnowledgeBase: "kb", Knowledge: "other"}))
	assert.NotEqual(t, person, graphWriteIdentity("Alex", "Person",
		types.NameSpace{KnowledgeBase: "other", Knowledge: "doc"}))
	repo := NewNeo4jRepository(nil).(*Neo4jRepository)
	assert.Equal(t, []string{"ENTITYkb", "ENTITYdoc"}, repo.graphWriteLabels(scope, ""))
}

func TestGraphTypeLabelsPreserveExactNames(t *testing.T) {
	for _, name := range []string{
		"Person", "person", "A-B", "A_B", "ENTITYkb", "TYPE_Person", "\u4f01\u4e1a`\u7c7b\u578b",
	} {
		assert.Equal(t, "TYPE_"+name, graphTypeLabel(name))
		assert.Equal(t, []string{"TYPE_" + name}, graphTypeLabels(name))
	}
	assert.Empty(t, graphTypeLabel(""))
	assert.Equal(t, []string{}, graphTypeLabels(""))
}
