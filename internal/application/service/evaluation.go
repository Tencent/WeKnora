package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/Tencent/WeKnora/internal/application/service/metric"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

type evaluationMetricRegistration struct {
	definition types.EvaluationMetricDefinition
	calculator interfaces.Metrics
}

var evaluationMetricRegistry = []evaluationMetricRegistration{
	{types.EvaluationMetricDefinition{Name: "precision", Version: "v1", Category: "retrieval", HigherIsBetter: true, RequiresContext: true}, metric.NewPrecisionMetric()},
	{types.EvaluationMetricDefinition{Name: "recall", Version: "v1", Category: "retrieval", HigherIsBetter: true, RequiresContext: true}, metric.NewRecallMetric()},
	{types.EvaluationMetricDefinition{Name: "ndcg3", Version: "v1", Category: "retrieval", HigherIsBetter: true, RequiresContext: true}, metric.NewNDCGMetric(3)},
	{types.EvaluationMetricDefinition{Name: "ndcg10", Version: "v1", Category: "retrieval", HigherIsBetter: true, RequiresContext: true}, metric.NewNDCGMetric(10)},
	{types.EvaluationMetricDefinition{Name: "mrr", Version: "v1", Category: "retrieval", HigherIsBetter: true, RequiresContext: true}, metric.NewMRRMetric()},
	{types.EvaluationMetricDefinition{Name: "map", Version: "v1", Category: "retrieval", HigherIsBetter: true, RequiresContext: true}, metric.NewMAPMetric()},
	{types.EvaluationMetricDefinition{Name: "bleu1", Version: "v1", Category: "generation", HigherIsBetter: true, RequiresAnswer: true}, metric.NewBLEUMetric(true, metric.BLEU1Gram)},
	{types.EvaluationMetricDefinition{Name: "bleu2", Version: "v1", Category: "generation", HigherIsBetter: true, RequiresAnswer: true}, metric.NewBLEUMetric(true, metric.BLEU2Gram)},
	{types.EvaluationMetricDefinition{Name: "bleu4", Version: "v1", Category: "generation", HigherIsBetter: true, RequiresAnswer: true}, metric.NewBLEUMetric(true, metric.BLEU4Gram)},
	{types.EvaluationMetricDefinition{Name: "rouge1", Version: "v1", Category: "generation", HigherIsBetter: true, RequiresAnswer: true}, metric.NewRougeMetric(true, "rouge-1", "f")},
	{types.EvaluationMetricDefinition{Name: "rouge2", Version: "v1", Category: "generation", HigherIsBetter: true, RequiresAnswer: true}, metric.NewRougeMetric(true, "rouge-2", "f")},
	{types.EvaluationMetricDefinition{Name: "rougel", Version: "v1", Category: "generation", HigherIsBetter: true, RequiresAnswer: true}, metric.NewRougeMetric(true, "rouge-l", "f")},
}

type EvaluationService struct {
	config               *config.Config
	repository           interfaces.EvaluationRepository
	dataset              interfaces.DatasetService
	knowledgeBaseService interfaces.KnowledgeBaseService
	sessionService       interfaces.SessionService
	modelService         interfaces.ModelService
}

func NewEvaluationService(cfg *config.Config, repository interfaces.EvaluationRepository, dataset interfaces.DatasetService,
	knowledgeBaseService interfaces.KnowledgeBaseService, sessionService interfaces.SessionService,
	modelService interfaces.ModelService) interfaces.EvaluationService {
	return &EvaluationService{config: cfg, repository: repository, dataset: dataset, knowledgeBaseService: knowledgeBaseService, sessionService: sessionService, modelService: modelService}
}

func (e *EvaluationService) ListMetrics(context.Context) []types.EvaluationMetricDefinition {
	result := make([]types.EvaluationMetricDefinition, 0, len(evaluationMetricRegistry))
	for _, m := range evaluationMetricRegistry {
		result = append(result, m.definition)
	}
	return result
}

func tenantID(ctx context.Context) uint64 { return types.MustTenantIDFromContext(ctx) }
func jsonValue(value interface{}) (types.JSON, error) {
	data, err := json.Marshal(value)
	return types.JSON(data), err
}

func validateReferenceContexts(contexts []types.EvaluationReferenceContext) error {
	for index := range contexts {
		contexts[index].Text = strings.TrimSpace(contexts[index].Text)
		if contexts[index].Text == "" {
			return fmt.Errorf("reference_contexts[%d].text is required", index)
		}
	}
	return nil
}

