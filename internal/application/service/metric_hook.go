package service

import (
	"context"
	"sync"

	"github.com/Tencent/WeKnora/internal/application/service/metric"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// MetricList stores and aggregates metric results
type MetricList struct {
	results []*types.MetricResult
}

// metricCalculators defines all metrics to be calculated
var metricCalculators = []struct {
	calc     interfaces.Metrics                 // Metric calculator implementation
	getField func(*types.MetricResult) *float64 // Field accessor for result
}{
	// Retrieval Metrics
	{metric.NewPrecisionMetric(), func(r *types.MetricResult) *float64 { return &r.RetrievalMetrics.Precision }},
	{metric.NewRecallMetric(), func(r *types.MetricResult) *float64 { return &r.RetrievalMetrics.Recall }},
	{metric.NewNDCGMetric(3), func(r *types.MetricResult) *float64 { return &r.RetrievalMetrics.NDCG3 }},
	{metric.NewNDCGMetric(10), func(r *types.MetricResult) *float64 { return &r.RetrievalMetrics.NDCG10 }},
	{metric.NewMRRMetric(), func(r *types.MetricResult) *float64 { return &r.RetrievalMetrics.MRR }},
	{metric.NewMAPMetric(), func(r *types.MetricResult) *float64 { return &r.RetrievalMetrics.MAP }},

	// Generation Metrics
	{metric.NewBLEUMetric(true, metric.BLEU1Gram), func(r *types.MetricResult) *float64 {
		return &r.GenerationMetrics.BLEU1
	}},
	{metric.NewBLEUMetric(true, metric.BLEU2Gram), func(r *types.MetricResult) *float64 {
		return &r.GenerationMetrics.BLEU2
	}},
	{metric.NewBLEUMetric(true, metric.BLEU4Gram), func(r *types.MetricResult) *float64 {
		return &r.GenerationMetrics.BLEU4
	}},
	{metric.NewRougeMetric(true, "rouge-1", "f"), func(r *types.MetricResult) *float64 {
		return &r.GenerationMetrics.ROUGE1
	}},
	{metric.NewRougeMetric(true, "rouge-2", "f"), func(r *types.MetricResult) *float64 {
		return &r.GenerationMetrics.ROUGE2
	}},
	{metric.NewRougeMetric(true, "rouge-l", "f"), func(r *types.MetricResult) *float64 {
		return &r.GenerationMetrics.ROUGEL
	}},
}

// Append calculates and stores metrics for given input
func (m *MetricList) Append(metricInput *types.MetricInput) {
	result := &types.MetricResult{}
	// Calculate all configured metrics
	for _, c := range metricCalculators {
		score := c.calc.Compute(metricInput)
		*c.getField(result) = score
	}
	logger.Infof(context.Background(), "metric: %v", result)
	m.results = append(m.results, result)
}

// Avg calculates average of all stored metric results
func (m *MetricList) Avg() *types.MetricResult {
	if len(m.results) == 0 {
		return &types.MetricResult{}
	}

	avgResult := &types.MetricResult{}
	count := float64(len(m.results))

	// Calculate average for each metric
	for _, config := range metricCalculators {
		sum := 0.0
		for _, r := range m.results {
			sum += *config.getField(r)
		}
		*config.getField(avgResult) = sum / count
	}
	return avgResult
}

// HookMetric tracks evaluation metrics for QA pairs
type HookMetric struct {
	qaPairMetricList []*qaPairMetric // Per-QA pair metrics
	metricResults    *MetricList     // Aggregated results
	knowledgeID      string          // Temporary evaluation knowledge source
	mu               *sync.RWMutex   // Thread safety
}

const unknownRetrievalID = -1

// qaPairMetric stores metrics for a single QA pair
type qaPairMetric struct {
	qaPair       *types.QAPair
	searchResult []*types.SearchResult
	rerankResult []*types.SearchResult
	chatResponse *types.ChatResponse
}

// NewHookMetric creates a new HookMetric with given capacity
func NewHookMetric(capacity int, knowledgeID string) *HookMetric {
	return &HookMetric{
		metricResults:    &MetricList{},
		qaPairMetricList: make([]*qaPairMetric, capacity),
		knowledgeID:      knowledgeID,
		mu:               &sync.RWMutex{},
	}
}

// recordInit initializes metric tracking for a QA pair
func (h *HookMetric) recordInit(index int) {
	h.qaPairMetricList[index] = &qaPairMetric{}
}

// recordQaPair records the QA pair data
func (h *HookMetric) recordQaPair(index int, qaPair *types.QAPair) {
	h.qaPairMetricList[index].qaPair = qaPair
}

// recordSearchResult records search results
func (h *HookMetric) recordSearchResult(index int, searchResult []*types.SearchResult) {
	h.qaPairMetricList[index].searchResult = searchResult
}

// recordRerankResult records reranked results
func (h *HookMetric) recordRerankResult(index int, rerankResult []*types.SearchResult) {
	h.qaPairMetricList[index].rerankResult = rerankResult
}

// recordChatResponse records the generated chat response
func (h *HookMetric) recordChatResponse(index int, chatResponse *types.ChatResponse) {
	h.qaPairMetricList[index].chatResponse = chatResponse
}

// recordFinish finalizes metrics for a QA pair
func (h *HookMetric) recordFinish(index int) {
	// Prepare retrieval source: prefer rerank results, fall back to search results
	retrievalSource := h.qaPairMetricList[index].rerankResult
	if len(retrievalSource) == 0 {
		retrievalSource = h.qaPairMetricList[index].searchResult
	}

	// Evaluation ingests a PID-indexed passage slice into one temporary knowledge.
	// Passage processing preserves the slice index as ChunkIndex, so a result from
	// that knowledge has stable PID provenance without guessing from its content.
	// Keep one entry per rank: unknown sources and duplicate PIDs remain explicit
	// misses instead of being removed and compressing the ranking.
	qaPair := h.qaPairMetricList[index].qaPair
	retrievalIDs := make([]int, len(retrievalSource))
	seen := make(map[int]struct{}, len(retrievalSource))
	for i, r := range retrievalSource {
		retrievalIDs[i] = unknownRetrievalID
		if r == nil || r.KnowledgeID != h.knowledgeID || r.ChunkIndex < 0 {
			continue
		}
		pid := r.ChunkIndex
		if _, ok := seen[pid]; ok {
			continue
		}
		seen[pid] = struct{}{}
		retrievalIDs[i] = pid
	}

	// Get generated text if available
	generatedTexts := ""
	if h.qaPairMetricList[index].chatResponse != nil {
		generatedTexts = h.qaPairMetricList[index].chatResponse.Content
	}

	// Prepare metric input data
	metricInput := &types.MetricInput{
		RetrievalGT:    [][]int{qaPair.PIDs},
		RetrievalIDs:   retrievalIDs,
		GeneratedTexts: generatedTexts,
		GeneratedGT:    qaPair.Answer,
	}

	// Thread-safe append of metrics
	h.mu.Lock()
	defer h.mu.Unlock()
	h.metricResults.Append(metricInput)
}

// MetricResult returns the averaged metric results
func (h *HookMetric) MetricResult() *types.MetricResult {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.metricResults.Avg()
}
