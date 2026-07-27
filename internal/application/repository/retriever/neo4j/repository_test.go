package neo4j

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	neodriver "github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

type fakeManagedTransaction struct {
	runErr  error
	queries []string
	params  []map[string]any
}

func (t *fakeManagedTransaction) Run(
	_ context.Context,
	query string,
	params map[string]any,
) (neodriver.Result, error) {
	t.queries = append(t.queries, query)
	t.params = append(t.params, params)
	if t.runErr != nil {
		return nil, t.runErr
	}
	return nil, nil
}

type fakeGraphWriteSession struct {
	transactions []*fakeManagedTransaction
	executed     int
	committed    int
	closed       bool
}

func (s *fakeGraphWriteSession) ExecuteWrite(
	ctx context.Context,
	work neodriver.ManagedTransactionWork,
	_ ...func(*neodriver.TransactionConfig),
) (any, error) {
	if s.executed >= len(s.transactions) {
		return nil, errors.New("unexpected ExecuteWrite call")
	}
	tx := s.transactions[s.executed]
	s.executed++
	result, err := work(tx)
	if err != nil {
		return nil, err
	}
	s.committed++
	return result, nil
}

func (s *fakeGraphWriteSession) Close(context.Context) error {
	s.closed = true
	return nil
}

func TestAddGraph_SingleRelationshipFailureKeepsCommittedNodes(t *testing.T) {
	nodeTx := &fakeManagedTransaction{}
	relationTx := &fakeManagedTransaction{runErr: errors.New("invalid relationship")}
	session := &fakeGraphWriteSession{
		transactions: []*fakeManagedTransaction{nodeTx, relationTx},
	}
	repository := &Neo4jRepository{
		nodePrefix: "ENTITY",
		newWriteSession: func(context.Context) graphWriteSession {
			return session
		},
	}
	graph := &types.GraphData{
		Node: []*types.GraphNode{
			{Name: "Alice", Chunks: []string{"chunk-1"}},
			{Name: "Bob", Chunks: []string{"chunk-1"}},
		},
		Relation: []*types.GraphRelation{
			{Node1: "Alice", Node2: "Bob", Type: "knows"},
		},
	}

	result, err := repository.addGraph(context.Background(), types.NameSpace{
		KnowledgeBase: "kb-1",
		Knowledge:     "knowledge-1",
	}, graph)
	if err != nil {
		t.Fatalf("single invalid relationship should be isolated, got %v", err)
	}
	if result.NodesWritten != 2 || result.RelationsWritten != 0 || result.RelationsFailed != 1 {
		t.Fatalf("write result = %+v, want two nodes written and one relationship isolated", result)
	}
	if len(result.Failures) != 1 || result.Failures[0].Type != "knows" {
		t.Fatalf("failure details = %+v, want the rejected relationship", result.Failures)
	}
	if session.executed != 2 {
		t.Fatalf("ExecuteWrite calls = %d, want separate node and relationship transactions", session.executed)
	}
	if session.committed != 1 {
		t.Fatalf("committed transactions = %d, want the node transaction committed", session.committed)
	}
	if len(nodeTx.queries) != 1 || !strings.Contains(nodeTx.queries[0], "apoc.merge.node") {
		t.Fatalf("node transaction queries = %#v", nodeTx.queries)
	}
	if len(relationTx.queries) != 1 || !strings.Contains(relationTx.queries[0], "apoc.merge.relationship") {
		t.Fatalf("relationship transaction queries = %#v", relationTx.queries)
	}
	if !session.closed {
		t.Fatal("write session was not closed")
	}
}

func relationshipBatchSize(t *testing.T, tx *fakeManagedTransaction) int {
	t.Helper()
	if len(tx.params) != 1 {
		t.Fatalf("transaction params = %#v, want one query parameter set", tx.params)
	}
	data, ok := tx.params[0]["data"].([]map[string]interface{})
	if !ok {
		t.Fatalf("relationship data type = %T", tx.params[0]["data"])
	}
	return len(data)
}

func TestAddGraph_FailedRelationshipBatchIsBisected(t *testing.T) {
	transactions := []*fakeManagedTransaction{
		{}, // nodes: commit
		{runErr: errors.New("batch contains bad row")}, // 4 relations: rollback
		{}, // left 2: commit
		{runErr: errors.New("batch contains bad row")}, // right 2: rollback
		{runErr: errors.New("bad relation")},           // bad singleton: skip
		{},                                             // final singleton: commit
	}
	session := &fakeGraphWriteSession{transactions: transactions}
	repository := &Neo4jRepository{
		nodePrefix: "ENTITY",
		newWriteSession: func(context.Context) graphWriteSession {
			return session
		},
	}
	graph := &types.GraphData{
		Node: []*types.GraphNode{{Name: "Alice"}, {Name: "Bob"}},
		Relation: []*types.GraphRelation{
			{Node1: "Alice", Node2: "Bob", Type: "r1"},
			{Node1: "Alice", Node2: "Bob", Type: "r2"},
			{Node1: "Alice", Node2: "Bob", Type: "bad"},
			{Node1: "Alice", Node2: "Bob", Type: "r4"},
		},
	}

	result, err := repository.addGraph(context.Background(), types.NameSpace{Knowledge: "knowledge-1"}, graph)
	if err != nil {
		t.Fatalf("isolatable relationship failure should not fail graph write: %v", err)
	}
	if result.RelationsWritten != 3 || result.RelationsFailed != 1 {
		t.Fatalf("relationship statistics = %+v, want 3 written and 1 failed", result)
	}
	if len(result.Failures) != 1 || result.Failures[0].ItemIndex != 2 || result.Failures[0].Type != "bad" {
		t.Fatalf("failure details = %+v, want relation index 2", result.Failures)
	}
	if session.executed != 6 {
		t.Fatalf("ExecuteWrite calls = %d, want node + five relationship attempts", session.executed)
	}
	if session.committed != 3 {
		t.Fatalf("committed transactions = %d, want nodes + left pair + final singleton", session.committed)
	}
	wantSizes := []int{4, 2, 2, 1, 1}
	for i, want := range wantSizes {
		if got := relationshipBatchSize(t, transactions[i+1]); got != want {
			t.Fatalf("relationship attempt %d size = %d, want %d", i, got, want)
		}
	}
}