func (e *EvaluationService) CreateDataset(ctx context.Context, req *types.CreateEvaluationDatasetRequest) (*types.EvaluationDataset, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return nil, fmt.Errorf("dataset name is required")
	}
	d := &types.EvaluationDataset{TenantID: tenantID(ctx), Name: req.Name, Description: strings.TrimSpace(req.Description)}
	if err := e.repository.CreateDataset(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}
func (e *EvaluationService) GetDataset(ctx context.Context, id string) (*types.EvaluationDataset, error) {
	return e.repository.GetDataset(ctx, tenantID(ctx), id)
}
func (e *EvaluationService) ListDatasets(ctx context.Context, page *types.Pagination) (*types.PageResult, error) {
	return e.repository.ListDatasets(ctx, tenantID(ctx), page)
}
func (e *EvaluationService) UpdateDataset(ctx context.Context, id string, req *types.UpdateEvaluationDatasetRequest) (*types.EvaluationDataset, error) {
	d, err := e.GetDataset(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		v := strings.TrimSpace(*req.Name)
		if v == "" {
			return nil, fmt.Errorf("dataset name is required")
		}
		d.Name = v
	}
	if req.Description != nil {
		d.Description = strings.TrimSpace(*req.Description)
	}
	if err = e.repository.UpdateDataset(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}
func (e *EvaluationService) DeleteDataset(ctx context.Context, id string) error {
	if _, err := e.GetDataset(ctx, id); err != nil {
		return err
	}
	return e.repository.DeleteDataset(ctx, tenantID(ctx), id)
}

func (e *EvaluationService) CreateSample(ctx context.Context, datasetID string, req *types.CreateEvaluationSampleRequest) (*types.EvaluationSample, error) {
	if _, err := e.GetDataset(ctx, datasetID); err != nil {
		return nil, err
	}
	req.Question = strings.TrimSpace(req.Question)
	req.ReferenceAnswer = strings.TrimSpace(req.ReferenceAnswer)
	if req.Question == "" || req.ReferenceAnswer == "" {
		return nil, fmt.Errorf("question and reference_answer are required")
	}
	if err := validateReferenceContexts(req.ReferenceContexts); err != nil {
		return nil, err
	}
	contexts, err := jsonValue(req.ReferenceContexts)
	if err != nil {
		return nil, err
	}
	s := &types.EvaluationSample{TenantID: tenantID(ctx), DatasetID: datasetID, Question: req.Question, ReferenceAnswer: req.ReferenceAnswer, ReferenceContexts: contexts}
	if err = e.repository.CreateSample(ctx, s); err != nil {
		return nil, err
	}
	return s, nil
}
func (e *EvaluationService) ListSamples(ctx context.Context, datasetID string, page *types.Pagination) (*types.PageResult, error) {
	if _, err := e.GetDataset(ctx, datasetID); err != nil {
		return nil, err
	}
	return e.repository.ListSamples(ctx, tenantID(ctx), datasetID, page)
}
func (e *EvaluationService) UpdateSample(ctx context.Context, datasetID, id string, req *types.UpdateEvaluationSampleRequest) (*types.EvaluationSample, error) {
	s, err := e.repository.GetSample(ctx, tenantID(ctx), datasetID, id)
	if err != nil {
		return nil, err
	}
	if req.Question != nil {
		v := strings.TrimSpace(*req.Question)
		if v == "" {
			return nil, fmt.Errorf("question is required")
		}
		s.Question = v
	}
	if req.ReferenceAnswer != nil {
		v := strings.TrimSpace(*req.ReferenceAnswer)
		if v == "" {
			return nil, fmt.Errorf("reference_answer is required")
		}
		s.ReferenceAnswer = v
	}
	if req.ReferenceContexts != nil {
		if err := validateReferenceContexts(*req.ReferenceContexts); err != nil {
			return nil, err
		}
		s.ReferenceContexts, err = jsonValue(*req.ReferenceContexts)
		if err != nil {
			return nil, err
		}
	}
	if err = e.repository.UpdateSample(ctx, s); err != nil {
		return nil, err
	}
	return s, nil
}
func (e *EvaluationService) DeleteSample(ctx context.Context, datasetID, id string) error {
	if _, err := e.repository.GetSample(ctx, tenantID(ctx), datasetID, id); err != nil {
		return err
	}
	return e.repository.DeleteSample(ctx, tenantID(ctx), datasetID, id)
}

func defaultMetricSelections() []types.EvaluationMetricSelection {
	r := make([]types.EvaluationMetricSelection, 0, len(evaluationMetricRegistry))
	for _, m := range evaluationMetricRegistry {
		r = append(r, types.EvaluationMetricSelection{Name: m.definition.Name, Version: m.definition.Version})
	}
	return r
}
func metricRegistration(name, version string) (evaluationMetricRegistration, bool) {
	for _, m := range evaluationMetricRegistry {
		if m.definition.Name == name && m.definition.Version == version {
			return m, true
		}
	}
	return evaluationMetricRegistration{}, false
}

func (e *EvaluationService) snapshot(req *types.CreateEvaluationRunRequest) types.EvaluationConfigSnapshot {
	c := e.config.Conversation
	s := types.EvaluationConfigSnapshot{KnowledgeBaseID: req.KnowledgeBaseID, ChatModelID: req.ChatModelID, RerankModelID: req.RerankModelID, VectorThreshold: c.VectorThreshold, KeywordThreshold: c.KeywordThreshold, EmbeddingTopK: c.EmbeddingTopK, RerankTopK: c.RerankTopK, RerankThreshold: c.RerankThreshold, FallbackStrategy: types.FallbackStrategy(c.FallbackStrategy), FallbackResponse: c.FallbackResponse, Metrics: req.Metrics}
	if c.Summary != nil {
		s.SummaryConfig = types.SummaryConfig{MaxTokens: c.Summary.MaxTokens, RepeatPenalty: c.Summary.RepeatPenalty, TopK: c.Summary.TopK, TopP: c.Summary.TopP, FrequencyPenalty: c.Summary.FrequencyPenalty, PresencePenalty: c.Summary.PresencePenalty, Prompt: c.Summary.Prompt, ContextTemplate: c.Summary.ContextTemplate, NoMatchPrefix: c.Summary.NoMatchPrefix, Temperature: c.Summary.Temperature, Seed: c.Summary.Seed, MaxCompletionTokens: c.Summary.MaxCompletionTokens, Thinking: c.Summary.Thinking}
	}
	if req.VectorThreshold != nil {
		s.VectorThreshold = *req.VectorThreshold
	}
	if req.KeywordThreshold != nil {
		s.KeywordThreshold = *req.KeywordThreshold
	}
	if req.EmbeddingTopK != nil {
		s.EmbeddingTopK = *req.EmbeddingTopK
	}
	if req.RerankTopK != nil {
		s.RerankTopK = *req.RerankTopK
	}
	if req.RerankThreshold != nil {
		s.RerankThreshold = *req.RerankThreshold
	}
	if req.SummaryConfig != nil {
		s.SummaryConfig = *req.SummaryConfig
	}
	if req.FallbackStrategy != nil {
		s.FallbackStrategy = *req.FallbackStrategy
	}
	if req.FallbackResponse != nil {
		s.FallbackResponse = *req.FallbackResponse
	}
	return s
}

func (e *EvaluationService) validateRun(ctx context.Context, req *types.CreateEvaluationRunRequest) error {
	if req.DatasetID == "" || req.KnowledgeBaseID == "" || req.ChatModelID == "" {
		return fmt.Errorf("dataset_id, knowledge_base_id and chat_model_id are required")
	}
	if _, err := e.knowledgeBaseService.GetKnowledgeBaseByID(ctx, req.KnowledgeBaseID); err != nil {
		return fmt.Errorf("knowledge base: %w", err)
	}
	chatModel, err := e.modelService.GetModelByID(ctx, req.ChatModelID)
	if err != nil {
		return fmt.Errorf("chat model: %w", err)
	}
	if chatModel.Type != types.ModelTypeKnowledgeQA {
		return fmt.Errorf("chat_model_id must reference a KnowledgeQA model")
	}
	if req.RerankModelID != "" {
		rerankModel, err := e.modelService.GetModelByID(ctx, req.RerankModelID)
		if err != nil {
			return fmt.Errorf("rerank model: %w", err)
		}
		if rerankModel.Type != types.ModelTypeRerank {
			return fmt.Errorf("rerank_model_id must reference a Rerank model")
		}
	}
	if len(req.Metrics) == 0 {
		req.Metrics = defaultMetricSelections()
	}
	seen := map[string]bool{}
	for _, m := range req.Metrics {
		k := m.Name + "@" + m.Version
		if seen[k] {
			return fmt.Errorf("duplicate metric %s", k)
		}
		seen[k] = true
		if _, ok := metricRegistration(m.Name, m.Version); !ok {
			return fmt.Errorf("unsupported metric %s", k)
		}
	}
	s := e.snapshot(req)
	if s.VectorThreshold < 0 || s.VectorThreshold > 1 || s.KeywordThreshold < 0 || s.KeywordThreshold > 1 {
		return fmt.Errorf("retrieval thresholds must be between 0 and 1")
	}
	if s.EmbeddingTopK <= 0 || s.RerankTopK <= 0 {
		return fmt.Errorf("top_k values must be greater than zero")
	}
	return nil
}

func (e *EvaluationService) loadFrozenSamples(ctx context.Context, datasetID string) (string, []*types.EvaluationSample, error) {
	if datasetID != "default" {
		d, err := e.repository.GetDataset(ctx, tenantID(ctx), datasetID)
		if err != nil {
			return "", nil, err
		}
		samples, err := e.repository.ListAllSamples(ctx, tenantID(ctx), datasetID)
		return d.Name, samples, err
	}
	pairs, err := e.dataset.GetDatasetByID(ctx, "default")
	if err != nil {
		return "", nil, err
	}
	samples := make([]*types.EvaluationSample, 0, len(pairs))
	for i, p := range pairs {
		refs := make([]types.EvaluationReferenceContext, 0, len(p.Passages))
		for _, text := range p.Passages {
			refs = append(refs, types.EvaluationReferenceContext{Text: text})
		}
		raw, _ := jsonValue(refs)
		samples = append(samples, &types.EvaluationSample{ID: fmt.Sprintf("default-%d-%d", p.QID, i), TenantID: tenantID(ctx), DatasetID: "default", Question: p.Question, ReferenceAnswer: p.Answer, ReferenceContexts: raw})
	}
	return "内置默认数据集", samples, nil
}

func (e *EvaluationService) CreateRun(ctx context.Context, req *types.CreateEvaluationRunRequest) (*types.EvaluationRun, error) {
	if err := e.validateRun(ctx, req); err != nil {
		return nil, err
	}
	datasetName, samples, err := e.loadFrozenSamples(ctx, req.DatasetID)
	if err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("evaluation dataset has no samples")
	}
	snapshot := e.snapshot(req)
	snapshotJSON, err := jsonValue(snapshot)
	if err != nil {
		return nil, err
	}
	emptyScores, _ := jsonValue(types.EvaluationMetricScores{})
	run := &types.EvaluationRun{TenantID: tenantID(ctx), DatasetID: req.DatasetID, DatasetName: datasetName, Status: types.EvaluationRunPending, ConfigSnapshot: snapshotJSON, AggregateMetricScores: emptyScores, TotalSamples: len(samples)}
	results := make([]*types.EvaluationRunResult, 0, len(samples))
	for i, s := range samples {
		results = append(results, &types.EvaluationRunResult{TenantID: run.TenantID, RunID: run.ID, SampleID: s.ID, SampleIndex: i, Question: s.Question, ReferenceAnswer: s.ReferenceAnswer, ReferenceContexts: s.ReferenceContexts, RetrievedContexts: types.JSON("[]"), MetricScores: types.JSON("{}"), Status: types.EvaluationResultPending})
	}
	// BeforeCreate assigns run.ID, so assign it before the transactional insert.
	if run.ID == "" {
		run.ID = uuid.NewString()
	}
	for _, r := range results {
		r.RunID = run.ID
	}
	if err = e.repository.CreateRunWithResults(ctx, run, results); err != nil {
		return nil, err
	}
	background := logger.CloneContext(ctx)
	go e.executeRun(background, run.TenantID, run.ID)
	return run, nil
}

