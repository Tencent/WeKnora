package postgres

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/types"
)

// The graph adapter is exercised on SQLite: every statement it issues is
// standard SQL (VALUES CTEs, LIKE ... ESCAPE, ON CONFLICT), so the semantics
// that matter — idempotent merges, scoped deletes, bounded expansion and stored
// direction — can be pinned without a PostgreSQL server.
func newTestGraphDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	for _, stmt := range []string{
		`CREATE TABLE graph_nodes (
			id TEXT PRIMARY KEY,
			knowledge_base_id TEXT NOT NULL,
			knowledge_id TEXT NOT NULL,
			name TEXT NOT NULL,
			attributes TEXT NOT NULL DEFAULT '[]',
			UNIQUE (knowledge_base_id, knowledge_id, name)
		)`,
		`CREATE TABLE graph_node_chunks (
			node_id TEXT NOT NULL,
			chunk_id TEXT NOT NULL,
			PRIMARY KEY (node_id, chunk_id)
		)`,
		`CREATE TABLE graph_edges (
			id TEXT PRIMARY KEY,
			knowledge_base_id TEXT NOT NULL,
			knowledge_id TEXT NOT NULL,
			source_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			type TEXT NOT NULL,
			UNIQUE (knowledge_base_id, knowledge_id, source_id, target_id, type)
		)`,
	} {
		require.NoError(t, db.Exec(stmt).Error)
	}
	return db
}

func ns(kb, knowledge string) types.NameSpace {
	return types.NameSpace{KnowledgeBase: kb, Knowledge: knowledge}
}

func graphOf(nodes []*types.GraphNode, relations []*types.GraphRelation) *types.GraphData {
	return &types.GraphData{Node: nodes, Relation: relations}
}

func nodeByName(t *testing.T, graph *types.GraphData, name string) *types.GraphNode {
	t.Helper()
	for _, node := range graph.Node {
		if node.Name == name {
			return node
		}
	}
	t.Fatalf("node %q not in result (%d nodes)", name, len(graph.Node))
	return nil
}

// TestAddGraphMergesChunksAndKeepsFirstAttributes pins the Neo4j merge rules:
// chunks accumulate across extractions, attributes are set only on creation,
// and relation endpoints become nodes even when the node list omits them.
func TestAddGraphMergesChunksAndKeepsFirstAttributes(t *testing.T) {
	repo := NewGraphStore(newTestGraphDB(t))
	ctx := context.Background()
	scope := ns("kb-1", "doc-1")

	require.NoError(t, repo.AddGraph(ctx, scope, []*types.GraphData{graphOf(
		[]*types.GraphNode{
			{Name: "Alpha", Attributes: []string{"first"}, Chunks: []string{"c1"}},
			{Name: "Beta", Chunks: []string{"c3"}},
		},
		[]*types.GraphRelation{{Node1: "Alpha", Node2: "Beta", Type: "related_to"}},
	)}))
	require.NoError(t, repo.AddGraph(ctx, scope, []*types.GraphData{graphOf(
		[]*types.GraphNode{
			{Name: "Alpha", Attributes: []string{"second"}, Chunks: []string{"c2"}},
			{Name: "Gamma", Chunks: []string{"c4"}},
		},
		[]*types.GraphRelation{{Node1: "Alpha", Node2: "Gamma", Type: "related_to"}},
	)}))

	graph, err := repo.SearchNode(ctx, types.NameSpace{KnowledgeBase: "kb-1"}, []string{"Alpha"})
	require.NoError(t, err)

	alpha := nodeByName(t, graph, "Alpha")
	require.ElementsMatch(t, []string{"c1", "c2"}, alpha.Chunks)
	require.Equal(t, []string{"first"}, alpha.Attributes)
	beta := nodeByName(t, graph, "Beta")
	require.Equal(t, []string{"c3"}, beta.Chunks)
	require.Len(t, graph.Relation, 2)

	// Re-adding the same payload must not duplicate chunks or edges.
	require.NoError(t, repo.AddGraph(ctx, scope, []*types.GraphData{graphOf(
		[]*types.GraphNode{{Name: "Alpha", Chunks: []string{"c1"}}},
		[]*types.GraphRelation{{Node1: "Alpha", Node2: "Beta", Type: "related_to"}},
	)}))
	graph, err = repo.SearchNode(ctx, types.NameSpace{KnowledgeBase: "kb-1"}, []string{"Alpha"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"c1", "c2"}, nodeByName(t, graph, "Alpha").Chunks)
	require.Len(t, graph.Relation, 2)
}

