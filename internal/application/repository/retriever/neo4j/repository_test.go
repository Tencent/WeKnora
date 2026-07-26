package neo4j

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	driver "github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordedGraphQuery struct {
	cypher string
	params map[string]any
}

type recordingGraphTransaction struct {
	queries []recordedGraphQuery
	errAt   int
	err     error
}

func (tx *recordingGraphTransaction) Run(
	_ context.Context,
	cypher string,
	params map[string]any,
) (driver.Result, error) {
	tx.queries = append(tx.queries, recordedGraphQuery{cypher: cypher, params: params})
	if tx.errAt > 0 && len(tx.queries) == tx.errAt {
		return nil, tx.err
	}
	return nil, nil
}

func TestEnsureGraphOwnershipSchemaCreatesConstraintsOnce(t *testing.T) {
	tx := &recordingGraphTransaction{}
	writeCalls := 0
	repository := &Neo4jRepository{
		writeTransaction: func(ctx context.Context, work driver.ManagedTransactionWork) error {
			writeCalls++
			_, err := work(tx)
			return err
		},
	}

	require.NoError(t, repository.ensureGraphOwnershipSchema(context.Background()))
	require.NoError(t, repository.ensureGraphOwnershipSchema(context.Background()))

	assert.Equal(t, 1, writeCalls)
	require.Len(t, tx.queries, 3)
	assert.Contains(t, tx.queries[0].cypher, "GRAPH_OWNERSHIP_STATE")
	assert.Contains(t, tx.queries[0].cypher, "REQUIRE state.kg IS UNIQUE")
	assert.Contains(t, tx.queries[1].cypher, "GRAPH_CHUNK_OWNER")
	assert.Contains(t, tx.queries[1].cypher, "REQUIRE (owner.kg, owner.chunk_id) IS UNIQUE")
	assert.Contains(t, tx.queries[2].cypher, "GRAPH_ENTITY")
	assert.Contains(t, tx.queries[2].cypher, "REQUIRE (entity.kg, entity.name) IS UNIQUE")
}

func TestEnsureGraphChunkOwnerSerializesReplacementAndTracksInitialization(t *testing.T) {
	tx := &recordingGraphTransaction{}
	repository := &Neo4jRepository{nodePrefix: "ENTITY"}

	err := repository.ensureGraphChunkOwner(
		context.Background(),
		tx,
		types.NameSpace{Knowledge: "knowledge-1"},
		"chunk-1",
		7,
	)

	require.NoError(t, err)
	require.Len(t, tx.queries, 1)
	assert.Contains(t, tx.queries[0].cypher, "owner.initialized = false")
	assert.Contains(t, tx.queries[0].cypher, "owner.revision = 0")
	assert.Contains(t, tx.queries[0].cypher,
		"SET owner.revision = coalesce(owner.revision, 0) + 1")
	assert.Contains(t, tx.queries[0].cypher, "coalesce(state.attempt, 0) = $attempt")
	assert.Contains(t, tx.queries[0].cypher, "owner.attempt = $attempt")
	assert.Equal(t, 7, tx.queries[0].params["attempt"])
}

func TestFenceGraphAttemptAdvancesNamespaceGeneration(t *testing.T) {
	tx := &recordingGraphTransaction{}
	repository := &Neo4jRepository{
		nodePrefix:           "ENTITY",
		ownershipSchemaReady: true,
		writeTransaction: func(ctx context.Context, work driver.ManagedTransactionWork) error {
			_, err := work(tx)
			return err
		},
	}

	err := repository.FenceGraphAttempt(
		context.Background(),
		types.NameSpace{KnowledgeBase: "kb-1", Knowledge: "knowledge-1"},
		11,
	)

	require.NoError(t, err)
	require.Len(t, tx.queries, 1)
	assert.Contains(t, tx.queries[0].cypher, "apoc.lock.nodes([state])")
	assert.NotContains(t, tx.queries[0].cypher, "apoc.lock.write.nodes")
	assert.Contains(t, tx.queries[0].cypher,
		"coalesce(state.attempt, 0) > $attempt")
	assert.Contains(t, tx.queries[0].cypher, "graph attempt superseded")
	assert.Contains(t, tx.queries[0].cypher, "coalesce(state.attempt, 0) < $attempt")
	assert.Contains(t, tx.queries[0].cypher, "THEN $attempt")
	assert.Less(t,
		strings.Index(tx.queries[0].cypher, "apoc.lock.nodes([state])"),
		strings.Index(tx.queries[0].cypher, "coalesce(state.attempt, 0) > $attempt"),
	)
	assert.Equal(t, 11, tx.queries[0].params["attempt"])
}