func decodeJSON[T any](raw types.JSON) (T, error) {
	var value T
	err := json.Unmarshal(raw, &value)
	return value, err
}
func (e *EvaluationService) GetRun(ctx context.Context, id string) (*types.EvaluationRun, error) {
	return e.repository.GetRun(ctx, tenantID(ctx), id)
}
func (e *EvaluationService) ListRuns(ctx context.Context, page *types.Pagination) (*types.PageResult, error) {
	return e.repository.ListRuns(ctx, tenantID(ctx), page)
}
func (e *EvaluationService) ListRunResults(ctx context.Context, runID string, page *types.Pagination) (*types.PageResult, error) {
	if _, err := e.GetRun(ctx, runID); err != nil {
		return nil, err
	}
	return e.repository.ListRunResults(ctx, tenantID(ctx), runID, page)
}
func (e *EvaluationService) ReconcileInterruptedRuns(ctx context.Context) error {
	return e.repository.MarkInterruptedRunsFailed(ctx, "service restarted before evaluation completed")
}

func (e *EvaluationService) executeRun(ctx context.Context, tenant uint64, runID string) {
	run, err := e.repository.GetRun(ctx, tenant, runID)
	if err != nil {
		return
	}
	now := time.Now()
	run.Status = types.EvaluationRunRunning
	run.StartedAt = &now
	if err = e.repository.UpdateRun(ctx, run); err != nil {
		return
	}
	snapshot, err := decodeJSON[types.EvaluationConfigSnapshot](run.ConfigSnapshot)
	if err != nil {
		e.failRun(ctx, run, err)
		return
	}
	results, err := e.repository.ListAllRunResults(ctx, tenant, runID)
	if err != nil {
		e.failRun(ctx, run, err)
		return
	}
	for _, result := range results {
		start := time.Now()
		result.Status = types.EvaluationResultRunning
		if err = e.repository.UpdateRunResult(ctx, result); err != nil {
			e.failRun(ctx, run, err)
			return
		}
		scores := types.EvaluationMetricScores{}
		var refs []types.EvaluationReferenceContext
		_ = json.Unmarshal(result.ReferenceContexts, &refs)
		chatManage := &types.ChatManage{PipelineRequest: types.PipelineRequest{Query: result.Question, KnowledgeBaseIDs: []string{snapshot.KnowledgeBaseID}, SearchTargets: types.SearchTargets{&types.SearchTarget{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: snapshot.KnowledgeBaseID, TenantID: tenant}}, VectorThreshold: snapshot.VectorThreshold, KeywordThreshold: snapshot.KeywordThreshold, EmbeddingTopK: snapshot.EmbeddingTopK, RerankModelID: snapshot.RerankModelID, RerankTopK: snapshot.RerankTopK, RerankThreshold: snapshot.RerankThreshold, ChatModelID: snapshot.ChatModelID, SummaryConfig: snapshot.SummaryConfig, FallbackStrategy: snapshot.FallbackStrategy, FallbackResponse: snapshot.FallbackResponse, TenantID: tenant}, PipelineState: types.PipelineState{RewriteQuery: result.Question}}
		err = e.sessionService.KnowledgeQAByEvent(ctx, chatManage, types.Pipline["rag"])
		retrieved := toRetrievedContexts(chatManage)
		result.RetrievedContexts, _ = jsonValue(retrieved)
		if chatManage.ChatResponse != nil {
			result.GeneratedAnswer = chatManage.ChatResponse.Content
		}
		if err != nil {
			result.Status = types.EvaluationResultFailed
			result.Error = err.Error()
			run.FailedSamples++
		} else {
			scores = computeEvaluationScores(snapshot.Metrics, result.ReferenceAnswer, refs, result.GeneratedAnswer, retrieved)
			result.MetricScores, _ = jsonValue(scores)
			result.Status = types.EvaluationResultCompleted
		}
		result.DurationMilliseconds = time.Since(start).Milliseconds()
		run.FinishedSamples++
		if updateErr := e.repository.UpdateRunResult(ctx, result); updateErr != nil {
			e.failRun(ctx, run, updateErr)
			return
		}
		run.AggregateMetricScores, _ = e.aggregate(ctx, tenant, runID, run.TotalSamples)
		if updateErr := e.repository.UpdateRun(ctx, run); updateErr != nil {
			return
		}
	}
	done := time.Now()
	run.Status = types.EvaluationRunCompleted
	run.CompletedAt = &done
	run.AggregateMetricScores, _ = e.aggregate(ctx, tenant, runID, run.TotalSamples)
	if err := e.repository.UpdateRun(ctx, run); err != nil {
		logger.Errorf(ctx, "failed to complete evaluation run %s: %v", runID, err)
	}
}
func (e *EvaluationService) failRun(ctx context.Context, run *types.EvaluationRun, err error) {
	now := time.Now()
	run.Status = types.EvaluationRunFailed
	run.Error = err.Error()
	run.CompletedAt = &now
	_ = e.repository.UpdateRun(ctx, run)
}

