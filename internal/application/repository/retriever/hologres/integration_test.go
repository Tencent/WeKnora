package hologres

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Run only against a Hologres V5+ TEST database with AGE DDL/DML permissions:
//
//	WEKNORA_HOLOGRES_TEST_DSN='postgresql://...' \
//	  go test ./internal/application/repository/retriever/hologres -run Integration -v
//
// Each run creates its own uniquely named graph, never DefaultGraph.
// Set WEKNORA_HOLOGRES_TEST_KEEP_GRAPH=true to retain inspection fixtures.
// Deletion assertions still run; the basic fixtures are restored afterward.
func liveRepository(t *testing.T) (*Repository, context.Context) {
	t.Helper()
	dsn := os.Getenv("WEKNORA_HOLOGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_HOLOGRES_TEST_DSN to test real Hologres V5+ AGE semantics")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	t.Cleanup(cancel)
	cfg := Config{DSN: dsn, Graph: fmt.Sprintf("weknora_test_%d", time.Now().UnixNano()), AutoCreate: true}
	repo, err := Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		defer func() { assert.NoError(t, repo.Close()) }()
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if os.Getenv("WEKNORA_HOLOGRES_TEST_KEEP_GRAPH") == "true" {
			var nodes, relations int
			require.NoError(t, repo.db.QueryRowContext(cleanupCtx,
				`SELECT count(*) FROM `+repo.entities).Scan(&nodes))
			require.NoError(t, repo.db.QueryRowContext(cleanupCtx,
				`SELECT count(*) FROM `+repo.relations).Scan(&relations))
			t.Logf("retained AGE graph=%s; nodes=%d; relations=%d", cfg.Graph, nodes, relations)
			return
		}
		_, err := repo.db.ExecContext(cleanupCtx, `CALL ag_catalog.hg_age_drop_graph($1, true)`, cfg.Graph)
		assert.NoError(t, err, "drop disposable AGE graph %s", cfg.Graph)
		var remaining int
		checkErr := repo.db.QueryRowContext(cleanupCtx,
			`SELECT count(*) FROM hologres.hg_ag_label WHERE graph = $1`, cfg.Graph).Scan(&remaining)
		assert.NoError(t, checkErr, "test graph cleanup verification")
		assert.Zero(t, remaining, "test graph cleanup verification")
		t.Logf("cleanup verified for %s", cfg.Graph)
	})
	var version string
	require.NoError(t, repo.db.QueryRowContext(ctx, "SELECT version()").Scan(&version))
	t.Logf("server=%s; disposable graph=%s", version, cfg.Graph)
	return repo, ctx
}

