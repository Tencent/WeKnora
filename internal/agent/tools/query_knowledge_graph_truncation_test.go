package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// graphToolOverEveryCap builds a tool whose graph answer overflows both caps:
// graphQueryMaxRelations+12 distinct relations on one hub entity, and
// graphQueryMaxChunks+4 chunks referenced by it.
func graphToolOverEveryCap() *QueryKnowledgeGraphTool {
	relations := make([]*types.GraphRelation, 0, graphQueryMaxRelations+13)
	for i := 0; i < graphQueryMaxRelations+12; i++ {
		relations = append(relations, &types.GraphRelation{
			Node1: "Docker", Node2: fmt.Sprintf("Service%02d", i), Type: "depends_on",
		})
	}
	// A repeat of a relation already in the list must not inflate the total.
	relations = append(relations, &types.GraphRelation{Node1: "Docker", Node2: "Service00", Type: "depends_on"})

	chunks := make(map[string]*types.Chunk, graphQueryMaxChunks+4)
	chunkIDs := make([]string, 0, graphQueryMaxChunks+4)
	for i := 0; i < graphQueryMaxChunks+4; i++ {
		id := fmt.Sprintf("c-%02d", i)
		chunkIDs = append(chunkIDs, id)
		chunks[id] = &types.Chunk{
			ID: id, KnowledgeBaseID: "kb-1", KnowledgeID: "doc", Content: "Docker evidence", IsEnabled: true,
		}
	}
	graphRepo := &stubGraphRepo{graph: &types.GraphData{
		Node:     []*types.GraphNode{{Name: "Docker", Chunks: chunkIDs}},
		Relation: relations,
	}}
	return NewQueryKnowledgeGraphTool(&stubKnowledgeBaseService{
		kb: &types.KnowledgeBase{ID: "kb-1", ExtractConfig: &types.ExtractConfig{
			Enabled: true, Nodes: []*types.GraphNode{{Name: "技术"}},
		}},
	}).WithGraph(graphRepo, &stubGraphChunkRepo{chunks: chunks})
}

// A hub entity has far more relations than the tool returns. The cut has to be
// stated, in the tool output and in Data: read without a marker, the returned
// subgraph is the entity's whole neighbourhood to the model, which then answers
// "X is not related to Y" from a list that never held Y.
func TestQueryKnowledgeGraph_MarksCappedRelationsAndChunks(t *testing.T) {
	tool := graphToolOverEveryCap()

	args, err := json.Marshal(QueryKnowledgeGraphInput{KnowledgeBaseIDs: []string{"kb-1"}, Query: "Docker"})
	require.NoError(t, err)
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	t.Logf("tool output:\n%s", result.Output)

	assert.Contains(t, result.Output, fmt.Sprintf("✓ Found %d of %d relations", graphQueryMaxRelations,
		graphQueryMaxRelations+12), "the headline count must not read as the total")
	assert.Contains(t, result.Output, "=== ⚠️ Truncated ===")
	assert.Contains(t, result.Output, fmt.Sprintf("Relations: %d of %d shown", graphQueryMaxRelations,
		graphQueryMaxRelations+12))
	assert.Contains(t, result.Output, fmt.Sprintf("Graph evidence chunks: %d of %d fetched", graphQueryMaxChunks,
		graphQueryMaxChunks+4))

	relations, ok := result.Data["relations"].([]map[string]interface{})
	require.True(t, ok)
	assert.Len(t, relations, graphQueryMaxRelations)
	assert.Equal(t, graphQueryMaxRelations+12, result.Data["relations_total"])
	assert.Equal(t, 12, result.Data["relations_omitted"])
	assert.Equal(t, graphQueryMaxChunks+4, result.Data["graph_chunks_total"])
	assert.Equal(t, 4, result.Data["graph_chunks_omitted"])
}

// A long query names more entity candidates than the tool looks up. The words
// that were left out have to be stated: an entity named by one of them is never
// matched, and "no relevant graph information" would otherwise read as "this
// entity has no relations".
func TestQueryKnowledgeGraph_MarksCappedQueryTerms(t *testing.T) {
	graphRepo := &stubGraphRepo{graph: &types.GraphData{}}
	tool := NewQueryKnowledgeGraphTool(&stubKnowledgeBaseService{
		kb: &types.KnowledgeBase{ID: "kb-1", ExtractConfig: &types.ExtractConfig{
			Enabled: true, Nodes: []*types.GraphNode{{Name: "技术"}},
		}},
	}).WithGraph(graphRepo, &stubGraphChunkRepo{chunks: map[string]*types.Chunk{}})

	query := "docker kubernetes scheduler container image registry cluster pod node volume secret ingress"
	args, err := json.Marshal(QueryKnowledgeGraphInput{KnowledgeBaseIDs: []string{"kb-1"}, Query: query})
	require.NoError(t, err)
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	t.Logf("tool output:\n%s", result.Output)

	// The 12 words plus the query itself are candidates; the cap keeps 8.
	assert.Equal(t, 13, result.Data["query_terms_total"])
	assert.Equal(t, 5, result.Data["query_terms_omitted"])
	assert.Contains(t, result.Output, "No relevant graph information found.")
	assert.Contains(t, result.Output, "Entity terms: 8 of 13 query words")
}

// Silence has to mean "nothing was dropped": a result under every cap carries
// no truncation marker and no total, so a marker always means a subset.
func TestQueryKnowledgeGraph_NoTruncationMarkerWhenNothingDropped(t *testing.T) {
	graphRepo := &stubGraphRepo{graph: &types.GraphData{
		Node:     []*types.GraphNode{{Name: "Docker", Chunks: []string{"c-1"}}},
		Relation: []*types.GraphRelation{{Node1: "Kubernetes", Node2: "Docker", Type: "orchestrates"}},
	}}
	chunkRepo := &stubGraphChunkRepo{chunks: map[string]*types.Chunk{
		"c-1": {ID: "c-1", KnowledgeBaseID: "kb-1", KnowledgeID: "doc", Content: "Docker", IsEnabled: true},
	}}
	tool := NewQueryKnowledgeGraphTool(&stubKnowledgeBaseService{
		kb: &types.KnowledgeBase{ID: "kb-1", ExtractConfig: &types.ExtractConfig{
			Enabled: true, Nodes: []*types.GraphNode{{Name: "技术"}},
		}},
	}).WithGraph(graphRepo, chunkRepo)

	args, err := json.Marshal(QueryKnowledgeGraphInput{KnowledgeBaseIDs: []string{"kb-1"}, Query: "Docker"})
	require.NoError(t, err)
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	assert.NotContains(t, result.Output, "Truncated")
	assert.NotContains(t, result.Output, " of 1 relations")
	_, hasRelationsTotal := result.Data["relations_total"]
	assert.False(t, hasRelationsTotal, "an uncapped result must not claim a relation total")
	_, hasChunksTotal := result.Data["graph_chunks_total"]
	assert.False(t, hasChunksTotal, "an uncapped result must not claim a chunk total")
	_, hasTermsTotal := result.Data["query_terms_total"]
	assert.False(t, hasTermsTotal, "an uncapped result must not claim a term total")
}
