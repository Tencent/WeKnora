package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubGraphRepo struct {
	interfaces.RetrieveGraphRepository
	graph *types.GraphData
	terms []string
}

func (s *stubGraphRepo) SearchNode(_ context.Context, _ types.NameSpace, nodes []string) (*types.GraphData, error) {
	s.terms = nodes
	return s.graph, nil
}

type stubGraphChunkRepo struct {
	interfaces.ChunkRepository
	chunks map[string]*types.Chunk
}

func (s *stubGraphChunkRepo) ListChunksByIDOnly(_ context.Context, ids []string) ([]*types.Chunk, error) {
	var out []*types.Chunk
	for _, id := range ids {
		if c := s.chunks[id]; c != nil {
			out = append(out, c)
		}
	}
	return out, nil
}

// The tool used to run plain text search only. It now looks the entities up
// in the graph, returns their relations, and puts the chunks they came from
// first — only chunks of the queried, authorized knowledge base.
func TestQueryKnowledgeGraph_QueriesTheGraph(t *testing.T) {
	graphRepo := &stubGraphRepo{graph: &types.GraphData{
		Node: []*types.GraphNode{
			{Name: "Docker", Chunks: []string{"c-docker", "c-foreign", "c-disabled"}},
			{Name: "Kubernetes", Chunks: []string{"c-k8s"}},
		},
		Relation: []*types.GraphRelation{{Node1: "Kubernetes", Node2: "Docker", Type: "orchestrates"}},
	}}
	chunkRepo := &stubGraphChunkRepo{chunks: map[string]*types.Chunk{
		"c-docker": {
			ID: "c-docker", KnowledgeBaseID: "kb-1", KnowledgeID: "doc",
			Content: "Docker runs containers", IsEnabled: true,
		},
		"c-k8s": {
			ID: "c-k8s", KnowledgeBaseID: "kb-1", KnowledgeID: "doc",
			Content: "Kubernetes schedules pods", IsEnabled: true,
		},
		"c-foreign":  {ID: "c-foreign", KnowledgeBaseID: "kb-other", Content: "foreign", IsEnabled: true},
		"c-disabled": {ID: "c-disabled", KnowledgeBaseID: "kb-1", Content: "disabled", IsEnabled: false},
	}}
	tool := NewQueryKnowledgeGraphTool(&stubKnowledgeBaseService{
		kb: &types.KnowledgeBase{ID: "kb-1", ExtractConfig: &types.ExtractConfig{
			Enabled: true, Nodes: []*types.GraphNode{{Name: "技术"}},
		}},
		results: []*types.SearchResult{{ID: "c-text", KnowledgeID: "doc", Content: "text hit", Score: 0.9}},
	}).WithGraph(graphRepo, chunkRepo)

	args, err := json.Marshal(QueryKnowledgeGraphInput{KnowledgeBaseIDs: []string{"kb-1"}, Query: "Docker Kubernetes"})
	require.NoError(t, err)
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	assert.Equal(t, []string{"Docker Kubernetes", "Kubernetes", "Docker"}, graphRepo.terms,
		"terms keep case: Neo4j CONTAINS is case-sensitive")
	rows, ok := result.Data["results"].([]map[string]interface{})
	require.True(t, ok)
	ids := make([]interface{}, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row["chunk_id"])
	}
	assert.Equal(t, []interface{}{"c-docker", "c-k8s", "c-text"}, ids,
		"graph evidence first, foreign and disabled chunks dropped")
	relations, ok := result.Data["relations"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, relations, 1)
	assert.Equal(t, "orchestrates", relations[0]["type"])
	assert.Contains(t, result.Output, "Kubernetes --[orchestrates]--> Docker")
}

// The graph namespace is the whole knowledge base. Under a document scope,
// relations between entities from out-of-scope documents must not reach the
// model, and a failed text search is reported next to the graph evidence.
func TestQueryKnowledgeGraph_ScopesRelationsToDocuments(t *testing.T) {
	graphRepo := &stubGraphRepo{graph: &types.GraphData{
		Node: []*types.GraphNode{
			{Name: "Docker", Chunks: []string{"c-a"}},
			{Name: "Kubernetes", Chunks: []string{"c-a"}},
			{Name: "Secret", Chunks: []string{"c-b"}},
		},
		Relation: []*types.GraphRelation{
			{Node1: "Kubernetes", Node2: "Docker", Type: "orchestrates"},
			{Node1: "Secret", Node2: "Docker", Type: "leaks"},
		},
	}}
	chunkRepo := &stubGraphChunkRepo{chunks: map[string]*types.Chunk{
		"c-a": {ID: "c-a", KnowledgeBaseID: "kb-1", KnowledgeID: "doc-a", Content: "in scope", IsEnabled: true},
		"c-b": {ID: "c-b", KnowledgeBaseID: "kb-1", KnowledgeID: "doc-b", Content: "out of scope", IsEnabled: true},
	}}
	tool := NewQueryKnowledgeGraphTool(&stubKnowledgeBaseService{
		kb: &types.KnowledgeBase{ID: "kb-1", ExtractConfig: &types.ExtractConfig{
			Enabled: true, Nodes: []*types.GraphNode{{Name: "技术"}},
		}},
		err: assert.AnError,
	}, types.SearchTargets{{
		Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-1", KnowledgeIDs: []string{"doc-a"},
	}}).WithGraph(graphRepo, chunkRepo)

	args, err := json.Marshal(QueryKnowledgeGraphInput{KnowledgeBaseIDs: []string{"kb-1"}, Query: "Docker"})
	require.NoError(t, err)
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	relations, ok := result.Data["relations"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, relations, 1)
	assert.Equal(t, "orchestrates", relations[0]["type"])
	assert.NotContains(t, result.Output, "Secret")
	errs, _ := result.Data["errors"].([]string)
	require.Len(t, errs, 1, "the failed text search is reported beside the graph hits")
	assert.Contains(t, errs[0], "text search failed")
}
