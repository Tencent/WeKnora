package chatpipeline

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service/metric"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// TestDeterministicRetrievalQualityGate runs a fixed candidate set through the
// production deterministic ordering and TopK stage before computing the same
// retrieval metrics used by evaluation runs. It is intentionally thresholded:
// changes that reverse ranking, truncate too aggressively, or drop relevant
// candidates fail CI instead of merely updating a golden JSON fixture.
func TestDeterministicRetrievalQualityGate(t *testing.T) {
	manager := NewEventManager()
	NewPluginFilterTopK(manager)
	request := &types.ChatManage{PipelineRequest: types.PipelineRequest{RerankTopK: 3}}
	request.SearchResult = []*types.SearchResult{
		{ID: "irrelevant-high", ChunkIndex: 102, KnowledgeID: "kb", Score: 0.99},
		{ID: "relevant-first", ChunkIndex: 101, KnowledgeID: "kb", Score: 0.90},
		{ID: "relevant-second", ChunkIndex: 103, KnowledgeID: "kb", Score: 0.80},
		{ID: "irrelevant-low", ChunkIndex: 104, KnowledgeID: "kb", Score: 0.70},
	}
	require.Nil(t, manager.Trigger(context.Background(), types.FILTER_TOP_K, request))

	retrieved := make([]int, 0, len(request.SearchResult))
	for _, result := range request.SearchResult {
		retrieved = append(retrieved, result.ChunkIndex)
	}
	input := &types.MetricInput{RetrievalGT: [][]int{{101, 103}}, RetrievalIDs: retrieved}
	recall := metric.NewRecallMetric().Compute(input)
	mrr := metric.NewMRRMetric().Compute(input)
	ndcg := metric.NewNDCGMetric(3).Compute(input)

	require.GreaterOrEqual(t, recall, 1.0, "recall regression: got %.4f, minimum 1.0000", recall)
	require.GreaterOrEqual(t, mrr, 0.5, "MRR regression: got %.4f, minimum 0.5000", mrr)
	require.GreaterOrEqual(t, ndcg, 0.69, "NDCG@3 regression: got %.4f, minimum 0.6900", ndcg)
}
