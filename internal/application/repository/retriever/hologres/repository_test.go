package hologres

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRepository(t *testing.T) (*Repository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	repo, err := NewRepository(db, DefaultGraph)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, mock.ExpectationsWereMet())
		mock.ExpectClose()
		assert.NoError(t, db.Close())
	})
	return repo, mock
}

func TestConfigurationAndDisabledBackend(t *testing.T) {
	t.Setenv("HOLOGRES_GRAPH_DSN", "postgresql://user:password@localhost/db?sslmode=require")
	t.Setenv("HOLOGRES_GRAPH_NAME", "")
	t.Setenv("HOLOGRES_GRAPH_AUTO_CREATE", "")
	cfg, err := ConfigFromEnv()
	require.NoError(t, err)
	require.Equal(t, DefaultGraph, cfg.Graph)
	require.False(t, cfg.AutoCreate)
	for _, bad := range []string{"public; DROP SCHEMA public", "a.b", "Uppercase", strings.Repeat("a", 64)} {
		_, err := NewRepository(nil, bad)
		require.Error(t, err)
	}
	t.Setenv("HOLOGRES_GRAPH_AUTO_CREATE", "wrong")
	_, err = ConfigFromEnv()
	require.Error(t, err)
	repo, err := NewRepository(nil, DefaultGraph)
	require.NoError(t, err)
	require.NoError(t, repo.AddGraph(context.Background(), types.NameSpace{}, nil))
	require.NoError(t, repo.DelGraph(context.Background(), nil))
	graph, err := repo.SearchNode(context.Background(), types.NameSpace{}, nil)
	require.NoError(t, err)
	require.Nil(t, graph)
	public, err := repo.SearchGraph(context.Background(), types.NameSpace{}, "Acme")
	require.NoError(t, err)
	require.Nil(t, public)
}

func TestQuerySyntaxScopeBoundsAndInjection(t *testing.T) {
	repo, _ := NewRepository(nil, DefaultGraph)
	for _, public := range []bool{false, true} {
		for _, document := range []string{"", "doc"} {
			ns := types.NameSpace{KnowledgeBase: "kb-`):MATCH (x)", Knowledge: document}
			payload := "' RETURN 1 // %_\\"
			query, args, err := repo.searchSQL(ns, []string{payload, payload, "Acme"}, public)
			require.NoError(t, err)
			_, err = pg_query.Parse(query)
			require.NoError(t, err, query)
			require.NotContains(t, query, payload)
			require.NotContains(t, query, ns.KnowledgeBase)
			require.Contains(t, query, "strpos(")
			require.NotContains(t, query, " LIKE ")
			require.Contains(t, query,
				"JOIN scoped_nodes a ON a.id = e.start_id JOIN scoped_nodes b ON b.id = e.end_id")
			require.Contains(t, query,
				"LEFT JOIN selected_seeds s ON s.id = e.start_id LEFT JOIN selected_seeds t ON t.id = e.end_id")
			require.Contains(t, query, "CASE WHEN")
			require.Contains(t, query, "ag_catalog.agtype_object_field_text(properties, 'kg')")
			require.NotContains(t, query, " ->> ")
			require.NotContains(t, query, ".properties::text")
			require.Contains(t, query, "ag_catalog.agtype_out(")
			require.Equal(t, ns.KnowledgeBase, args[0])
			if document != "" {
				require.Equal(t, document, args[1])
			}
			require.Equal(t, payload, args[len(args)-2])
			if public {
				require.Contains(t, query, "LIMIT 101")
				require.Contains(t, query, "LIMIT 513")
				require.Contains(t, query, "FROM seeds\n\t\t\tUNION ALL")
				require.Contains(t, query, "ag_catalog.agtype_object_field(")
				require.Contains(t, query, "'_search'")
			} else {
				require.Contains(t, query, "LIMIT 200")
				require.Contains(t, query, "LIMIT 2000")
				require.Contains(t, query, "AND EXISTS")
			}
		}
	}
}

func TestPrepareGraphAndBoundedProjection(t *testing.T) {
	ns := types.NameSpace{KnowledgeBase: "kb", Knowledge: "doc"}
	graph := &types.GraphData{Node: []*types.GraphNode{
		{Name: "Acme", Chunks: []string{"c1", "c1"}, Attributes: []string{"first"}},
		{Name: "Acme", Chunks: []string{"c2"}, Attributes: []string{"ignored on match"}},
	}, Relation: []*types.GraphRelation{{Node1: "Acme", Node2: "Shanghai", Type: "LOCATED_IN"}}}
	nodes, err := prepareGraph(ns, graph)
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	require.Equal(t, []string{"c1", "c2"}, nodes[0].Chunks)
	require.Equal(t, []string{"first"}, nodes[0].Attributes)
	require.Equal(t, "Shanghai", nodes[1].Name)
	require.Empty(t, nodes[1].Chunks)
	p := &nodeProperties{Name: strings.Repeat("中", 600), KB: "kb", Knowledge: "doc"}
	for i := 0; i < 600; i++ {
		p.Chunks = append(p.Chunks, fmt.Sprint(i))
	}
	for i := 0; i < 30; i++ {
		p.Attributes = append(p.Attributes, strings.Repeat("中", 3000))
	}
	_, err = p.encode()
	require.NoError(t, err)
	require.Len(t, []rune(p.Projection.Name), 513)
	require.Len(t, p.Projection.Chunks, 513)
	require.Len(t, p.Projection.Attributes, 21)
	require.Len(t, []rune(p.Projection.Attributes[0]), 1001)
	require.Len(t, p.Chunks, 600, "full provenance must not be discarded")
}