func toRetrievedContexts(chat *types.ChatManage) []types.EvaluationRetrievedContext {
	items := chat.MergeResult
	if len(items) == 0 {
		items = chat.RerankResult
	}
	if len(items) == 0 {
		items = chat.SearchResult
	}
	result := make([]types.EvaluationRetrievedContext, 0, len(items))
	for i, item := range items {
		result = append(result, types.EvaluationRetrievedContext{Text: item.Content, KnowledgeID: item.KnowledgeID, ChunkID: item.ID, Score: item.Score, Rank: i + 1})
	}
	return result
}
func normalizeEvaluationText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, strings.TrimSpace(value))
}
func retrievalMetricInput(refs []types.EvaluationReferenceContext, retrieved []types.EvaluationRetrievedContext) *types.MetricInput {
	gt := make([]int, 0, len(refs))
	for i := range refs {
		gt = append(gt, i+1)
	}
	pred := make([]int, 0, len(retrieved))
	for retrievedIndex, item := range retrieved {
		matchedID := 0
		for referenceIndex, reference := range refs {
			if reference.ChunkID != "" && item.ChunkID != "" {
				if reference.ChunkID == item.ChunkID {
					matchedID = referenceIndex + 1
					break
				}
				continue
			}
			if normalizeEvaluationText(reference.Text) == normalizeEvaluationText(item.Text) {
				matchedID = referenceIndex + 1
				break
			}
		}
		if matchedID == 0 {
			matchedID = len(refs) + retrievedIndex + 1
		}
		pred = append(pred, matchedID)
	}
	return &types.MetricInput{RetrievalGT: [][]int{gt}, RetrievalIDs: pred}
}
func computeMetricSafely(calc interfaces.Metrics, input *types.MetricInput) (score float64, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("metric panic: %v", recovered)
		}
	}()
	return calc.Compute(input), nil
}
func computeEvaluationScores(selected []types.EvaluationMetricSelection, referenceAnswer string, refs []types.EvaluationReferenceContext, generated string, retrieved []types.EvaluationRetrievedContext) types.EvaluationMetricScores {
	scores := types.EvaluationMetricScores{}
	retrievalInput := retrievalMetricInput(refs, retrieved)
	for _, selection := range selected {
		registration, _ := metricRegistration(selection.Name, selection.Version)
		d := registration.definition
		entry := types.EvaluationMetricScore{Name: d.Name, Version: d.Version, Category: d.Category, Status: types.EvaluationMetricSkipped, HigherIsBetter: d.HigherIsBetter}
		if d.RequiresContext && len(refs) == 0 {
			entry.Status = types.EvaluationMetricNotApplicable
			entry.Reason = "reference_contexts missing"
			scores[d.Name] = entry
			continue
		}
		if d.RequiresAnswer && strings.TrimSpace(referenceAnswer) == "" {
			entry.Status = types.EvaluationMetricNotApplicable
			entry.Reason = "reference_answer missing"
			scores[d.Name] = entry
			continue
		}
		input := retrievalInput
		if d.RequiresAnswer {
			input = &types.MetricInput{GeneratedTexts: generated, GeneratedGT: referenceAnswer}
		}
		value, err := computeMetricSafely(registration.calculator, input)
		if err != nil {
			entry.Status = types.EvaluationMetricFailed
			entry.Error = err.Error()
		} else {
			entry.Status = types.EvaluationMetricScored
			entry.Score = &value
		}
		scores[d.Name] = entry
	}
	return scores
}
func (e *EvaluationService) aggregate(ctx context.Context, tenant uint64, runID string, total int) (types.JSON, error) {
	results, err := e.repository.ListAllRunResults(ctx, tenant, runID)
	if err != nil {
		return nil, err
	}
	return aggregateEvaluationMetricScores(results, total)
}

