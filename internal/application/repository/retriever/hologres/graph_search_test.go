package hologres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func queryRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"kind", "seed_index", "node_id", "node_payload", "neighbor_id", "neighbor_payload", "edge_id", "source_id", "target_id", "relation_type"})
}

func nodePayload(name, doc, chunk string, public bool) string {
	p := nodeProperties{Name: name, KB: "kb", Knowledge: doc, Chunks: []string{chunk}, Attributes: []string{doc + " attribute"}}
	if public {
		p.encode()
		data, _ := json.Marshal(p.Projection)
		return string(data)
	}
	data, _ := json.Marshal(p)
	return string(data)
}

func expectSearch(t *testing.T, repo *Repository, mock sqlmock.Sqlmock, ns types.NameSpace, terms []string, public bool, rows *sqlmock.Rows) {
	t.Helper()
	query, args, err := repo.searchSQL(ns, terms, public)
	require.NoError(t, err)
	expected := mock.ExpectQuery(regexp.QuoteMeta(query))
	if len(args) == 2 {
		expected.WithArgs(args[0], args[1])
	}
	if len(args) == 3 {
		expected.WithArgs(args[0], args[1], args[2])
	}
	expected.WillReturnRows(rows)
}

func TestSearchNodeDirectionSameNamedDocumentsAndSelfLoop(t *testing.T) {
	repo, mock := testRepository(t)
	ns := types.NameSpace{KnowledgeBase: "kb"}
	a1 := nodePayload("Acme", "doc1", "c1", false)
	b1 := nodePayload("Shanghai", "doc1", "c1", false)
	a2 := nodePayload("Acme", "doc2", "c2", false)
	b2 := nodePayload("Shanghai", "doc2", "c2", false)
	rows := queryRows().
		AddRow(1, 1, "b1", b1, "a1", a1, "r1", "a1", "b1", "LOCATED_IN").
		AddRow(1, 2, "b2", b2, "a2", a2, "r2", "a2", "b2", "LOCATED_IN").
		AddRow(1, 3, "a1", a1, "b1", b1, "reverse", "b1", "a1", "RELATED_TO").
		AddRow(1, 3, "a1", a1, "a1", a1, "self", "a1", "a1", "SELF")
	expectSearch(t, repo, mock, ns, []string{"Shanghai"}, false, rows)
	graph, err := repo.SearchNode(context.Background(), ns, []string{"Shanghai"})
	require.NoError(t, err)
	require.Len(t, graph.Node, 4)
	require.Len(t, graph.Relation, 4)
	require.Equal(t, "b1", graph.Node[0].ID, "seed priority is independent of edge direction")
	require.Equal(t, "Acme", graph.Relation[0].Node1)
	require.Equal(t, "Shanghai", graph.Relation[0].Node2)
	require.Equal(t, "b1", graph.Relation[2].SourceID)
	require.Equal(t, graph.Relation[3].SourceID, graph.Relation[3].TargetID)
	var sources []string
	for _, node := range graph.Node {
		if node.Name == "Acme" {
			sources = append(sources, node.KnowledgeID+":"+node.Chunks[0])
		}
	}
	require.ElementsMatch(t, []string{"doc1:c1", "doc2:c2"}, sources)
}

func TestPublicIsolatedEntitiesAndCandidateCaps(t *testing.T) {
	repo, mock := testRepository(t)
	ns := types.NameSpace{KnowledgeBase: "kb"}
	isolated := nodePayload("Isolated", "doc", "c1", true)
	expectSearch(t, repo, mock, ns, []string{"Isolated"}, true, queryRows().AddRow(0, 1, "a", isolated, "", "", "", "", "", ""))
	out, err := repo.SearchGraph(context.Background(), ns, "Isolated")
	require.NoError(t, err)
	require.Len(t, out.Graph.Node, 1)
	require.Empty(t, out.Graph.Relation)

	seeds := queryRows()
	for i := 0; i <= types.GraphSearchMaxSeeds; i++ {
		seeds.AddRow(0, i+1, fmt.Sprint(i), nodePayload("Cap", "doc", "c1", true), "", "", "", "", "", "")
	}
	expectSearch(t, repo, mock, ns, []string{"Cap"}, true, seeds)
	out, err = repo.SearchGraph(context.Background(), ns, "Cap")
	require.NoError(t, err)
	require.Len(t, out.Graph.Node, types.GraphSearchMaxSeeds)
	require.Equal(t, []string{"candidate_seeds"}, out.TruncationReasons)

	edges := queryRows()
	p := nodePayload("Hub", "doc", "c1", true)
	for i := 0; i <= types.GraphSearchMaxRows; i++ {
		edges.AddRow(1, 1, "hub", p, "hub", p, fmt.Sprint(i), "hub", "hub", "SELF")
	}
	expectSearch(t, repo, mock, ns, []string{"Hub"}, true, edges)
	out, err = repo.SearchGraph(context.Background(), ns, "Hub")
	require.NoError(t, err)
	require.Len(t, out.Graph.Relation, types.GraphSearchMaxRows)
	require.Equal(t, []string{"candidate_relations"}, out.TruncationReasons)
}

func TestQueryFailureNeverReturnsSuccessfulPartialGraph(t *testing.T) {
	repo, mock := testRepository(t)
	ns := types.NameSpace{KnowledgeBase: "kb"}
	p := nodePayload("Acme", "doc", "c1", true)
	rows := queryRows().AddRow(0, 1, "a", p, "", "", "", "", "", "").AddRow(0, 2, "b", p, "", "", "", "", "", "")
	rows.RowError(1, errors.New("stream interrupted"))
	expectSearch(t, repo, mock, ns, []string{"Acme"}, true, rows)
	out, err := repo.SearchGraph(context.Background(), ns, "Acme")
	require.ErrorContains(t, err, "stream interrupted")
	require.Nil(t, out)

	query, _, err := repo.searchSQL(ns, []string{"Acme"}, true)
	require.NoError(t, err)
	mock.ExpectQuery(regexp.QuoteMeta(query)).WillReturnError(context.DeadlineExceeded)
	out, err = repo.SearchGraph(context.Background(), ns, "Acme")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, out)
}

func TestRejectInvalidScopesAndMalformedData(t *testing.T) {
	repo, _ := testRepository(t)
	_, err := repo.SearchGraph(context.Background(), types.NameSpace{KnowledgeBase: "kb", Knowledge: "doc"}, "Acme")
	require.Error(t, err)
	_, err = repo.SearchNode(context.Background(), types.NameSpace{}, []string{"Acme"})
	require.Error(t, err)
	_, err = repo.SearchGraph(context.Background(), types.NameSpace{KnowledgeBase: "kb"}, " ")
	require.Error(t, err)
	_, err = decodeNode("a", nodePayload("Acme", "doc2", "c2", false), types.NameSpace{KnowledgeBase: "kb", Knowledge: "doc1"}, false)
	require.Error(t, err)
	for _, payload := range []string{"null", `{"name":17}`, `{"name":"Acme","knowledge_id":"doc","chunks":[17]}`} {
		_, err := decodeNode("a", payload, types.NameSpace{KnowledgeBase: "kb"}, true)
		require.Error(t, err)
	}
}