func TestReplaceGraphChunkRetractsAndAddsOwnershipInOneTransaction(t *testing.T) {
	tx := &recordingGraphTransaction{}
	writeCalls := 0
	repository := &Neo4jRepository{
		nodePrefix:           "ENTITY",
		ownershipSchemaReady: true,
		writeTransaction: func(ctx context.Context, work driver.ManagedTransactionWork) error {
			writeCalls++
			_, err := work(tx)
			return err
		},
	}
	graph := &types.GraphData{
		Node: []*types.GraphNode{
			{Name: "Alpha", Chunks: []string{"wrong-owner"}, Attributes: []string{"person"}},
			{Name: "Beta"},
		},
		Relation: []*types.GraphRelation{
			{Node1: "Alpha", Node2: "Beta", Type: "knows", Chunks: []string{"wrong-owner"}},
		},
	}

	err := repository.ReplaceGraphChunk(
		context.Background(),
		types.NameSpace{KnowledgeBase: "kb-1", Knowledge: "knowledge-1"},
		"current-chunk",
		13,
		graph,
	)

	require.NoError(t, err)
	assert.Equal(t, 2, writeCalls)
	require.Len(t, tx.queries, 9)
	assert.Contains(t, tx.queries[0].cypher, "GRAPH_OWNERSHIP_STATE")
	assert.Contains(t, tx.queries[0].cypher, "coalesce(state.version, 0) < $schema_version")
	assert.Contains(t, tx.queries[0].cypher, "r.chunks IS NULL")
	assert.Contains(t, tx.queries[0].cypher, "apoc.refactor.mergeNodes")
	assert.Contains(t, tx.queries[0].cypher,
		"apoc.coll.union(chunks, coalesce(duplicate.chunks, []))")
	assert.Contains(t, tx.queries[0].cypher,
		"apoc.coll.union(attributes, coalesce(duplicate.attributes, []))")
	assert.Contains(t, tx.queries[0].cypher, "SET node.chunks = chunks")
	assert.Contains(t, tx.queries[0].cypher, "node.attributes = attributes")
	assert.Contains(t, tx.queries[0].cypher, "node.legacy_attributes = attributes")
	assert.Contains(t, tx.queries[0].cypher, "node.legacy_attribute_chunks = chunks")
	assert.Contains(t, tx.queries[0].cypher, "MERGE (owner:GRAPH_CHUNK_OWNER {")
	assert.Contains(t, tx.queries[0].cypher, "chunk_id: chunk_id")
	assert.Contains(t, tx.queries[0].cypher, "owner.initialized = true")
	assert.Contains(t, tx.queries[0].cypher, "owner.graph_version = 'legacy'")
	assert.Contains(t, tx.queries[0].cypher, "WITH state, entity, chunk_id")
	assert.Contains(t, tx.queries[0].cypher, "owner.attempt = coalesce(state.attempt, 0)")
	assert.Contains(t, tx.queries[0].cypher,
		"MERGE (owner)-[contribution:GRAPH_NODE_CONTRIBUTION]->(entity)")
	assert.Contains(t, tx.queries[0].cypher, "SET contribution.attributes = []")
	assert.Contains(t, tx.queries[0].cypher, "SET entity:GRAPH_ENTITY")
	assert.Less(t,
		strings.Index(tx.queries[0].cypher, "apoc.refactor.mergeNodes"),
		strings.Index(tx.queries[0].cypher, "SET entity:GRAPH_ENTITY"),
	)
	assert.Less(t,
		strings.Index(tx.queries[0].cypher, "state.version >= $schema_version"),
		strings.Index(tx.queries[0].cypher, "SET state.revision"),
	)
	assert.Contains(t, tx.queries[0].cypher, "WITH state, labeled,")
	assert.Contains(t, tx.queries[0].cypher, "[relation IN collect(DISTINCT r)")
	assert.Contains(t, tx.queries[1].cypher, "apoc.lock.read.nodes([state])")
	assert.Contains(t, tx.queries[2].cypher, "GRAPH_CHUNK_OWNER")
	assert.Contains(t, tx.queries[3].cypher, "owner.initialized = true")
	assert.Contains(t, tx.queries[3].cypher, "coalesce(owner.graph_version, '') <> $graph_version")
	assert.Contains(t, tx.queries[4].cypher, "owner.initialized = true")
	assert.Contains(t, tx.queries[4].cypher, "GRAPH_NODE_CONTRIBUTION")
	assert.Contains(t, tx.queries[4].cypher, "DELETE contribution")
	assert.Contains(t, tx.queries[4].cypher, "remaining.attributes")
	assert.Contains(t, tx.queries[4].cypher, "node.legacy_attribute_chunks")
	assert.Contains(t, tx.queries[4].cypher, "node.legacy_attributes")
	assert.Contains(t, tx.queries[5].cypher, "owner.initialized = true")
	assert.Contains(t, tx.queries[6].cypher, "coalesce(owner.graph_version, '') <> $graph_version")
	assert.Contains(t, tx.queries[7].cypher, "coalesce(owner.graph_version, '') <> $graph_version")
	assert.Less(t, strings.Index(tx.queries[3].cypher, "owner.graph_version"), strings.Index(tx.queries[3].cypher, "MATCH (n:"))
	assert.Less(t, strings.Index(tx.queries[5].cypher, "owner.graph_version"), strings.Index(tx.queries[5].cypher, "MATCH (n:"))
	assert.Less(t, strings.Index(tx.queries[6].cypher, "owner.graph_version"), strings.Index(tx.queries[6].cypher, "UNWIND $data"))
	assert.Less(t, strings.Index(tx.queries[7].cypher, "owner.graph_version"), strings.Index(tx.queries[7].cypher, "UNWIND $data"))
	assert.Contains(t, tx.queries[3].cypher, "coalesce(r.chunks, [])")
	assert.Contains(t, tx.queries[3].cypher, "DELETE r")
	assert.Contains(t, tx.queries[5].cypher, "coalesce(n.chunks, [])")
	assert.Contains(t, tx.queries[5].cypher, "DETACH DELETE n")
	assert.Contains(t, tx.queries[6].cypher, "MERGE (owner)-[contribution:GRAPH_NODE_CONTRIBUTION]->(node)")
	assert.Contains(t, tx.queries[6].cypher, "SET contribution.attributes = row.attributes")
	assert.Contains(t, tx.queries[6].cypher, "remaining.attributes")
	assert.Contains(t, tx.queries[6].cypher, "node.legacy_attributes")
	assert.Contains(t, tx.queries[6].cypher, "apoc.coll.union(coalesce(node.chunks, []), row.chunks)")
	assert.Contains(t, tx.queries[7].cypher, "CALL apoc.lock.nodes(")
	assert.Contains(t, tx.queries[7].cypher, "elementId(source) < elementId(target)")
	assert.Contains(t, tx.queries[7].cypher, "apoc.coll.union(coalesce(rel.chunks, []), row.chunks)")
	assert.Contains(t, tx.queries[8].cypher, "SET owner.graph_version = $graph_version")
	assert.Contains(t, tx.queries[8].cypher, "owner.initialized = true")
	for _, index := range []int{2, 3, 4, 5, 6, 7, 8} {
		assert.Equal(t, 13, tx.queries[index].params["attempt"])
	}
	for _, index := range []int{3, 4, 5, 6, 7, 8} {
		assert.Contains(t, tx.queries[index].cypher, "coalesce(state.attempt, 0) = $attempt")
		assert.Contains(t, tx.queries[index].cypher, "owner.attempt = $attempt")
	}

	nodeRows, ok := tx.queries[6].params["data"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, nodeRows, 2)
	assert.Equal(t, []string{"current-chunk"}, nodeRows[0]["chunks"])
	assert.Equal(t, []string{"person"}, nodeRows[0]["attributes"])
	assert.Contains(t, nodeRows[0]["labels"], "GRAPH_ENTITY")
	relationRows, ok := tx.queries[7].params["data"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, relationRows, 1)
	assert.Equal(t, []string{"current-chunk"}, relationRows[0]["chunks"])
	assert.Equal(t, []string{"current-chunk"}, tx.queries[3].params["chunk_ids"])
}