// TestSearchNodeHonoursDocumentScope verifies a namespace that names one
// document does not leak the knowledge base's other documents.
func TestSearchNodeHonoursDocumentScope(t *testing.T) {
	repo := NewGraphStore(newTestGraphDB(t))
	ctx := context.Background()
	for _, doc := range []string{"doc-1", "doc-2"} {
		require.NoError(t, repo.AddGraph(ctx, ns("kb-1", doc), []*types.GraphData{graphOf(
			[]*types.GraphNode{{Name: "Alpha " + doc}, {Name: "Beta " + doc}},
			[]*types.GraphRelation{{Node1: "Alpha " + doc, Node2: "Beta " + doc, Type: "related_to"}},
		)}))
	}

	graph, err := repo.SearchNode(ctx, ns("kb-1", "doc-1"), []string{"Alpha"})
	require.NoError(t, err)
	require.Len(t, graph.Node, 2)
	for _, node := range graph.Node {
		require.Equal(t, "doc-1", node.KnowledgeID)
	}
}

// TestSearchNodeKeepsStoredDirection verifies an undirected match does not
// reverse the stored edge.
func TestSearchNodeKeepsStoredDirection(t *testing.T) {
	repo := NewGraphStore(newTestGraphDB(t))
	ctx := context.Background()

	require.NoError(t, repo.AddGraph(ctx, ns("kb-1", "doc-1"), []*types.GraphData{graphOf(
		[]*types.GraphNode{{Name: "Alpha"}, {Name: "Beta"}},
		[]*types.GraphRelation{{Node1: "Alpha", Node2: "Beta", Type: "causes"}},
	)}))

	graph, err := repo.SearchNode(ctx, types.NameSpace{KnowledgeBase: "kb-1"}, []string{"Beta"})
	require.NoError(t, err)
	require.Len(t, graph.Relation, 1)
	alpha := nodeByName(t, graph, "Alpha")
	beta := nodeByName(t, graph, "Beta")
	require.Equal(t, alpha.ID, graph.Relation[0].SourceID)
	require.Equal(t, beta.ID, graph.Relation[0].TargetID)
}

// TestSearchNodeSkipsRelationlessSeeds verifies the agent path only seeds from
// entities that have a neighbourhood, while the public path keeps them.
func TestSearchNodeSkipsRelationlessSeeds(t *testing.T) {
	repo := NewGraphStore(newTestGraphDB(t))
	ctx := context.Background()
	require.NoError(t, repo.AddGraph(ctx, ns("kb-1", "doc-1"), []*types.GraphData{graphOf(
		[]*types.GraphNode{{Name: "Island"}, {Name: "Alpha"}, {Name: "Beta"}},
		[]*types.GraphRelation{{Node1: "Alpha", Node2: "Beta", Type: "related_to"}},
	)}))

	graph, err := repo.SearchNode(ctx, types.NameSpace{KnowledgeBase: "kb-1"}, []string{"Island"})
	require.NoError(t, err)
	require.Empty(t, graph.Node)
	require.Empty(t, graph.Relation)

	candidates, err := repo.SearchGraph(ctx, types.NameSpace{KnowledgeBase: "kb-1"}, "Island")
	require.NoError(t, err)
	require.Len(t, candidates.Graph.Node, 1)
	require.Equal(t, "Island", candidates.Graph.Node[0].Name)
}