func aggregateEvaluationMetricScores(results []*types.EvaluationRunResult, total int) (types.JSON, error) {
	sums := map[string]float64{}
	counts := map[string]int{}
	templates := map[string]types.EvaluationMetricScore{}
	for _, r := range results {
		scores, decodeErr := decodeJSON[types.EvaluationMetricScores](r.MetricScores)
		if decodeErr != nil {
			continue
		}
		for name, score := range scores {
			current, exists := templates[name]
			if !exists || aggregateStatusPriority(score.Status) > aggregateStatusPriority(current.Status) {
				templates[name] = score
			}
			if score.Status == types.EvaluationMetricScored && score.Score != nil {
				sums[name] += *score.Score
				counts[name]++
			}
		}
	}
	aggregate := types.EvaluationMetricScores{}
	for name, entry := range templates {
		count := counts[name]
		entry.ScoredSampleCount = count
		entry.TotalSampleCount = total
		if count == 0 {
			entry.Score = nil
			aggregate[name] = entry
			continue
		}
		value := sums[name] / float64(count)
		entry.Score = &value
		entry.Status = types.EvaluationMetricScored
		entry.Reason = ""
		entry.Error = ""
		aggregate[name] = entry
	}
	return jsonValue(aggregate)
}

func aggregateStatusPriority(status types.EvaluationMetricStatus) int {
	switch status {
	case types.EvaluationMetricFailed:
		return 3
	case types.EvaluationMetricNotApplicable:
		return 2
	case types.EvaluationMetricSkipped:
		return 1
	case types.EvaluationMetricScored:
		return 4
	default:
		return 0
	}
}