func TestAddGraph_RetryableRelationshipErrorIsNotBisected(t *testing.T) {
	retryable := &neodriver.Neo4jError{
		Code: "Neo.TransientError.Transaction.DeadlockDetected",
		Msg:  "deadlock",
	}
	session := &fakeGraphWriteSession{transactions: []*fakeManagedTransaction{
		{},
		{runErr: retryable},
	}}
	repository := &Neo4jRepository{
		nodePrefix: "ENTITY",
		newWriteSession: func(context.Context) graphWriteSession {
			return session
		},
	}
	graph := &types.GraphData{
		Node: []*types.GraphNode{{Name: "Alice"}, {Name: "Bob"}},
		Relation: []*types.GraphRelation{
			{Node1: "Alice", Node2: "Bob", Type: "r1"},
			{Node1: "Alice", Node2: "Bob", Type: "r2"},
		},
	}

	result, err := repository.addGraph(context.Background(), types.NameSpace{Knowledge: "knowledge-1"}, graph)
	if err == nil || !strings.Contains(err.Error(), "deadlock") {
		t.Fatalf("error = %v, want retryable infrastructure error", err)
	}
	if result.NodesWritten != 2 || result.RelationsWritten != 0 || result.RelationsFailed != 0 {
		t.Fatalf("write result = %+v, infrastructure error must not be reported as a bad row", result)
	}
	if session.executed != 2 {
		t.Fatalf("ExecuteWrite calls = %d, retryable error must not be bisected", session.executed)
	}
	if session.committed != 1 {
		t.Fatalf("committed transactions = %d, want only node transaction", session.committed)
	}
}

func TestAddGraph_RelationshipsUseConfiguredBatchSize(t *testing.T) {
	relations := make([]*types.GraphRelation, relationshipWriteBatchSize+1)
	for i := range relations {
		relations[i] = &types.GraphRelation{Node1: "Alice", Node2: "Bob", Type: "related"}
	}
	transactions := []*fakeManagedTransaction{{}, {}, {}}
	session := &fakeGraphWriteSession{transactions: transactions}
	repository := &Neo4jRepository{
		nodePrefix: "ENTITY",
		newWriteSession: func(context.Context) graphWriteSession {
			return session
		},
	}
	graph := &types.GraphData{
		Node:     []*types.GraphNode{{Name: "Alice"}, {Name: "Bob"}},
		Relation: relations,
	}

	result, err := repository.addGraph(context.Background(), types.NameSpace{Knowledge: "knowledge-1"}, graph)
	if err != nil {
		t.Fatalf("batched relationship write failed: %v", err)
	}
	if result.RelationsWritten != len(relations) || result.RelationsFailed != 0 {
		t.Fatalf("relationship statistics = %+v, want all relationships written", result)
	}
	if got := relationshipBatchSize(t, transactions[1]); got != relationshipWriteBatchSize {
		t.Fatalf("first batch size = %d, want %d", got, relationshipWriteBatchSize)
	}
	if got := relationshipBatchSize(t, transactions[2]); got != 1 {
		t.Fatalf("second batch size = %d, want 1", got)
	}
}

func TestAddGraph_NodeFailureStopsBeforeRelationshipTransaction(t *testing.T) {
	nodeTx := &fakeManagedTransaction{runErr: errors.New("node write failed")}
	session := &fakeGraphWriteSession{transactions: []*fakeManagedTransaction{nodeTx}}
	repository := &Neo4jRepository{
		nodePrefix: "ENTITY",
		newWriteSession: func(context.Context) graphWriteSession {
			return session
		},
	}
	graph := &types.GraphData{
		Node:     []*types.GraphNode{{Name: "Alice"}, {Name: "Bob"}},
		Relation: []*types.GraphRelation{{Node1: "Alice", Node2: "Bob", Type: "knows"}},
	}

	result, err := repository.addGraph(context.Background(), types.NameSpace{Knowledge: "knowledge-1"}, graph)
	if err == nil || !strings.Contains(err.Error(), "failed to create nodes") {
		t.Fatalf("error = %v, want node creation failure", err)
	}
	if result.NodesWritten != 0 || result.NodesFailed != 2 {
		t.Fatalf("node statistics = %+v, want both nodes failed", result)
	}
	if session.executed != 1 {
		t.Fatalf("ExecuteWrite calls = %d, want relationship transaction not to start", session.executed)
	}
	if session.committed != 0 {
		t.Fatalf("committed transactions = %d, want none", session.committed)
	}
}