// TestSearchNodeRanksExactMatchFirst pins the ordering rule that lets an
// entity whose name is the query survive the seed cap.
func TestSearchNodeRanksExactMatchFirst(t *testing.T) {
	repo := NewGraphStore(newTestGraphDB(t))
	ctx := context.Background()
	require.NoError(t, repo.AddGraph(ctx, ns("kb-1", "doc-1"), []*types.GraphData{graphOf(
		[]*types.GraphNode{
			{Name: "Alpha Beta", Chunks: []string{"c1"}},
			{Name: "Alpha Gamma", Chunks: []string{"c2"}},
			{Name: "Alpha", Chunks: []string{"c3"}},
			{Name: "Hub", Chunks: []string{"c4"}},
		},
		[]*types.GraphRelation{
			{Node1: "Alpha Beta", Node2: "Hub", Type: "related_to"},
			{Node1: "Alpha Gamma", Node2: "Hub", Type: "related_to"},
			{Node1: "Alpha", Node2: "Hub", Type: "related_to"},
		},
	)}))

	graph, err := repo.SearchNode(ctx, types.NameSpace{KnowledgeBase: "kb-1"}, []string{"Alpha"})
	require.NoError(t, err)
	require.NotEmpty(t, graph.Node)
	require.Equal(t, "Alpha", graph.Node[0].Name)
}

// TestDelGraphIsScopedToDocument verifies deleting one document's graph keeps
// its neighbours in the same knowledge base.
func TestDelGraphIsScopedToDocument(t *testing.T) {
	repo := NewGraphStore(newTestGraphDB(t))
	ctx := context.Background()
	for _, doc := range []string{"doc-1", "doc-2"} {
		require.NoError(t, repo.AddGraph(ctx, ns("kb-1", doc), []*types.GraphData{graphOf(
			[]*types.GraphNode{{Name: "Alpha"}, {Name: "Beta"}},
			[]*types.GraphRelation{{Node1: "Alpha", Node2: "Beta", Type: "related_to"}},
		)}))
	}

	require.NoError(t, repo.DelGraph(ctx, []types.NameSpace{ns("kb-1", "doc-1")}))

	graph, err := repo.SearchNode(ctx, types.NameSpace{KnowledgeBase: "kb-1"}, []string{"Alpha"})
	require.NoError(t, err)
	require.Len(t, graph.Node, 2) // only doc-2's pair remains
	for _, node := range graph.Node {
		require.Equal(t, "doc-2", node.KnowledgeID)
	}

	// A namespace without a document clears the whole knowledge base.
	require.NoError(t, repo.DelGraph(ctx, []types.NameSpace{ns("kb-1", "")}))
	graph, err = repo.SearchNode(ctx, types.NameSpace{KnowledgeBase: "kb-1"}, []string{"Alpha"})
	require.NoError(t, err)
	require.Empty(t, graph.Node)
}

// TestSearchGraphReportsSeedTruncation verifies the cap is visible instead of
// silently shrinking the candidate set.
func TestSearchGraphReportsSeedTruncation(t *testing.T) {
	repo := NewGraphStore(newTestGraphDB(t))
	ctx := context.Background()

	graph := &types.GraphData{}
	for i := 0; i < types.GraphSearchMaxSeeds+5; i++ {
		name := fmt.Sprintf("Entity %03d", i)
		graph.Node = append(graph.Node, &types.GraphNode{Name: name, Chunks: []string{"c1"}})
	}
	require.NoError(t, repo.AddGraph(ctx, ns("kb-1", "doc-1"), []*types.GraphData{graph}))

	candidates, err := repo.SearchGraph(ctx, types.NameSpace{KnowledgeBase: "kb-1"}, "Entity")
	require.NoError(t, err)
	require.Len(t, candidates.Graph.Node, types.GraphSearchMaxSeeds)
	require.Contains(t, candidates.TruncationReasons, "candidate_seeds")
}

