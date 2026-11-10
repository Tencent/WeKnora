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

func (s *stubGraphChunkRepo) ListChunkEvidenceByIDOnly(_ context.Context, ids []string) ([]*types.Chunk, error) {
	var out []*types.Chunk
	for _, id := range ids {
		if c := s.chunks[id]; c != nil {
			out = append(out, &types.Chunk{
				ID: c.ID, KnowledgeBaseID: c.KnowledgeBaseID, KnowledgeID: c.KnowledgeID, IsEnabled: c.IsEnabled,
			})
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
			{Name: "Docker", ID: "docker-doc", Chunks: []string{"c-docker", "c-foreign", "c-disabled"}},
			{Name: "Kubernetes", ID: "k8s-doc", Chunks: []string{"c-k8s"}},
		},
		Relation: []*types.GraphRelation{{
			Node1: "Kubernetes", Node2: "Docker", Type: "orchestrates", SourceID: "k8s-doc", TargetID: "docker-doc",
		}},
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
	}).WithGraph(graphRepo, chunkRepo).WithKnowledgeScope(liveGraphEvidenceDocuments())

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
			{Name: "Docker", ID: "docker-a", Chunks: []string{"c-a"}},
			{Name: "Kubernetes", ID: "k8s-a", Chunks: []string{"c-a"}},
			{Name: "Secret", ID: "secret-b", Chunks: []string{"c-b"}},
		},
		Relation: []*types.GraphRelation{
			{Node1: "Kubernetes", Node2: "Docker", Type: "orchestrates", SourceID: "k8s-a", TargetID: "docker-a"},
			{Node1: "Secret", Node2: "Docker", Type: "leaks", SourceID: "secret-b", TargetID: "docker-a"},
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
	}}).WithGraph(graphRepo, chunkRepo).WithKnowledgeScope(&graphEvidenceKnowledgeService{
		documents: map[string]*types.Knowledge{
			"doc-a": {ID: "doc-a", Title: "doc-a", KnowledgeBaseID: "kb-1"},
			"doc-b": {ID: "doc-b", Title: "doc-b", KnowledgeBaseID: "kb-1"},
		},
	})

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

type graphScopeKnowledgeService struct {
	scopeKnowledgeService
}

func (*graphScopeKnowledgeService) GetKnowledgeBatchWithSharedAccess(
	_ context.Context, _ uint64, ids []string,
) ([]*types.Knowledge, error) {
	var documents []*types.Knowledge
	for _, id := range ids {
		documents = append(documents, &types.Knowledge{ID: id, Title: id, KnowledgeBaseID: "kb-1"})
	}
	return documents, nil
}

