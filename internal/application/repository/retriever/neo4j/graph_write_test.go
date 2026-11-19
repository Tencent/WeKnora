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
}

func (tx *graphWriteTransaction) Run(context.Context, string, map[string]any) (driver.Result, error) {
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