func beginWriteExpectation(mock sqlmock.Sqlmock) {
	mock.ExpectExec("SET hg_experimental_enable_transaction = on").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
}

type propertiesArgument struct {
	name, kb, doc string
	chunks, attrs []string
}

func (a propertiesArgument) Match(value driver.Value) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	var p nodeProperties
	if json.Unmarshal([]byte(text), &p) != nil || p.Projection == nil {
		return false
	}
	return p.Name == a.name && p.KB == a.kb && p.Knowledge == a.doc &&
		fmt.Sprint(p.Chunks) == fmt.Sprint(a.chunks) && fmt.Sprint(p.Attributes) == fmt.Sprint(a.attrs)
}

func TestAddGraphPreservesIdentityChunksAndDirection(t *testing.T) {
	repo, mock := testRepository(t)
	ns := types.NameSpace{KnowledgeBase: "kb", Knowledge: "doc1"}
	old, _ := json.Marshal(&nodeProperties{
		Name: "Acme", KB: "kb", Knowledge: "doc1", Chunks: []string{"c1"}, Attributes: []string{"original"},
	})
	beginWriteExpectation(mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id::text, ag_catalog.agtype_out(properties)::text")).
		WithArgs("kb", "doc1", "Acme", "Shanghai").
		WillReturnRows(sqlmock.NewRows([]string{"id", "properties"}).AddRow("9007199254740993", string(old)))
	mock.ExpectExec("UPDATE .*entity.* SET properties").
		WithArgs(
			propertiesArgument{"Acme", "kb", "doc1", []string{"c1", "c2"}, []string{"original"}}, "9007199254740993",
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO .*entity.*").WithArgs(propertiesArgument{"Shanghai", "kb", "doc1", nil, nil}).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id::text FROM").WithArgs("kb", "doc1", "Shanghai").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("9007199254740994"))
	mock.ExpectExec("INSERT INTO .*relation.*").
		WithArgs("9007199254740993", "9007199254740994", sqlmock.AnyArg(), "LOCATED_IN").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	graph := &types.GraphData{
		Node: []*types.GraphNode{{Name: "Acme", Chunks: []string{"c2", "c1"}, Attributes: []string{"new"}}},
		Relation: []*types.GraphRelation{
			{Node1: "Acme", Node2: "Shanghai", Type: "LOCATED_IN"},
			{Node1: "Acme", Node2: "Shanghai", Type: "LOCATED_IN"},
		},
	}
	require.NoError(t, repo.AddGraph(context.Background(), ns, []*types.GraphData{graph}))
}

func TestWriteFailureRollsBackAndInvalidInputDoesNotWrite(t *testing.T) {
	repo, mock := testRepository(t)
	ns := types.NameSpace{KnowledgeBase: "kb", Knowledge: "doc"}
	beginWriteExpectation(mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id::text, ag_catalog.agtype_out(properties)::text")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "properties"}))
	mock.ExpectExec("INSERT INTO").WillReturnError(errors.New("write failed"))
	mock.ExpectRollback()
	err := repo.AddGraph(context.Background(), ns, []*types.GraphData{{Node: []*types.GraphNode{{Name: "Acme"}}}})
	require.ErrorContains(t, err, "write failed")
	for _, graph := range []*types.GraphData{
		nil,
		{Node: []*types.GraphNode{nil}},
		{Node: []*types.GraphNode{{Name: "bad\x00name"}}},
		{Relation: []*types.GraphRelation{nil}},
	} {
		require.Error(t, repo.AddGraph(context.Background(), ns, []*types.GraphData{graph}))
	}
	require.Error(t, repo.AddGraph(context.Background(), types.NameSpace{KnowledgeBase: "kb"}, nil))
}

func TestDeleteRemovesEdgesBeforeNodesAndStops(t *testing.T) {
	repo, mock := testRepository(t)
	ns := types.NameSpace{KnowledgeBase: "kb", Knowledge: "doc"}
	beginWriteExpectation(mock)
	mock.ExpectQuery("SELECT id::text FROM").WithArgs("kb", "doc").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("a").AddRow("b"))
	mock.ExpectExec("DELETE FROM .*relation.*start_id::text IN").WithArgs("a", "b").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("DELETE FROM .*entity.*").WithArgs("a", "b").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()
	beginWriteExpectation(mock)
	mock.ExpectQuery("SELECT id::text FROM").WithArgs("kb", "doc").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()
	require.NoError(t, repo.DelGraph(context.Background(), []types.NameSpace{ns, ns}))
}

func TestSchemaRequiresExplicitProvisioning(t *testing.T) {
	repo, mock := testRepository(t)
	mock.ExpectQuery("SELECT EXISTS").WithArgs(DefaultGraph).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	require.ErrorContains(t, repo.EnsureSchema(context.Background(), false), "missing")
	mock.ExpectQuery("SELECT EXISTS").WithArgs(DefaultGraph).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec(regexp.QuoteMeta("CALL ag_catalog.hg_age_create_graph($1)")).WithArgs(DefaultGraph).
		WillReturnResult(sqlmock.NewResult(0, 0))
	for _, label := range []struct{ name, kind, proc string }{{"entity", "v", "vlabel"}, {"relation", "e", "elabel"}} {
		mock.ExpectQuery("SELECT EXISTS").WithArgs(DefaultGraph, label.name, label.kind).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectExec("CALL ag_catalog.hg_age_create_"+label.proc).WithArgs(DefaultGraph, label.name).
			WillReturnResult(sqlmock.NewResult(0, 0))
	}
	require.NoError(t, repo.EnsureSchema(context.Background(), true))
}