func TestReplaceGraphChunkStopsBeforePublishingAfterRetractionFailure(t *testing.T) {
	want := errors.New("neo4j unavailable")
	tx := &recordingGraphTransaction{errAt: 4, err: want}
	repository := &Neo4jRepository{
		nodePrefix:           "ENTITY",
		ownershipSchemaReady: true,
		writeTransaction: func(ctx context.Context, work driver.ManagedTransactionWork) error {
			_, err := work(tx)
			return err
		},
	}

	err := repository.ReplaceGraphChunk(
		context.Background(),
		types.NameSpace{KnowledgeBase: "kb-1", Knowledge: "knowledge-1"},
		"chunk-1",
		3,
		&types.GraphData{},
	)

	require.ErrorIs(t, err, want)
	assert.Len(t, tx.queries, 4)
}

func TestDelGraphChunksRetractsSharedOwnershipBeforeDeletingOwnerlessData(t *testing.T) {
	tx := &recordingGraphTransaction{}
	repository := &Neo4jRepository{
		nodePrefix:           "ENTITY",
		ownershipSchemaReady: true,
		writeTransaction: func(ctx context.Context, work driver.ManagedTransactionWork) error {
			_, err := work(tx)
			return err
		},
	}

	err := repository.DelGraphChunks(
		context.Background(),
		types.NameSpace{KnowledgeBase: "kb-1", Knowledge: "knowledge-1"},
		[]string{"chunk-1", "chunk-2"},
	)

	require.NoError(t, err)
	require.Len(t, tx.queries, 6)
	assert.Contains(t, tx.queries[0].cypher, "coalesce(state.version, 0) < $schema_version")
	assert.Contains(t, tx.queries[0].cypher, "r.chunks IS NULL")
	assert.Contains(t, tx.queries[1].cypher, "apoc.lock.read.nodes([state])")
	assert.Contains(t, tx.queries[2].cypher, "NOT (ownerID IN $chunk_ids)")
	assert.Contains(t, tx.queries[2].cypher, "size(r.chunks) = 0")
	assert.Contains(t, tx.queries[2].cypher, "WITH DISTINCT r")
	assert.Less(t,
		strings.Index(tx.queries[2].cypher, "WITH DISTINCT r"),
		strings.Index(tx.queries[2].cypher, "SET r.chunks"),
	)
	assert.Contains(t, tx.queries[3].cypher, "GRAPH_NODE_CONTRIBUTION")
	assert.Contains(t, tx.queries[3].cypher, "remaining.attributes")
	assert.Contains(t, tx.queries[4].cypher, "NOT (ownerID IN $chunk_ids)")
	assert.Contains(t, tx.queries[4].cypher, "size(n.chunks) = 0")
	assert.Contains(t, tx.queries[5].cypher, "DETACH DELETE owner")
	for _, query := range tx.queries {
		assert.False(t, strings.Contains(query.cypher, "apoc.periodic.iterate"))
	}
	assert.Equal(t, []string{"chunk-1", "chunk-2"}, tx.queries[2].params["chunk_ids"])
}

