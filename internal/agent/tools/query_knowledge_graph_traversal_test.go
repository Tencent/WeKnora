package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubChatModel returns a canned entity-extraction response.
type stubChatModel struct {
	content string
	err     error
	calls   int
}

func (m *stubChatModel) Chat(_ context.Context, _ []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return &types.ChatResponse{Content: m.content}, nil
}

func (m *stubChatModel) ChatStream(_ context.Context, _ []chat.Message, _ *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *stubChatModel) GetModelName() string { return "stub-chat" }

func (m *stubChatModel) GetModelID() string { return "stub-chat-id" }

// graphChunkService fakes the chunk backfill path used by graph traversal.
type graphChunkService struct {
	interfaces.ChunkService
	chunks []*types.Chunk
}

func (s *graphChunkService) GetRepository() interfaces.ChunkRepository {
	return &graphChunkRepo{chunks: s.chunks}
}

type graphChunkRepo struct {
	interfaces.ChunkRepository
	chunks []*types.Chunk
}

func (r *graphChunkRepo) ListChunksByIDOnly(_ context.Context, ids []string) ([]*types.Chunk, error) {
	byID := make(map[string]*types.Chunk, len(r.chunks))
	for _, chunk := range r.chunks {
		byID[chunk.ID] = chunk
	}
	matched := make([]*types.Chunk, 0, len(ids))
	for _, id := range ids {
		if chunk, ok := byID[id]; ok {
			matched = append(matched, chunk)
		}
	}
	return matched, nil
}

func enableGraphBackendForTest(t *testing.T) {
	t.Helper()
	original := graphBackendEnabled
	graphBackendEnabled = func() bool { return true }
	t.Cleanup(func() { graphBackendEnabled = original })
}

func graphTestKB() *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID: "kb-1",
		ExtractConfig: &types.ExtractConfig{
			Enabled: true,
			Nodes:   []*types.GraphNode{{Name: "Technology"}},
			Relations: []*types.GraphRelation{
				{Type: "depends_on"},
			},
		},
	}
}

func TestQueryKnowledgeGraph_TraversesGraphAndMergesResults(t *testing.T) {
	enableGraphBackendForTest(t)

	stub := &stubKnowledgeBaseService{
		kb: graphTestKB(),
		graph: &types.GraphData{
			Node: []*types.GraphNode{
				{Name: "Docker", Chunks: []string{"chunk-graph-1"}},
				{Name: "Kubernetes", Chunks: []string{"chunk-graph-2"}},
			},
			Relation: []*types.GraphRelation{
				{Node1: "Docker", Node2: "Kubernetes", Type: "depends_on"},
			},
		},
		// chunk-graph-1 also appears in hybrid results and must be deduplicated.
		results: []*types.SearchResult{
			{
				ID:             "chunk-graph-1",
				Content:        "duplicate hybrid hit for the graph chunk",
				KnowledgeID:    "doc-1",
				KnowledgeTitle: "Hybrid Doc",
				Score:          0.7,
				MatchType:      types.MatchTypeEmbedding,
			},
			{
				ID:             "chunk-hybrid-only",
				Content:        "hybrid-only recall chunk",
				KnowledgeID:    "doc-2",
				KnowledgeTitle: "Hybrid Doc 2",
				Score:          0.6,
				MatchType:      types.MatchTypeKeywords,
			},
		},
	}
	chunkSvc := &graphChunkService{chunks: []*types.Chunk{
		{ID: "chunk-graph-1", KnowledgeID: "doc-1", Content: "Docker graph chunk", ChunkIndex: 1},
		{ID: "chunk-graph-2", KnowledgeID: "doc-2", Content: "Kubernetes graph chunk", ChunkIndex: 2},
	}}
	chatModel := &stubChatModel{content: `["Docker", "Kubernetes"]`}

	tool := NewQueryKnowledgeGraphTool(stub, chunkSvc, chatModel)
	args, err := json.Marshal(QueryKnowledgeGraphInput{
		KnowledgeBaseIDs: []string{"kb-1"},
		Query:            "What is the relationship between Docker and Kubernetes?",
	})
	require.NoError(t, err)

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	require.True(t, result.Success)

	// Entity extraction ran once and surfaced in the output.
	require.Equal(t, 1, chatModel.calls)
	assert.Contains(t, result.Output, "Extracted Entities: [Docker Kubernetes]")
	assert.Contains(t, result.Output, "Docker --[depends_on]--> Kubernetes")

	// The graph traversal section reports real traversal numbers.
	assert.Contains(t, result.Output, "matched 2 graph nodes, 1 relations, 2 referenced chunks")
	assert.Contains(t, result.Output, "Results combine graph-traversed chunks (graph match) with hybrid retrieval")

	// Deduplicated total: 2 graph chunks + 1 hybrid-only chunk.
	assert.Contains(t, result.Output, "✓ Found 3 relevant results (deduplicated)")

	data, ok := result.Data["graph_traversal"].(map[string]map[string]interface{})
	require.True(t, ok)
	kbStats := data["kb-1"]
	assert.Equal(t, 2, kbStats["nodes_matched"])
	assert.Equal(t, 1, kbStats["relations_matched"])
	assert.Equal(t, 2, kbStats["graph_chunks"])
	assert.Equal(t, []string{"Docker", "Kubernetes"}, kbStats["entities"])

	// Graph chunks rank ahead of the hybrid-only chunk and carry the graph
	// match type.
	formatted := result.Data["results"].([]map[string]interface{})
	require.Len(t, formatted, 3)
	assert.Equal(t, "chunk-graph-1", formatted[0]["chunk_id"])
	assert.Equal(t, "Graph Match", formatted[0]["match_type"])
	assert.Equal(t, "chunk-graph-2", formatted[1]["chunk_id"])
	assert.Equal(t, "chunk-hybrid-only", formatted[2]["chunk_id"])

	// Visualization data now contains real entity nodes and relation edges.
	graphData := result.Data["graph_data"].(map[string]interface{})
	assert.Equal(t, 1, graphData["total_edges"])
}