// TestSearchGraphProjectsOversizedFields verifies the Cypher projection's
// bounds (one extra item so the service can flag clipping) are reproduced.
func TestSearchGraphProjectsOversizedFields(t *testing.T) {
	repo := NewGraphStore(newTestGraphDB(t))
	ctx := context.Background()

	attributes := make([]string, 0, types.GraphSearchMaxAttributes+3)
	for i := 0; i < types.GraphSearchMaxAttributes+3; i++ {
		attributes = append(attributes, strings.Repeat("a", types.GraphSearchMaxAttributeRunes+5))
	}
	chunks := make([]string, 0, types.GraphSearchMaxEvidence+3)
	for i := 0; i < types.GraphSearchMaxEvidence+3; i++ {
		chunks = append(chunks, fmt.Sprintf("chunk-%03d", i))
	}
	name := strings.Repeat("N", types.GraphSearchMaxNameRunes+5)
	require.NoError(t, repo.AddGraph(ctx, ns("kb-1", "doc-1"), []*types.GraphData{graphOf(
		[]*types.GraphNode{{Name: name, Attributes: attributes, Chunks: chunks}}, nil,
	)}))

	candidates, err := repo.SearchGraph(ctx, types.NameSpace{KnowledgeBase: "kb-1"}, "N")
	require.NoError(t, err)
	require.Len(t, candidates.Graph.Node, 1)
	node := candidates.Graph.Node[0]
	require.Len(t, []rune(node.Name), types.GraphSearchMaxNameRunes+graphProjectionOvershoot)
	require.Len(t, node.Attributes, types.GraphSearchMaxAttributes+graphProjectionOvershoot)
	require.Len(t, []rune(node.Attributes[0]), types.GraphSearchMaxAttributeRunes+graphProjectionOvershoot)
	require.Len(t, node.Chunks, types.GraphSearchMaxEvidence+graphProjectionOvershoot)
}

// TestSearchGraphTreatsLikeMetacharactersLiterally verifies entity names are
// matched as text, not as LIKE patterns.
func TestSearchGraphTreatsLikeMetacharactersLiterally(t *testing.T) {
	repo := NewGraphStore(newTestGraphDB(t))
	ctx := context.Background()
	require.NoError(t, repo.AddGraph(ctx, ns("kb-1", "doc-1"), []*types.GraphData{graphOf(
		[]*types.GraphNode{
			{Name: "100% done", Chunks: []string{"c1"}},
			{Name: "plain", Chunks: []string{"c2"}},
		}, nil,
	)}))

	candidates, err := repo.SearchGraph(ctx, types.NameSpace{KnowledgeBase: "kb-1"}, "%")
	require.NoError(t, err)
	require.Len(t, candidates.Graph.Node, 1)
	require.Equal(t, "100% done", candidates.Graph.Node[0].Name)
}

// TestAddGraphRequiresDocumentScope protects the deletion path, which always
// scopes by document.
func TestAddGraphRequiresDocumentScope(t *testing.T) {
	repo := NewGraphStore(newTestGraphDB(t))
	err := repo.AddGraph(context.Background(), ns("kb-1", ""), []*types.GraphData{graphOf(
		[]*types.GraphNode{{Name: "Alpha"}}, nil,
	)})
	require.Error(t, err)
}

// TestSearchGraphRejectsMultiDocumentNamespace mirrors the Neo4j adapter's
// guard: the public API queries exactly one knowledge base.
func TestSearchGraphRejectsMultiDocumentNamespace(t *testing.T) {
	repo := NewGraphStore(newTestGraphDB(t))
	_, err := repo.SearchGraph(context.Background(), ns("kb-1", "doc-1"), "Alpha")
	require.Error(t, err)
}