func TestRecoverGraphNamespaceDeletesOnlyDirtyNamespace(t *testing.T) {
	namespace := types.NameSpace{KnowledgeBase: "kb-1", Knowledge: "knowledge-1"}

	t.Run("clean", func(t *testing.T) {
		writeCalls := 0
		repository := &Neo4jRepository{
			graphRecoveryRequired: func(context.Context, types.NameSpace) (bool, error) {
				return false, nil
			},
			writeTransaction: func(context.Context, driver.ManagedTransactionWork) error {
				writeCalls++
				return nil
			},
		}

		require.NoError(t, repository.RecoverGraphNamespace(context.Background(), namespace))
		assert.Zero(t, writeCalls)
	})

	t.Run("dirty", func(t *testing.T) {
		var transactions []*recordingGraphTransaction
		repository := &Neo4jRepository{
			nodePrefix:           "ENTITY",
			ownershipSchemaReady: true,
			graphRecoveryRequired: func(context.Context, types.NameSpace) (bool, error) {
				return true, nil
			},
			writeTransaction: func(ctx context.Context, work driver.ManagedTransactionWork) error {
				tx := &recordingGraphTransaction{}
				transactions = append(transactions, tx)
				_, err := work(tx)
				return err
			},
		}

		require.NoError(t, repository.RecoverGraphNamespace(context.Background(), namespace))
		require.Len(t, transactions, 3)
		assert.Contains(t, transactions[0].queries[0].cypher, "state.deleting = true")
		assert.Contains(t, transactions[1].queries[0].cypher, "DELETE r")
		assert.Equal(t, true, transactions[2].queries[0].params["succeeded"])
	})
}