func TestQueryKnowledgeGraph_GraphFailureFallsBackToRetrieval(t *testing.T) {
	enableGraphBackendForTest(t)

	stub := &stubKnowledgeBaseService{
		kb:       graphTestKB(),
		graphErr: errors.New("neo4j connection refused"),
		results: []*types.SearchResult{
			{
				ID: "chunk-hybrid", Content: "hybrid fallback", KnowledgeID: "doc-1",
				KnowledgeTitle: "Doc", Score: 0.8, MatchType: types.MatchTypeEmbedding,
			},
		},
	}
	chatModel := &stubChatModel{content: `["Docker"]`}

	tool := NewQueryKnowledgeGraphTool(stub, &graphChunkService{}, chatModel)
	args, err := json.Marshal(QueryKnowledgeGraphInput{
		KnowledgeBaseIDs: []string{"kb-1"},
		Query:            "Docker",
	})
	require.NoError(t, err)

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	require.True(t, result.Success)

	assert.Contains(t, result.Output, "traversal skipped (graph traversal failed: neo4j connection refused)")
	assert.Contains(t, result.Output, "✓ Found 1 relevant results (deduplicated)")
	assert.Contains(t, result.Output, "chunk-hybrid")
}

func TestQueryKnowledgeGraph_NoGraphBackendSkipsTraversal(t *testing.T) {
	// graphBackendEnabled stays at its production default here: NEO4J_ENABLE
	// is unset in the test environment, so the traversal must be skipped even
	// though a chat model and graph data are available.
	stub := &stubKnowledgeBaseService{
		kb: graphTestKB(),
		results: []*types.SearchResult{
			{ID: "chunk-h", Content: "fallback", KnowledgeID: "d", Score: 0.5, MatchType: types.MatchTypeEmbedding},
		},
		graph: &types.GraphData{
			Node: []*types.GraphNode{{Name: "Docker", Chunks: []string{"chunk-graph-1"}}},
		},
	}
	chatModel := &stubChatModel{content: `["Docker"]`}

	tool := NewQueryKnowledgeGraphTool(stub, &graphChunkService{}, chatModel)
	args, err := json.Marshal(QueryKnowledgeGraphInput{
		KnowledgeBaseIDs: []string{"kb-1"},
		Query:            "Docker",
	})
	require.NoError(t, err)

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	require.True(t, result.Success)

	assert.Contains(t, result.Output, "traversal skipped (graph backend disabled")
	data, ok := result.Data["graph_traversal"].(map[string]map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, 0, data["kb-1"]["nodes_matched"])
	assert.NotEmpty(t, data["kb-1"]["skipped"])
}

func TestQueryKnowledgeGraph_EntityExtractionFailureSkipsTraversal(t *testing.T) {
	enableGraphBackendForTest(t)

	stub := &stubKnowledgeBaseService{
		kb: graphTestKB(),
		results: []*types.SearchResult{
			{ID: "chunk-h", Content: "fallback", KnowledgeID: "d", Score: 0.5, MatchType: types.MatchTypeEmbedding},
		},
	}
	chatModel := &stubChatModel{err: errors.New("model unavailable")}

	tool := NewQueryKnowledgeGraphTool(stub, &graphChunkService{}, chatModel)
	args, err := json.Marshal(QueryKnowledgeGraphInput{
		KnowledgeBaseIDs: []string{"kb-1"},
		Query:            "Docker",
	})
	require.NoError(t, err)

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Contains(t, result.Output, "traversal skipped (no entities extracted from query)")
}

func TestParseEntityList(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{"plain json array", `["Docker", "Kubernetes"]`, []string{"Docker", "Kubernetes"}},
		{"fenced array", "```json\n[\"Docker\"]\n```", []string{"Docker"}},
		{"prose prefix", `Here are the entities: ["A", "B"]`, []string{"A", "B"}},
		{"deduplicates", `["A", "A", "B"]`, []string{"A", "B"}},
		{"trims whitespace", `[" A ", ""]`, []string{"A"}},
		{"caps entity count", `["1","2","3","4","5","6","7","8","9","10"]`, []string{"1", "2", "3", "4", "5", "6", "7", "8"}},
		{"empty array", `[]`, nil},
		{"not json", `Docker and Kubernetes`, nil},
		{"empty", ``, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, parseEntityList(tc.content))
		})
	}
}