func (e *EvaluationService) CompareRuns(ctx context.Context, baselineID, candidateID string) (*types.EvaluationComparison, error) {
	baseline, err := e.GetRun(ctx, baselineID)
	if err != nil {
		return nil, err
	}
	candidate, err := e.GetRun(ctx, candidateID)
	if err != nil {
		return nil, err
	}
	if baseline.DatasetID != candidate.DatasetID {
		return nil, fmt.Errorf("runs must use the same dataset")
	}
	baseResults, err := e.repository.ListAllRunResults(ctx, tenantID(ctx), baselineID)
	if err != nil {
		return nil, err
	}
	candidateResults, err := e.repository.ListAllRunResults(ctx, tenantID(ctx), candidateID)
	if err != nil {
		return nil, err
	}
	return compareEvaluationResultSets(baseline.DatasetID, baselineID, candidateID, baseResults, candidateResults), nil
}

func compareEvaluationResultSets(datasetID, baselineID, candidateID string, baseResults, candidateResults []*types.EvaluationRunResult) *types.EvaluationComparison {
	candidateBySample := map[string]*types.EvaluationRunResult{}
	for _, r := range candidateResults {
		candidateBySample[r.SampleID] = r
	}
	metricDeltas := map[string]*types.EvaluationMetricDelta{}
	for _, b := range baseResults {
		c := candidateBySample[b.SampleID]
		if c == nil {
			continue
		}
		bs, _ := decodeJSON[types.EvaluationMetricScores](b.MetricScores)
		cs, _ := decodeJSON[types.EvaluationMetricScores](c.MetricScores)
		for name, bm := range bs {
			cm, ok := cs[name]
			if !ok || bm.Version != cm.Version || bm.Status != types.EvaluationMetricScored || cm.Status != types.EvaluationMetricScored || bm.Score == nil || cm.Score == nil {
				continue
			}
			key := name + "@" + bm.Version
			d := metricDeltas[key]
			if d == nil {
				d = &types.EvaluationMetricDelta{Name: name, Version: bm.Version}
				metricDeltas[key] = d
			}
			delta := *cm.Score - *bm.Score
			d.SampleDeltas = append(d.SampleDeltas, types.EvaluationSampleMetricDelta{SampleID: b.SampleID, BaselineScore: *bm.Score, CandidateScore: *cm.Score, Delta: delta})
			d.BaselineScore += *bm.Score
			d.CandidateScore += *cm.Score
		}
	}
	comparison := &types.EvaluationComparison{BaselineRunID: baselineID, CandidateRunID: candidateID, DatasetID: datasetID}
	for _, d := range metricDeltas {
		d.ComparableSampleCount = len(d.SampleDeltas)
		if d.ComparableSampleCount > 0 {
			d.BaselineScore /= float64(d.ComparableSampleCount)
			d.CandidateScore /= float64(d.ComparableSampleCount)
			d.Delta = d.CandidateScore - d.BaselineScore
			registration, _ := metricRegistration(d.Name, d.Version)
			d.Improved = (registration.definition.HigherIsBetter && d.Delta > 0) || (!registration.definition.HigherIsBetter && d.Delta < 0)
		}
		comparison.Metrics = append(comparison.Metrics, *d)
	}
	return comparison
}

