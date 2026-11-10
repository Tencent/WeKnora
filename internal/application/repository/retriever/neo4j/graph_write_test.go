package neo4j

import (
	"context"
	"errors"
	"fmt"
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
	queries       []string
	payloads      []map[string]any
}

func (tx *graphWriteTransaction) Run(_ context.Context, query string, params map[string]any) (driver.Result, error) {
	tx.queries = append(tx.queries, query)
	tx.payloads = append(tx.payloads, params)
	tx.calls++
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

func TestAddGraphUsesStableNodeAndRelationshipOrder(t *testing.T) {
	graphA := &types.GraphData{
		Node: []*types.GraphNode{{Name: "z"}, {Name: "a"}, nil},
		Relation: []*types.GraphRelation{
			{Node1: "z", Node2: "a", Type: "R"},
			{Node1: "c", Node2: "b", Type: "R"},
			{Node1: "a", Node2: "z", Type: "R"},
			nil,
		},
	}
	graphB := &types.GraphData{
		Node: []*types.GraphNode{{Name: "a"}, {Name: "z"}},
		Relation: []*types.GraphRelation{
			{Node1: "a", Node2: "z", Type: "R"},
			{Node1: "c", Node2: "b", Type: "R"},
			{Node1: "z", Node2: "a", Type: "R"},
		},
	}
	graphs := []*types.GraphData{graphA, graphB}
	var firstNodes, firstRelations []string
	for i, graph := range graphs {
		t.Run(fmt.Sprintf("order-%d", i), func(t *testing.T) {
			tx := &graphWriteTransaction{}
			session := &graphWriteSession{tx: tx}
			repo := NewNeo4jRepository(&graphWriteDriver{session: session})
			require.NoError(t, repo.AddGraph(
				context.Background(), types.NameSpace{Knowledge: "doc"}, []*types.GraphData{graph},
			))
			require.Len(t, tx.payloads, 2)
			nodes, relations := nodeNames(tx.payloads[0]), relationKeys(tx.payloads[1])
			assert.Equal(t, []string{"a", "z"}, nodes)
			assert.Equal(t, []string{"a->z", "z->a", "c->b"}, relations,
				"rows must sort by the canonical first/second endpoint pair, not original source")
			if firstNodes == nil {
				firstNodes, firstRelations = nodes, relations
			} else {
				assert.Equal(t, firstNodes, nodes, "node write order must not depend on model output order")
				assert.Equal(
					t, firstRelations, relations,
					"relationship write order must not depend on model output order",
				)
			}
		})
	}
}

func nodeNames(params map[string]any) []string {
	rows := params["data"].([]map[string]interface{})
	names := make([]string, len(rows))
	for i, row := range rows {
		names[i] = row["name"].(string)
	}
	return names
}

func relationKeys(params map[string]any) []string {
	return relationKeysFromRows(params["data"].([]map[string]interface{}))
}

func relationKeysFromRows(rows []map[string]interface{}) []string {
	keys := make([]string, len(rows))
	for i, row := range rows {
		keys[i] = row["source"].(string) + "->" + row["target"].(string)
	}
	return keys
}

func TestCanonicalRelationOrderLeavesNoInversionAtAdjacentRows(t *testing.T) {
	// A raw-source sort can leave (a,z) before (b,c), but the statement locks
	// a then z, so starting (b,c) next is an inversion if b sorts between them.
	// Sorting by the canonical first/second endpoint pair removes that gap.
	rows := []map[string]interface{}{
		{"source": "z", "target": "a", "type": "R"},
		{"source": "c", "target": "b", "type": "R"},
		{"source": "a", "target": "z", "type": "R"},
	}
	sortRelationRowsForLockOrder(rows)
	require.Equal(t, []string{"a->z", "z->a", "c->b"}, relationKeysFromRows(rows))
}

func TestCanonicalRelationOrderIsStableForOppositeDirections(t *testing.T) {
	rows := []map[string]interface{}{
		{"source": "z", "target": "a", "type": "R"},
		{"source": "a", "target": "z", "type": "R"},
	}
	sortRelationRowsForLockOrder(rows)
	require.Equal(t, []string{"a->z", "z->a"}, relationKeysFromRows(rows),
		"equal canonical endpoints need a deterministic original-direction tie-breaker")
}

func TestRelationshipQueryMergesEndpointsInCanonicalOrder(t *testing.T) {
	tx := &graphWriteTransaction{}
	session := &graphWriteSession{tx: tx}
	repo := NewNeo4jRepository(&graphWriteDriver{session: session})
	require.NoError(t, repo.AddGraph(context.Background(), types.NameSpace{Knowledge: "doc"}, []*types.GraphData{{
		Relation: []*types.GraphRelation{{Node1: "z", Node2: "a", Type: "R"}},
	}}))
	require.Len(t, tx.queries, 2)
	query := tx.queries[1]
	assert.Contains(t, query, "ORDER BY first_name ASC, second_name ASC, row.type ASC",
		"the database, not just the Go slice, must establish canonical relationship lock order")
	assert.Contains(t, query, "CASE WHEN row.source <= row.target THEN row.source ELSE row.target END")
	assert.Contains(t, query, "CASE WHEN row.source <= row.target THEN row.source_labels ELSE row.target_labels END")
	assert.Contains(t, query, "CASE WHEN row.source <= row.target THEN row.target_labels ELSE row.source_labels END")
	assert.Contains(t, query, "CASE WHEN row.source = first_name THEN first_node ELSE second_node END")
	assert.Contains(t, query, "CASE WHEN row.target = first_name THEN first_node ELSE second_node END")
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
		for _, statement := range []int{1, 2} {
			for _, consume := range []bool{false, true} {
				phase := "run"
				if consume {
					phase = "consume"
				}
				name := "nodes"
				if statement == 2 {
					name = "relationships"
				}
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

func TestAddGraphConsumesBothWriteResults(t *testing.T) {
	tx := &graphWriteTransaction{}
	session := &graphWriteSession{tx: tx}
	repo := NewNeo4jRepository(&graphWriteDriver{session: session})
	require.NoError(t, repo.AddGraph(context.Background(), types.NameSpace{}, []*types.GraphData{{}}))
	assert.Equal(t, 2, tx.calls)
	assert.Equal(t, 2, tx.consumed)
	assert.True(t, session.closed)
}