func TestHologresIntegration(t *testing.T) {
	repo, ctx := liveRepository(t)
	kb := "kb-`):MATCH (outside)"
	graph := func(chunk string) *types.GraphData {
		return &types.GraphData{Node: []*types.GraphNode{
			{Name: "Acme", Chunks: []string{chunk}, Attributes: []string{"company"}},
			{Name: "Shanghai", Chunks: []string{chunk}},
		}, Relation: []*types.GraphRelation{{Node1: "Acme", Node2: "Shanghai", Type: "LOCATED_IN"}}}
	}
	doc1 := types.NameSpace{KnowledgeBase: kb, Knowledge: "doc1"}
	doc2 := types.NameSpace{KnowledgeBase: kb, Knowledge: "doc2"}
	require.NoError(t, repo.AddGraph(ctx, doc1, []*types.GraphData{graph("c1")}))
	require.NoError(t, repo.AddGraph(ctx, doc2, []*types.GraphData{graph("c2")}))
	require.NoError(t, repo.AddGraph(ctx, doc1, []*types.GraphData{graph("c1")}))

	// Concurrent retries must neither duplicate entities/edges nor lose chunks.
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	for _, chunk := range []string{"c3", "c4"} {
		workers.Add(1)
		go func(chunk string) {
			defer workers.Done()
			errors <- repo.AddGraph(ctx, doc1, []*types.GraphData{graph(chunk)})
		}(chunk)
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	found, err := repo.SearchNode(ctx, types.NameSpace{KnowledgeBase: kb}, []string{"Shanghai"})
	require.NoError(t, err)
	require.Len(t, found.Node, 4)
	require.Len(t, found.Relation, 2)
	for _, relation := range found.Relation {
		require.Equal(t, "Acme", relation.Node1)
		require.Equal(t, "Shanghai", relation.Node2)
	}
	for _, node := range found.Node {
		if node.KnowledgeID == "doc1" {
			require.ElementsMatch(t, []string{"c1", "c3", "c4"}, node.Chunks)
		}
		if node.KnowledgeID == "doc2" {
			require.Equal(t, []string{"c2"}, node.Chunks)
		}
	}

	require.NoError(t, repo.AddGraph(ctx, doc1, []*types.GraphData{
		{Node: []*types.GraphNode{{Name: "Isolated", Chunks: []string{"c1"}}}},
	}))
	public, err := repo.SearchGraph(ctx, types.NameSpace{KnowledgeBase: kb}, "Isolated")
	require.NoError(t, err)
	require.Len(t, public.Graph.Node, 1)
	require.Empty(t, public.Graph.Relation)
	isolated, err := repo.SearchNode(ctx, doc1, []string{"Isolated"})
	require.NoError(t, err)
	require.Empty(t, isolated.Node)
	public, err = repo.SearchGraph(ctx, types.NameSpace{KnowledgeBase: kb}, "' RETURN 1 //")
	require.NoError(t, err)
	require.Empty(t, public.Graph.Node)
	public, err = repo.SearchGraph(ctx, types.NameSpace{KnowledgeBase: "outside"}, "Acme")
	require.NoError(t, err)
	require.Empty(t, public.Graph.Node)

	require.NoError(t, repo.DelGraph(ctx, []types.NameSpace{doc1}))
	found, err = repo.SearchNode(ctx, types.NameSpace{KnowledgeBase: kb}, []string{"Acme"})
	require.NoError(t, err)
	require.Len(t, found.Node, 2)
	require.Len(t, found.Relation, 1)
	for _, node := range found.Node {
		require.Equal(t, "doc2", node.KnowledgeID)
	}
	require.NoError(t, repo.DelGraph(ctx, []types.NameSpace{{KnowledgeBase: kb}}))
	public, err = repo.SearchGraph(ctx, types.NameSpace{KnowledgeBase: kb}, "Acme")
	require.NoError(t, err)
	require.Empty(t, public.Graph.Node)

	if os.Getenv("WEKNORA_HOLOGRES_TEST_KEEP_GRAPH") == "true" {
		// Restore readable samples only after the deletion assertions pass.
		for _, chunk := range []string{"c1", "c3", "c4"} {
			require.NoError(t, repo.AddGraph(ctx, doc1, []*types.GraphData{graph(chunk)}))
		}
		require.NoError(t, repo.AddGraph(ctx, doc2, []*types.GraphData{graph("c2")}))
		require.NoError(t, repo.AddGraph(ctx, doc1, []*types.GraphData{
			{Node: []*types.GraphNode{{Name: "Isolated", Chunks: []string{"c1"}}}},
		}))
	}
}

// Bulk fixtures exercise database-side search/delete limits without thousands
// of single-entity network round trips. Ingestion itself is tested via AddGraph.
func seedLiveNodes(ctx context.Context, t *testing.T, repo *Repository, ns types.NameSpace, count int, prefix string) {
	t.Helper()
	values, args := []string{}, []any{}
	for i := 0; i < count; i++ {
		p := nodeProperties{
			Name: fmt.Sprintf("%s%04d", prefix, i), KB: ns.KnowledgeBase,
			Knowledge: ns.Knowledge, Chunks: []string{"c1"},
		}
		payload, err := p.encode()
		require.NoError(t, err)
		args = append(args, string(payload))
		values = append(values, fmt.Sprintf("(CAST($%d AS ag_catalog.agtype))", len(args)))
	}
	_, err := repo.db.ExecContext(ctx,
		`INSERT INTO `+repo.entities+` (properties) VALUES `+strings.Join(values, ","), args...)
	require.NoError(t, err)
}

func liveCount(ctx context.Context, t *testing.T, repo *Repository, table string, ns types.NameSpace) int {
	t.Helper()
	var count int
	args := []any{ns.KnowledgeBase}
	if ns.Knowledge != "" {
		args = append(args, ns.Knowledge)
	}
	require.NoError(t, repo.db.QueryRowContext(ctx,
		`SELECT count(*) FROM `+table+` WHERE `+scopePredicate("", ns.Knowledge != ""), args...).Scan(&count))
	return count
}

func TestHologresIntegrationMatrix(t *testing.T) {
	repo, ctx := liveRepository(t)
	t.Run("schema_and_pool_sessions", func(t *testing.T) {
		require.NoError(t, repo.EnsureSchema(ctx, false))
		var workers sync.WaitGroup
		errs := make(chan error, 4)
		for i := 0; i < 4; i++ {
			workers.Add(1)
			go func() { defer workers.Done(); errs <- repo.EnsureSchema(ctx, true) }()
		}
		workers.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		// Force several physical sessions; settings must apply to every one.
		for i := 0; i < 4; i++ {
			conn, err := repo.db.Conn(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, conn.Close()) })
			var searchPath, timeout string
			require.NoError(t, conn.QueryRowContext(ctx, "SHOW search_path").Scan(&searchPath))
			require.NoError(t, conn.QueryRowContext(ctx, "SHOW statement_timeout").Scan(&timeout))
			require.Equal(t, "public", searchPath)
			require.NotEqual(t, "0", timeout)
		}
		missing, err := NewRepository(repo.db, repo.graph+"_missing")
		require.NoError(t, err)
		require.ErrorContains(t, missing.EnsureSchema(ctx, false), "missing")
	})

	t.Run("unicode_literals_attributes_identity_and_direction", func(t *testing.T) {
		ns := types.NameSpace{KnowledgeBase: "literal-'%;_知识库", Knowledge: "文档-1"}
		name := "公司'%;_\\東京🌏"
		g := &types.GraphData{Node: []*types.GraphNode{
			{Name: name, Chunks: []string{"c1", "c1"}, Attributes: []string{"first", "für São Paulo"}},
			{Name: "上海", Chunks: []string{"c1"}},
		}, Relation: []*types.GraphRelation{
			{Node1: name, Node2: "上海", Type: "位于"},
			{Node1: "上海", Node2: name, Type: "包含"},
			{Node1: name, Node2: name, Type: "SELF"},
			{Node1: name, Node2: "上海", Type: "SECOND_TYPE"},
		}}
		require.NoError(t, repo.AddGraph(ctx, ns, []*types.GraphData{g}))
		first, err := repo.SearchNode(ctx, ns, []string{"上海", name, name})
		require.NoError(t, err)
		require.Len(t, first.Node, 2)
		require.Len(t, first.Relation, 4)
		ids := map[string]string{}
		for _, n := range first.Node {
			ids[n.Name] = n.ID
		}
		g.Node[0].Chunks = []string{"c2", "c1", "c3"}
		g.Node[0].Attributes = []string{"must-not-overwrite"}
		require.NoError(t, repo.AddGraph(ctx, ns, []*types.GraphData{g}))
		next, err := repo.SearchNode(ctx, ns, []string{name})
		require.NoError(t, err)
		require.Len(t, next.Relation, 4)
		for _, n := range next.Node {
			require.Equal(t, ids[n.Name], n.ID)
			if n.Name == name {
				require.Equal(t, []string{"c1", "c2", "c3"}, n.Chunks)
				require.Equal(t, []string{"first", "für São Paulo"}, n.Attributes)
			}
		}
		for _, e := range next.Relation {
			require.Equal(t, ids[e.Node1], e.SourceID)
			require.Equal(t, ids[e.Node2], e.TargetID)
		}
		for _, query := range []string{"%", "_", "'", "\\", "東京", "🌏", "'; DROP SCHEMA public; --"} {
			out, err := repo.SearchGraph(ctx, types.NameSpace{KnowledgeBase: ns.KnowledgeBase}, query)
			require.NoError(t, err)
			if strings.HasPrefix(query, "'; DROP") {
				require.Empty(t, out.Graph.Node)
			} else {
				require.Len(t, out.Graph.Node, 2)
			}
		}
		other := types.NameSpace{KnowledgeBase: "different-kb", Knowledge: ns.Knowledge}
		require.NoError(t, repo.AddGraph(ctx, other, []*types.GraphData{g}))
		out, err := repo.SearchNode(ctx, other, []string{name})
		require.NoError(t, err)
		require.Len(t, out.Node, 2)
		for _, n := range out.Node {
			require.NotEqual(t, ids[n.Name], n.ID)
		}
	})

	t.Run("exact_seed_order_and_case_sensitivity", func(t *testing.T) {
		kb := "ranking"
		for _, doc := range []string{"z", "a"} {
			g := &types.GraphData{}
			for _, name := range []string{"xRank", "Ranker", "Rank"} {
				g.Node = append(g.Node, &types.GraphNode{Name: name})
			}
			require.NoError(t, repo.AddGraph(ctx,
				types.NameSpace{KnowledgeBase: kb, Knowledge: doc}, []*types.GraphData{g}))
		}
		out, err := repo.SearchGraph(ctx, types.NameSpace{KnowledgeBase: kb}, "Rank")
		require.NoError(t, err)
		got := []string{}
		for _, n := range out.Graph.Node {
			got = append(got, n.Name+":"+n.KnowledgeID)
		}
		require.Equal(t, []string{"Rank:a", "Rank:z", "xRank:a", "xRank:z", "Ranker:a", "Ranker:z"}, got)
		out, err = repo.SearchGraph(ctx, types.NameSpace{KnowledgeBase: kb}, "rank")
		require.NoError(t, err)
		require.Empty(t, out.Graph.Node)
	})

	t.Run("seed_caps_and_edge_dedup_before_budget", func(t *testing.T) {
		ns := types.NameSpace{KnowledgeBase: "seed-cap", Knowledge: "doc"}
		seedLiveNodes(ctx, t, repo, ns, chatMaxSeeds+5, "Seed")
		props, _ := json.Marshal(map[string]string{"kb": ns.KnowledgeBase, "kg": ns.Knowledge, "rel_type": "SELF"})
		_, err := repo.db.ExecContext(ctx, `INSERT INTO `+repo.relations+` (start_id,end_id,properties)
			SELECT id,id,CAST($2 AS ag_catalog.agtype) FROM `+repo.entities+`
			WHERE ag_catalog.agtype_object_field_text(properties,'kb')=$1`, ns.KnowledgeBase, string(props))
		require.NoError(t, err)
		out, err := repo.SearchGraph(ctx, types.NameSpace{KnowledgeBase: ns.KnowledgeBase}, "Seed")
		require.NoError(t, err)
		require.Len(t, out.Graph.Node, types.GraphSearchMaxSeeds)
		require.Len(t, out.Graph.Relation, types.GraphSearchMaxSeeds)
		require.Contains(t, out.TruncationReasons, "candidate_seeds")
		chat, err := repo.SearchNode(ctx, ns, []string{"Seed", "Seed"})
		require.NoError(t, err)
		require.Len(t, chat.Node, chatMaxSeeds)
		require.Len(t, chat.Relation, chatMaxSeeds)
	})

	t.Run("relationship_row_caps", func(t *testing.T) {
		ns := types.NameSpace{KnowledgeBase: "row-cap", Knowledge: "doc"}
		g := &types.GraphData{Node: []*types.GraphNode{{Name: "Hub"}, {Name: "Peer"}}}
		require.NoError(t, repo.AddGraph(ctx, ns, []*types.GraphData{g}))
		values, args := []string{}, []any{ns.KnowledgeBase}
		for i := 0; i < chatMaxRows+1; i++ {
			props, _ := json.Marshal(map[string]string{
				"kb": ns.KnowledgeBase, "kg": ns.Knowledge, "rel_type": fmt.Sprintf("TYPE_%04d", i),
			})
			args = append(args, string(props))
			values = append(values, fmt.Sprintf("(CAST($%d AS ag_catalog.agtype))", len(args)))
		}
		_, err := repo.db.ExecContext(ctx, `INSERT INTO `+repo.relations+` (start_id,end_id,properties)
			SELECT s.id,t.id,v.p FROM `+repo.entities+` s, `+repo.entities+` t,
				(VALUES `+strings.Join(values, ",")+`) AS v(p)
			WHERE ag_catalog.agtype_object_field_text(s.properties,'kb')=$1
				AND ag_catalog.agtype_object_field_text(t.properties,'kb')=$1
			AND ag_catalog.agtype_object_field_text(s.properties,'name')='Hub'
				AND ag_catalog.agtype_object_field_text(t.properties,'name')='Peer'`, args...)
		require.NoError(t, err)
		require.Equal(t, chatMaxRows+1, liveCount(ctx, t, repo, repo.relations, ns))
		out, err := repo.SearchGraph(ctx, types.NameSpace{KnowledgeBase: ns.KnowledgeBase}, "Hub")
		require.NoError(t, err)
		require.Len(t, out.Graph.Relation, types.GraphSearchMaxRows)
		require.Contains(t, out.TruncationReasons, "candidate_relations")
		// Matching BOTH endpoints must not charge each edge twice.
		chat, err := repo.SearchNode(ctx, ns, []string{"Hub", "Peer"})
		require.NoError(t, err)
		require.Len(t, chat.Relation, chatMaxRows)
		require.Len(t, chat.Node, 2)
	})

	t.Run("bounded_public_projection_full_chat_provenance", func(t *testing.T) {
		ns := types.NameSpace{KnowledgeBase: "projection", Knowledge: "doc"}
		name := "Long" + strings.Repeat("中", 600)
		n := &types.GraphNode{Name: name}
		for i := 0; i < 600; i++ {
			n.Chunks = append(n.Chunks, fmt.Sprintf("chunk-%04d", i))
		}
		for i := 0; i < 30; i++ {
			n.Attributes = append(n.Attributes, strings.Repeat("🌏", 1200))
		}
		g := &types.GraphData{
			Node:     []*types.GraphNode{n},
			Relation: []*types.GraphRelation{{Node1: name, Node2: name, Type: strings.Repeat("型", 200)}},
		}
		require.NoError(t, repo.AddGraph(ctx, ns, []*types.GraphData{g}))
		out, err := repo.SearchGraph(ctx, types.NameSpace{KnowledgeBase: ns.KnowledgeBase}, "Long")
		require.NoError(t, err)
		require.Len(t, out.Graph.Node, 1)
		require.Len(t, []rune(out.Graph.Node[0].Name), types.GraphSearchMaxNameRunes+1)
		require.Len(t, out.Graph.Node[0].Chunks, types.GraphSearchMaxEvidence+1)
		require.Len(t, out.Graph.Node[0].Attributes, types.GraphSearchMaxAttributes+1)
		require.Len(t, []rune(out.Graph.Node[0].Attributes[0]), types.GraphSearchMaxAttributeRunes+1)
		require.Len(t, []rune(out.Graph.Relation[0].Type), 129)
		chat, err := repo.SearchNode(ctx, ns, []string{"Long"})
		require.NoError(t, err)
		require.Equal(t, name, chat.Node[0].Name)
		require.Len(t, chat.Node[0].Chunks, 600)
		require.Len(t, chat.Node[0].Attributes, 30)
		require.Len(t, []rune(chat.Relation[0].Type), 200)
	})

	t.Run("concurrent_new_entity_and_chunk_updates", func(t *testing.T) {
		ns := types.NameSpace{KnowledgeBase: "concurrent", Knowledge: "doc"}
		const count = 8
		errs := make(chan error, count)
		start := make(chan struct{})
		var workers sync.WaitGroup
		for i := 0; i < count; i++ {
			workers.Add(1)
			go func(i int) {
				defer workers.Done()
				<-start
				g := &types.GraphData{
					Node:     []*types.GraphNode{{Name: "Concurrent", Chunks: []string{fmt.Sprint(i)}}},
					Relation: []*types.GraphRelation{{Node1: "Concurrent", Node2: "Concurrent", Type: "SELF"}},
				}
				errs <- repo.AddGraph(ctx, ns, []*types.GraphData{g})
			}(i)
		}
		close(start)
		workers.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		out, err := repo.SearchNode(ctx, ns, []string{"Concurrent"})
		require.NoError(t, err)
		require.Len(t, out.Node, 1)
		require.Len(t, out.Relation, 1)
		want := []string{}
		for i := 0; i < count; i++ {
			want = append(want, fmt.Sprint(i))
		}
		require.ElementsMatch(t, want, out.Node[0].Chunks)
		require.Equal(t, 1, liveCount(ctx, t, repo, repo.entities, ns))
		require.Equal(t, 1, liveCount(ctx, t, repo, repo.relations, ns))
	})

	t.Run("server_error_rolls_back_entities_and_updates", func(t *testing.T) {
		ns := types.NameSpace{KnowledgeBase: "rollback", Knowledge: "doc"}
		baseline := &types.GraphData{Node: []*types.GraphNode{{Name: "Existing", Chunks: []string{"original"}}}}
		require.NoError(t, repo.AddGraph(ctx, ns, []*types.GraphData{baseline}))
		broken := *repo
		broken.relations = `"` + repo.graph + `"."missing_edge_label"`
		g := &types.GraphData{
			Node:     []*types.GraphNode{{Name: "Existing", Chunks: []string{"must-rollback"}}, {Name: "New"}},
			Relation: []*types.GraphRelation{{Node1: "Existing", Node2: "New", Type: "FAIL"}},
		}
		require.Error(t, broken.AddGraph(ctx, ns, []*types.GraphData{g}))
		require.Equal(t, 1, liveCount(ctx, t, repo, repo.entities, ns))
		out, err := repo.SearchGraph(ctx, types.NameSpace{KnowledgeBase: ns.KnowledgeBase}, "Existing")
		require.NoError(t, err)
		require.Equal(t, []string{"original"}, out.Graph.Node[0].Chunks)
		require.Error(t, repo.AddGraph(ctx, ns, []*types.GraphData{g, nil}))
		require.Equal(t, 1, liveCount(ctx, t, repo, repo.entities, ns))
	})

	t.Run("cancellation_pool_recovery_and_invalid_scope", func(t *testing.T) {
		ns := types.NameSpace{KnowledgeBase: "cancel", Knowledge: "doc"}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		out, err := repo.SearchNode(cancelled, ns, []string{"Acme"})
		require.Error(t, err)
		require.Nil(t, out)
		require.Error(t, repo.AddGraph(cancelled, ns, []*types.GraphData{
			{Node: []*types.GraphNode{{Name: "Cancelled"}}},
		}))
		require.Error(t, repo.DelGraph(cancelled, []types.NameSpace{ns}))
		require.NoError(t, repo.db.PingContext(ctx))
		require.Equal(t, 0, liveCount(ctx, t, repo, repo.entities, ns))
		_, err = repo.SearchGraph(ctx, ns, "Acme")
		require.Error(t, err)
		_, err = repo.SearchNode(ctx, types.NameSpace{}, []string{"Acme"})
		require.Error(t, err)
		_, err = repo.SearchGraph(ctx, types.NameSpace{KnowledgeBase: "cancel"}, "\x00")
		require.Error(t, err)
		require.Error(t, repo.AddGraph(ctx, ns, []*types.GraphData{{Node: []*types.GraphNode{{Name: "bad\x00name"}}}}))
	})

	t.Run("batched_deletion_concurrency_and_other_kb_survival", func(t *testing.T) {
		ns := types.NameSpace{KnowledgeBase: "delete-many", Knowledge: "doc"}
		other := types.NameSpace{KnowledgeBase: "delete-other", Knowledge: "doc"}
		seedLiveNodes(ctx, t, repo, ns, deleteBatchSize+5, "Delete")
		seedLiveNodes(ctx, t, repo, other, 1, "Keep")
		props, _ := json.Marshal(map[string]string{"kb": ns.KnowledgeBase, "kg": ns.Knowledge, "rel_type": "SELF"})
		_, err := repo.db.ExecContext(ctx, `INSERT INTO `+repo.relations+` (start_id,end_id,properties)
			SELECT id,id,CAST($2 AS ag_catalog.agtype) FROM `+repo.entities+`
			WHERE ag_catalog.agtype_object_field_text(properties,'kb')=$1`, ns.KnowledgeBase, string(props))
		require.NoError(t, err)
		errs := make(chan error, 2)
		var workers sync.WaitGroup
		for i := 0; i < 2; i++ {
			workers.Add(1)
			go func() { defer workers.Done(); errs <- repo.DelGraph(ctx, []types.NameSpace{ns, ns}) }()
		}
		workers.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		require.Equal(t, 0, liveCount(ctx, t, repo, repo.entities, ns))
		require.Equal(t, 0, liveCount(ctx, t, repo, repo.relations, ns))
		require.Equal(t, 1, liveCount(ctx, t, repo, repo.entities, other))
		require.NoError(t, repo.DelGraph(ctx, []types.NameSpace{{KnowledgeBase: other.KnowledgeBase}}))
		require.Equal(t, 0, liveCount(ctx, t, repo, repo.entities, other))
	})
}