func legacyParams(snapshot types.EvaluationConfigSnapshot) *types.ChatManage {
	return &types.ChatManage{PipelineRequest: types.PipelineRequest{KnowledgeBaseIDs: []string{snapshot.KnowledgeBaseID}, VectorThreshold: snapshot.VectorThreshold, KeywordThreshold: snapshot.KeywordThreshold, EmbeddingTopK: snapshot.EmbeddingTopK, RerankModelID: snapshot.RerankModelID, RerankTopK: snapshot.RerankTopK, RerankThreshold: snapshot.RerankThreshold, ChatModelID: snapshot.ChatModelID, SummaryConfig: snapshot.SummaryConfig, FallbackStrategy: snapshot.FallbackStrategy, FallbackResponse: snapshot.FallbackResponse}}
}
func legacyMetric(scores types.EvaluationMetricScores) *types.MetricResult {
	result := &types.MetricResult{}
	get := func(name string) float64 {
		v, ok := scores[name]
		if !ok || v.Status != types.EvaluationMetricScored || v.Score == nil {
			return 0
		}
		return *v.Score
	}
	result.RetrievalMetrics = types.RetrievalMetrics{Precision: get("precision"), Recall: get("recall"), NDCG3: get("ndcg3"), NDCG10: get("ndcg10"), MRR: get("mrr"), MAP: get("map")}
	result.GenerationMetrics = types.GenerationMetrics{BLEU1: get("bleu1"), BLEU2: get("bleu2"), BLEU4: get("bleu4"), ROUGE1: get("rouge1"), ROUGE2: get("rouge2"), ROUGEL: get("rougel")}
	return result
}
func legacyStatus(status types.EvaluationRunStatus) types.EvaluationStatue {
	switch status {
	case types.EvaluationRunRunning:
		return types.EvaluationStatueRunning
	case types.EvaluationRunCompleted:
		return types.EvaluationStatueSuccess
	case types.EvaluationRunFailed:
		return types.EvaluationStatueFailed
	default:
		return types.EvaluationStatuePending
	}
}
func (e *EvaluationService) Evaluation(ctx context.Context, datasetID, knowledgeBaseID, chatModelID, rerankModelID string) (*types.EvaluationDetail, error) {
	if datasetID == "" {
		datasetID = "default"
	}
	run, err := e.CreateRun(ctx, &types.CreateEvaluationRunRequest{DatasetID: datasetID, KnowledgeBaseID: knowledgeBaseID, ChatModelID: chatModelID, RerankModelID: rerankModelID})
	if err != nil {
		return nil, err
	}
	snapshot, _ := decodeJSON[types.EvaluationConfigSnapshot](run.ConfigSnapshot)
	return &types.EvaluationDetail{Task: &types.EvaluationTask{ID: run.ID, TenantID: run.TenantID, DatasetID: run.DatasetID, StartTime: run.CreatedAt, Status: legacyStatus(run.Status), Total: run.TotalSamples, Finished: run.FinishedSamples}, Params: legacyParams(snapshot)}, nil
}
func (e *EvaluationService) EvaluationResult(ctx context.Context, taskID string) (*types.EvaluationDetail, error) {
	run, err := e.GetRun(ctx, taskID)
	if err != nil {
		return nil, err
	}
	snapshot, err := decodeJSON[types.EvaluationConfigSnapshot](run.ConfigSnapshot)
	if err != nil {
		return nil, err
	}
	scores, err := decodeJSON[types.EvaluationMetricScores](run.AggregateMetricScores)
	if err != nil {
		return nil, err
	}
	return &types.EvaluationDetail{Task: &types.EvaluationTask{ID: run.ID, TenantID: run.TenantID, DatasetID: run.DatasetID, StartTime: run.CreatedAt, Status: legacyStatus(run.Status), ErrMsg: run.Error, Total: run.TotalSamples, Finished: run.FinishedSamples}, Params: legacyParams(snapshot), Metric: legacyMetric(scores)}, nil
}