func TestGraphRecoveryRoutesStateCheckToWriter(t *testing.T) {
	assert.Equal(t, driver.AccessModeWrite, graphRecoverySessionConfig().AccessMode)
}

func TestGraphOwnershipVersionIgnoresOrderingAndCurrentOwnership(t *testing.T) {
	first := &types.GraphData{
		Node: []*types.GraphNode{
			{Name: "B", Chunks: []string{"chunk-1"}, Attributes: []string{"z", "a"}},
			{Name: "A", Chunks: []string{"chunk-1"}},
		},
		Relation: []*types.GraphRelation{
			{Node1: "B", Node2: "A", Type: "z", Chunks: []string{"chunk-1"}},
			{Node1: "A", Node2: "B", Type: "a", Chunks: []string{"chunk-1"}},
		},
	}
	second := &types.GraphData{
		Node: []*types.GraphNode{
			{Name: "A", Chunks: []string{"chunk-2"}},
			{Name: "B", Chunks: []string{"chunk-2"}, Attributes: []string{"a", "z"}},
		},
		Relation: []*types.GraphRelation{
			{Node1: "A", Node2: "B", Type: "a", Chunks: []string{"chunk-2"}},
			{Node1: "B", Node2: "A", Type: "z", Chunks: []string{"chunk-2"}},
		},
	}

	firstVersion, err := graphOwnershipVersion(first)
	require.NoError(t, err)
	secondVersion, err := graphOwnershipVersion(second)
	require.NoError(t, err)
	assert.Equal(t, firstVersion, secondVersion)
}

func TestGraphOwnershipVersionChangesWithGraphSemantics(t *testing.T) {
	base := &types.GraphData{
		Node: []*types.GraphNode{
			{Name: "A", Attributes: []string{"person"}},
			{Name: "B"},
		},
		Relation: []*types.GraphRelation{{Node1: "A", Node2: "B", Type: "knows"}},
	}
	changed := &types.GraphData{
		Node: []*types.GraphNode{
			{Name: "A", Attributes: []string{"organization"}},
			{Name: "B"},
		},
		Relation: []*types.GraphRelation{{Node1: "A", Node2: "B", Type: "works_with"}},
	}

	baseVersion, err := graphOwnershipVersion(base)
	require.NoError(t, err)
	changedVersion, err := graphOwnershipVersion(changed)
	require.NoError(t, err)
	assert.NotEqual(t, baseVersion, changedVersion)
}

func TestGraphOwnershipVersionRejectsRelationWithUnknownEndpoint(t *testing.T) {
	_, err := graphOwnershipVersion(&types.GraphData{
		Node: []*types.GraphNode{{Name: "A"}},
		Relation: []*types.GraphRelation{{
			Node1: "A",
			Node2: "B",
			Type:  "knows",
		}},
	})

	require.ErrorContains(t, err, "unknown node")
}

