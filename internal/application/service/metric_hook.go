package service

import (
	"context"
	"strconv"
	"sync"

	"github.com/Tencent/WeKnora/internal/evaluation/metricregistry"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// MetricList stores and aggregates metric results
type MetricList struct {
	results  []*types.MetricResult
	resolved *metricregistry.ResolvedPlan
}

func newMetricList(size int, resolved *metricregistry.ResolvedPlan) *MetricList {
	return &MetricList{results: make([]*types.MetricResult, size), resolved: resolved}
}

// AppendAt calculates and stores one sample by stable sample index.
func (m *MetricList) AppendAt(
	ctx context.Context,
	index int,
	metricInput *types.MetricInput,
) ([]types.EvaluationMetricObservationSnapshot, error) {
	result, observations, err := m.resolved.Compute(ctx, metricInput)
	if err != nil {
		return observations, err
	}
	logger.Infof(context.Background(), "metric: %v", result)
	m.results[index] = result
	return observations, nil
}

// Avg calculates average of all stored metric results
func (m *MetricList) Avg() *types.MetricResult {
	return m.resolved.Aggregate(m.results)
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
	metricResult *types.MetricResult
	observations []types.EvaluationMetricObservationSnapshot
}

// NewHookMetric creates a new HookMetric with given capacity
func NewHookMetric(capacity int, knowledgeID string) *HookMetric {
	registry, err := metricregistry.NewDefaultRegistry()
	if err != nil {
		panic(err)
	}
	plan, err := registry.Resolve(metricregistry.DefaultSpecs())
	if err != nil {
		panic(err)
	}
	return newHookMetric(capacity, knowledgeID, plan)
}

func newHookMetric(
	capacity int,
	knowledgeID string,
	resolved *metricregistry.ResolvedPlan,
) *HookMetric {
	return &HookMetric{
		metricResults:    newMetricList(capacity, resolved),
		qaPairMetricList: make([]*qaPairMetric, capacity),
		knowledgeID:      knowledgeID,
		mu:               &sync.RWMutex{},
	}
}

// NewHookMetricWithRegistry binds the task's frozen M3 plan to the registry.
func NewHookMetricWithRegistry(
	capacity int,
	knowledgeID string,
	registry *metricregistry.Registry,
	plan *types.EvaluationMetricPlanSnapshot,
) (*HookMetric, error) {
	if registry == nil {
		var err error
		registry, err = metricregistry.NewDefaultRegistry()
		if err != nil {
			return nil, err
		}
	}
	if plan == nil {
		resolved, err := registry.Resolve(metricregistry.DefaultSpecs())
		if err != nil {
			return nil, err
		}
		return newHookMetric(capacity, knowledgeID, resolved), nil
	}
	resolved, err := registry.ResolveSnapshot(plan)
	if err != nil {
		return nil, err
	}
	return newHookMetric(capacity, knowledgeID, resolved), nil
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
	if err := h.recordFinishWithContext(context.Background(), index); err != nil {
		panic(err)
	}
}

func (h *HookMetric) recordFinishWithContext(ctx context.Context, index int) error {
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
	retrievalIDs := evaluationRetrievalIDsWithProvenance(retrievalSource, h.knowledgeID)

	// Get generated text if available
	generatedTexts := ""
	if h.qaPairMetricList[index].chatResponse != nil {
		generatedTexts = h.qaPairMetricList[index].chatResponse.Content
	}

	// Prepare metric input data
	metricInput := &types.MetricInput{
		RetrievalGT:              [][]int{qaPair.PIDs},
		RetrievalGrades:          qaPair.PIDGrades,
		RetrievalLabelsAvailable: qaPair.RetrievalLabelsAvailable || len(qaPair.PIDs) > 0,
		RetrievalIDs:             retrievalIDs,
		GeneratedTexts:           generatedTexts,
		GeneratedGT:              qaPair.Answer,
	}

	// Thread-safe append of metrics
	h.mu.Lock()
	defer h.mu.Unlock()
	observations, err := h.metricResults.AppendAt(ctx, index, metricInput)
	if err != nil {
		return err
	}
	h.qaPairMetricList[index].metricResult = h.metricResults.results[index]
	h.qaPairMetricList[index].observations = observations
	return nil
}

// MetricResult returns the averaged metric results
func (h *HookMetric) MetricResult() *types.MetricResult {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.metricResults.Avg()
}

// evaluationRetrievalIDsWithProvenance maps one ranked result list to the
// per-rank PID list with the M0 provenance contract: a result counts only
// when it belongs to this evaluation's temporary knowledge and its
// ChunkIndex (the passage slice index, i.e. PID) is valid and unseen;
// unknown sources and duplicates keep their rank as -1.
func evaluationRetrievalIDsWithProvenance(results []*types.SearchResult, knowledgeID string) []int {
	retrievalIDs := make([]int, len(results))
	seen := make(map[int]struct{}, len(results))
	for i, r := range results {
		retrievalIDs[i] = unknownRetrievalID
		if r == nil || r.KnowledgeID != knowledgeID || r.ChunkIndex < 0 {
			continue
		}
		pid := r.ChunkIndex
		if _, ok := seen[pid]; ok {
			continue
		}
		seen[pid] = struct{}{}
		retrievalIDs[i] = pid
	}
	return retrievalIDs
}

// evaluationRankedResultsWithProvenance renders one ranked list for
// per-question persistence: every position keeps rank, score, PID (or -1),
// and its provenance state.
func evaluationRankedResultsWithProvenance(
	results []*types.SearchResult,
	knowledgeID string,
) []types.EvaluationRankedResult {
	ranked := make([]types.EvaluationRankedResult, len(results))
	seen := make(map[int]struct{}, len(results))
	for i, r := range results {
		ranked[i] = types.EvaluationRankedResult{
			Rank:       i + 1,
			PID:        unknownRetrievalID,
			Provenance: types.EvaluationRankProvenanceUnknown,
		}
		if r == nil {
			continue
		}
		ranked[i].Score = r.Score
		if r.KnowledgeID != knowledgeID || r.ChunkIndex < 0 {
			continue
		}
		pid := r.ChunkIndex
		if _, duplicate := seen[pid]; duplicate {
			ranked[i].Provenance = types.EvaluationRankProvenanceDuplicate
			continue
		}
		seen[pid] = struct{}{}
		ranked[i].PID = pid
		ranked[i].Provenance = types.EvaluationRankProvenanceKnown
	}
	return ranked
}

// evaluationGenerationPIDOrder lists the PIDs that actually entered the
// generation stage: known-provenance entries of the effective retrieval
// order (rerank preferred), truncated to the rerank top-k window.
func evaluationGenerationPIDOrder(ranked []types.EvaluationRankedResult, rerankTopK int) []int {
	pids := make([]int, 0, len(ranked))
	for _, entry := range ranked {
		if entry.Provenance != types.EvaluationRankProvenanceKnown {
			continue
		}
		pids = append(pids, entry.PID)
		if rerankTopK > 0 && len(pids) >= rerankTopK {
			break
		}
	}
	return pids
}

// questionResultInput assembles the per-question publication input for one
// completed sample. Callers hold publishMu, so the per-sample metric is the
// entry appended by the immediately preceding recordFinish.
func (h *HookMetric) questionResultInput(
	index int,
	_ *types.EvaluationMetricPlanSnapshot,
	rerankTopK int,
) *types.EvaluationQuestionResultInput {
	tracked := h.qaPairMetricList[index]
	if tracked == nil || tracked.qaPair == nil {
		return nil
	}
	qaPair := tracked.qaPair

	retrievalSource := tracked.rerankResult
	if len(retrievalSource) == 0 {
		retrievalSource = tracked.searchResult
	}
	effectiveRanked := evaluationRankedResultsWithProvenance(retrievalSource, h.knowledgeID)

	var generatedText string
	var promptTokens, completionTokens, totalTokens *int
	if tracked.chatResponse != nil {
		generatedText = tracked.chatResponse.Content
		prompt := tracked.chatResponse.Usage.PromptTokens
		completion := tracked.chatResponse.Usage.CompletionTokens
		total := tracked.chatResponse.Usage.TotalTokens
		promptTokens = &prompt
		completionTokens = &completion
		totalTokens = &total
	}

	h.mu.RLock()
	perSample := tracked.metricResult
	observations := append([]types.EvaluationMetricObservationSnapshot(nil), tracked.observations...)
	h.mu.RUnlock()

	qid := qaPair.DatasetQID
	if qid == "" {
		qid = strconv.Itoa(qaPair.QID)
	}
	return &types.EvaluationQuestionResultInput{
		SampleIndex:      index,
		QID:              qid,
		Question:         qaPair.Question,
		ReferenceAnswer:  qaPair.Answer,
		GroundTruthPIDs:  append([]int(nil), qaPair.PIDs...),
		SearchResults:    evaluationRankedResultsWithProvenance(tracked.searchResult, h.knowledgeID),
		RerankResults:    evaluationRankedResultsWithProvenance(tracked.rerankResult, h.knowledgeID),
		GenerationPIDs:   evaluationGenerationPIDOrder(effectiveRanked, rerankTopK),
		GeneratedText:    generatedText,
		PerSampleMetrics: perSample,
		Observations:     observations,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
		Status:           types.EvaluationQuestionStatusSuccess,
	}
}
