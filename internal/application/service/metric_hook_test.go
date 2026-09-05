package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

const evaluationKnowledgeID = "evaluation-knowledge"

func evaluateRetrievalMetrics(
	t *testing.T,
	qaPair *types.QAPair,
	search, rerank []*types.SearchResult,
) types.RetrievalMetrics {
	t.Helper()

	hook := NewHookMetric(1, evaluationKnowledgeID)
	hook.recordInit(0)
	hook.recordQaPair(0, qaPair)
	hook.recordSearchResult(0, search)
	hook.recordRerankResult(0, rerank)
	hook.recordFinish(0)

	return hook.MetricResult().RetrievalMetrics
}

func TestHookMetricUsesChunkProvenanceForDuplicateContent(t *testing.T) {
	metrics := evaluateRetrievalMetrics(t, &types.QAPair{
		PIDs:     []int{3, 7},
		Passages: []string{"shared passage", "shared passage"},
	}, []*types.SearchResult{
		{ID: "chunk-3", KnowledgeID: evaluationKnowledgeID, ChunkIndex: 3, Content: "shared passage"},
		{ID: "chunk-7", KnowledgeID: evaluationKnowledgeID, ChunkIndex: 7, Content: "shared passage"},
	}, nil)

	if got := metrics.Recall; got != 1 {
		t.Errorf("recall = %v, want 1 for two retrieved passages with distinct IDs", got)
	}
	if got := metrics.Precision; got != 1 {
		t.Errorf("precision = %v, want 1 for two distinct relevant passages", got)
	}
}

func TestHookMetricPreservesUnknownRerankResult(t *testing.T) {
	metrics := evaluateRetrievalMetrics(t, &types.QAPair{
		PIDs:     []int{3},
		Passages: []string{"relevant passage"},
	}, []*types.SearchResult{
		{ID: "search-relevant", KnowledgeID: evaluationKnowledgeID, ChunkIndex: 3},
	}, []*types.SearchResult{
		{ID: "unknown", KnowledgeID: "other-knowledge", ChunkIndex: 3},
		{ID: "rerank-relevant", KnowledgeID: evaluationKnowledgeID, ChunkIndex: 3},
	})

	if got := metrics.Precision; got != 0.5 {
		t.Errorf("precision = %v, want 0.5 when an unknown result precedes the relevant passage", got)
	}
	if got := metrics.MRR; got != 0.5 {
		t.Errorf("MRR = %v, want 0.5 when the relevant passage is ranked second", got)
	}
}

func TestHookMetricCountsDuplicatePassageResultOnceWithoutCompressingRank(t *testing.T) {
	metrics := evaluateRetrievalMetrics(t, &types.QAPair{
		PIDs:     []int{3},
		Passages: []string{"relevant passage"},
	}, []*types.SearchResult{
		{ID: "chunk-3-a", KnowledgeID: evaluationKnowledgeID, ChunkIndex: 3},
		{ID: "chunk-3-b", KnowledgeID: evaluationKnowledgeID, ChunkIndex: 3},
	}, nil)

	if got := metrics.Precision; got != 0.5 {
		t.Errorf("precision = %v, want 0.5 when a duplicate passage result occupies the second rank", got)
	}
	if got := metrics.MRR; got != 1 {
		t.Errorf("MRR = %v, want 1 when the first result is relevant", got)
	}
}
