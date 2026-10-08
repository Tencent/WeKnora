package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type budgetGraphRepo struct {
	interfaces.RetrieveGraphRepository
	graphs map[string]*types.GraphData
}

func (s *budgetGraphRepo) SearchNode(
	_ context.Context, namespace types.NameSpace, _ []string,
) (*types.GraphData, error) {
	return s.graphs[namespace.KnowledgeBase], nil
}

type budgetKnowledgeBaseService struct {
	stubKnowledgeBaseService
}

func (*budgetKnowledgeBaseService) GetKnowledgeBaseByIDOnly(
	_ context.Context, id string,
) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{
		ID: id, ExtractConfig: &types.ExtractConfig{Enabled: true, Nodes: []*types.GraphNode{{Name: "Company"}}},
	}, nil
}

func TestQueryKnowledgeGraph_ReportsTotalValidationAcrossKnowledgeBases(t *testing.T) {
	graphs := &budgetGraphRepo{graphs: make(map[string]*types.GraphData)}
	chunks := &graphEvidenceChunkRepo{
		stubGraphChunkRepo: stubGraphChunkRepo{chunks: make(map[string]*types.Chunk)},
	}
	documents := &graphEvidenceKnowledgeService{documents: make(map[string]*types.Knowledge)}
	expected := make(map[string][]string)
	for _, kbID := range []string{"kb-1", "kb-2"} {
		docID := kbID + "-doc"
		documents.documents[docID] = &types.Knowledge{ID: docID, KnowledgeBaseID: kbID}
		var ids []string
		for i := 0; i <= graphQueryMaxEvidenceCandidates; i++ {
			id := fmt.Sprintf("%s-c-%04d", kbID, i)
			ids = append(ids, id)
			chunks.chunks[id] = &types.Chunk{
				ID: id, KnowledgeID: docID, KnowledgeBaseID: kbID, Content: id, IsEnabled: true,
			}
		}
		graph := graphEvidenceGraph(ids, []string{ids[10]})
		graph.Relation[0].Type += kbID
		graphs.graphs[kbID] = graph
		expected[kbID] = ids[:graphQueryMaxEvidenceCandidates]
	}
	tool := NewQueryKnowledgeGraphTool(&budgetKnowledgeBaseService{}).
		WithGraph(graphs, chunks).WithKnowledgeScope(documents)
	result, err := tool.Execute(context.Background(),
		json.RawMessage(`{"knowledge_base_ids":["kb-1","kb-2"],"query":"Acme"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, true, result.Data["graph_validation_truncated"])
	require.Equal(t, graphQueryMaxEvidenceCandidates*2, result.Data["graph_candidates_validated_total"])
	require.Equal(t, graphQueryMaxEvidenceCandidates, result.Data["graph_validation_limit_per_kb"])
	require.Contains(t, result.Output, "1024 candidates were validated in total")
	require.Contains(t, result.Output, "cap 512 per knowledge base")
	require.Len(t, result.Data["relations"], 2)

	requested := make(map[string][]string)
	for _, batch := range chunks.batches {
		require.LessOrEqual(t, len(batch), graphQueryChunkBatchSize)
		for _, id := range batch {
			kbID := chunks.chunks[id].KnowledgeBaseID
			requested[kbID] = append(requested[kbID], id)
		}
	}
	require.Equal(t, expected, requested, "each KB gets its own bounded candidate prefix")
	require.Len(t, chunks.batches, 8)
	require.Len(t, chunks.contentBatches, 2)
	for _, batch := range chunks.contentBatches {
		kbID := chunks.chunks[batch[0]].KnowledgeBaseID
		require.Equal(t, expected[kbID][:graphQueryMaxChunks], batch)
	}
}