func TestQueryKnowledgeGraph_SameNamedDocumentInstances(t *testing.T) {
	graph := &types.GraphData{
		Node: []*types.GraphNode{
			{Name: "Acme", ID: "acme-a", KnowledgeID: "doc-a", Chunks: []string{"c-a"}},
			{Name: "Shanghai", ID: "city-a", KnowledgeID: "doc-a", Chunks: []string{"c-a"}},
			{Name: "Acme", ID: "acme-b", KnowledgeID: "doc-b", Chunks: []string{"c-b"}},
			{Name: "Shanghai", ID: "city-b", KnowledgeID: "doc-b", Chunks: []string{"c-b"}},
		},
		Relation: []*types.GraphRelation{
			{Node1: "Acme", Node2: "Shanghai", Type: "HEADQUARTERED_IN", SourceID: "acme-a", TargetID: "city-a"},
			{Node1: "Acme", Node2: "Shanghai", Type: "HAS_BRANCH_IN", SourceID: "acme-b", TargetID: "city-b"},
		},
	}
	chunks := &stubGraphChunkRepo{chunks: map[string]*types.Chunk{
		"c-a": {ID: "c-a", KnowledgeID: "doc-a", KnowledgeBaseID: "kb-1", Content: "doc a", IsEnabled: true},
		"c-b": {ID: "c-b", KnowledgeID: "doc-b", KnowledgeBaseID: "kb-1", Content: "doc b", IsEnabled: true},
	}}
	service := &graphScopeKnowledgeService{scopeKnowledgeService: scopeKnowledgeService{
		tags: map[string][]*types.KnowledgeTag{
			"doc-a": {testKnowledgeTag("tag-a")},
			"doc-b": {testKnowledgeTag("tag-b")},
		},
	}}
	for _, tt := range []struct {
		name      string
		target    *types.SearchTarget
		chunks    []string
		relations []string
	}{
		{
			"whole KB", &types.SearchTarget{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1"},
			[]string{"c-a", "c-b"},
			[]string{"HEADQUARTERED_IN", "HAS_BRANCH_IN"},
		},
		{"first document", &types.SearchTarget{
			Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-1",
			KnowledgeIDs: []string{"doc-a"},
		}, []string{"c-a"}, []string{"HEADQUARTERED_IN"}},
		{"second document", &types.SearchTarget{
			Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-1",
			KnowledgeIDs: []string{"doc-b"},
		}, []string{"c-b"}, []string{"HAS_BRANCH_IN"}},
		{"tag scope", &types.SearchTarget{
			Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1",
			TagIDs: []string{"tag-b"},
		}, []string{"c-b"}, []string{"HAS_BRANCH_IN"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewQueryKnowledgeGraphTool(&stubKnowledgeBaseService{
				kb: &types.KnowledgeBase{ID: "kb-1", ExtractConfig: &types.ExtractConfig{
					Enabled: true, Nodes: []*types.GraphNode{{Name: "company"}},
				}},
			}, types.SearchTargets{tt.target}).WithGraph(&stubGraphRepo{graph: graph}, chunks).
				WithKnowledgeScope(service)
			args, err := json.Marshal(QueryKnowledgeGraphInput{KnowledgeBaseIDs: []string{"kb-1"}, Query: "Shanghai"})
			require.NoError(t, err)
			result, err := tool.Execute(context.Background(), args)
			require.NoError(t, err)
			require.True(t, result.Success)
			rows := result.Data["results"].([]map[string]interface{})
			var ids []string
			for _, row := range rows {
				ids = append(ids, row["chunk_id"].(string))
			}
			assert.ElementsMatch(t, tt.chunks, ids)
			var relationTypes []string
			for _, relation := range result.Data["relations"].([]map[string]interface{}) {
				assert.Equal(t, "Acme", relation["source"])
				assert.Equal(t, "Shanghai", relation["target"])
				relationTypes = append(relationTypes, relation["type"].(string))
			}
			assert.ElementsMatch(t, tt.relations, relationTypes,
				"each relation must be supported by its own endpoint instances")
		})
	}
}

func TestRelationsBackedByRejectsMissingOrForeignEndpointIdentities(t *testing.T) {
	graph := &types.GraphData{
		Node: []*types.GraphNode{
			{Name: "Acme", ID: "acme-a", Chunks: []string{"c-a"}},
			{Name: "Shanghai", ID: "city-a", Chunks: []string{"c-a"}},
			{Name: "Acme", ID: "acme-b", Chunks: []string{"c-b"}},
			{Name: "Shanghai", ID: "city-b", Chunks: []string{"c-b"}},
		},
		Relation: []*types.GraphRelation{
			{Node1: "Acme", Node2: "Shanghai", SourceID: "acme-a", TargetID: "city-a"},
			{Node1: "Acme", Node2: "Shanghai", SourceID: "acme-b", TargetID: "city-b"},
			{Node1: "Acme", Node2: "Shanghai", SourceID: "acme-a", TargetID: "city-b"},
			{Node1: "Acme", Node2: "Shanghai"},
			nil,
		},
	}
	got := relationsBackedBy(graph, []*types.SearchResult{nil, {ID: "c-a"}})
	require.Len(t, got, 1)
	assert.Same(t, graph.Relation[0], got[0])
}