func TestDelGraphInvalidatesOwnersBeforeBatchDeletionAndClearsStateAfterward(t *testing.T) {
	var transactions []*recordingGraphTransaction
	repository := &Neo4jRepository{
		nodePrefix:           "ENTITY",
		ownershipSchemaReady: true,
		writeTransaction: func(ctx context.Context, work driver.ManagedTransactionWork) error {
			tx := &recordingGraphTransaction{}
			transactions = append(transactions, tx)
			_, err := work(tx)
			return err
		},
	}

	err := repository.DelGraph(context.Background(), []types.NameSpace{{
		KnowledgeBase: "kb-1",
		Knowledge:     "knowledge-1",
	}})

	require.NoError(t, err)
	require.Len(t, transactions, 3)
	require.Len(t, transactions[0].queries, 1)
	assert.Contains(t, transactions[0].queries[0].cypher, "state.deletion_token = $deletion_token")
	assert.Contains(t, transactions[0].queries[0].cypher, "DELETE ownership")
	assert.Contains(t, transactions[0].queries[0].cypher, "DELETE owner")
	token, ok := transactions[0].queries[0].params["deletion_token"].(string)
	require.True(t, ok)
	require.NotEmpty(t, token)
	require.Len(t, transactions[1].queries, 2)
	assert.Contains(t, transactions[1].queries[0].cypher, "RETURN DISTINCT r")
	assert.Contains(t, transactions[1].queries[0].cypher, "DELETE r")
	assert.Contains(t, transactions[1].queries[0].cypher, "failedOperations")
	assert.Contains(t, transactions[1].queries[0].cypher, "failedBatches")
	assert.Contains(t, transactions[1].queries[0].cypher, "wasTerminated")
	assert.Contains(t, transactions[1].queries[0].cypher, "apoc.util.validate")
	assert.Contains(t, transactions[1].queries[0].cypher, "parallel: false")
	assert.GreaterOrEqual(t,
		strings.Count(transactions[1].queries[0].cypher,
			"coalesce(state.deletion_token, '') <> $deletion_token"),
		2,
	)
	assert.Contains(t, transactions[1].queries[1].cypher, "DETACH DELETE n")
	assert.GreaterOrEqual(t,
		strings.Count(transactions[1].queries[1].cypher,
			"coalesce(state.deletion_token, '') <> $deletion_token"),
		2,
	)
	assert.Equal(t, token, transactions[1].queries[0].params["deletion_token"])
	assert.Equal(t, token, transactions[1].queries[1].params["deletion_token"])
	require.Len(t, transactions[2].queries, 1)
	assert.Contains(t, transactions[2].queries[0].cypher, "GRAPH_OWNERSHIP_STATE")
	assert.Contains(t, transactions[2].queries[0].cypher, "state.dirty")
	assert.Contains(t, transactions[2].queries[0].cypher, "deletion_token: $deletion_token")
	assert.Equal(t, token, transactions[2].queries[0].params["deletion_token"])
	assert.Equal(t, true, transactions[2].queries[0].params["succeeded"])
}

func TestDelGraphKeepsDeletionStateWhenBatchDeletionFails(t *testing.T) {
	want := errors.New("batch delete failed")
	var transactions []*recordingGraphTransaction
	repository := &Neo4jRepository{
		nodePrefix:           "ENTITY",
		ownershipSchemaReady: true,
		writeTransaction: func(ctx context.Context, work driver.ManagedTransactionWork) error {
			writeCalls := len(transactions) + 1
			tx := &recordingGraphTransaction{}
			if writeCalls == 2 {
				tx.errAt = 1
				tx.err = want
			}
			transactions = append(transactions, tx)
			_, err := work(tx)
			return err
		},
	}

	err := repository.DelGraph(context.Background(), []types.NameSpace{{
		KnowledgeBase: "kb-1",
		Knowledge:     "knowledge-1",
	}})

	require.ErrorIs(t, err, want)
	require.Len(t, transactions, 3)
	require.Len(t, transactions[2].queries, 1)
	assert.Equal(t, false, transactions[2].queries[0].params["succeeded"])
	assert.Contains(t, transactions[2].queries[0].cypher, "state.dirty")
	assert.Contains(t, transactions[2].queries[0].cypher, "deletion_token: $deletion_token")
}

func TestDelGraphRetrySupersedesAbandonedDeletionToken(t *testing.T) {
	var beginTokens []string
	repository := &Neo4jRepository{
		nodePrefix:           "ENTITY",
		ownershipSchemaReady: true,
		writeTransaction: func(ctx context.Context, work driver.ManagedTransactionWork) error {
			tx := &recordingGraphTransaction{}
			_, err := work(tx)
			if err == nil && len(tx.queries) == 1 &&
				strings.Contains(tx.queries[0].cypher, "DELETE ownership") {
				beginTokens = append(beginTokens, tx.queries[0].params["deletion_token"].(string))
			}
			return err
		},
	}
	namespace := types.NameSpace{KnowledgeBase: "kb-1", Knowledge: "knowledge-1"}

	require.NoError(t, repository.DelGraph(context.Background(), []types.NameSpace{namespace}))
	require.NoError(t, repository.DelGraph(context.Background(), []types.NameSpace{namespace}))

	require.Len(t, beginTokens, 2)
	assert.NotEqual(t, beginTokens[0], beginTokens[1])
}
