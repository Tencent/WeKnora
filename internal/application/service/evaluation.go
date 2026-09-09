package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"golang.org/x/sync/errgroup"
)

/*
corpus: pid -> content
queries: qid -> content
answers: aid -> content
qrels: qid -> pid
arels: qid -> aid
*/

// EvaluationService handles evaluation tasks for knowledge base and chat models
type EvaluationService struct {
	config               *config.Config                  // Application configuration
	dataset              interfaces.DatasetService       // Service for dataset operations
	knowledgeBaseService interfaces.KnowledgeBaseService // Service for knowledge base operations
	knowledgeService     interfaces.KnowledgeService     // Service for knowledge operations
	sessionService       interfaces.SessionService       // Service for chat sessions
	modelService         interfaces.ModelService         // Service for model operations
	evaluationRepo       interfaces.EvaluationRepository // Durable evaluation storage

	evaluationMemoryStorage *evaluationMemoryStorage // In-memory storage for evaluation tasks
}

func NewEvaluationService(
	config *config.Config,
	dataset interfaces.DatasetService,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	sessionService interfaces.SessionService,
	modelService interfaces.ModelService,
	evaluationRepo interfaces.EvaluationRepository,
) interfaces.EvaluationService {
	evaluationMemoryStorage := newEvaluationMemoryStorage()
	service := &EvaluationService{
		config:                  config,
		dataset:                 dataset,
		knowledgeBaseService:    knowledgeBaseService,
		knowledgeService:        knowledgeService,
		sessionService:          sessionService,
		modelService:            modelService,
		evaluationRepo:          evaluationRepo,
		evaluationMemoryStorage: evaluationMemoryStorage,
	}
	if err := evaluationRepo.MarkRunningInterrupted(context.Background()); err != nil {
		logger.Errorf(context.Background(), "Failed to mark interrupted evaluations: %v", err)
	}
	return service
}

// evaluationMemoryStorage stores evaluation tasks in memory with thread-safe access
type evaluationMemoryStorage struct {
	store map[string]*types.EvaluationDetail // Map of taskID to evaluation details
	mu    *sync.RWMutex                      // Read-write lock for concurrent access
}

func newEvaluationMemoryStorage() *evaluationMemoryStorage {
	res := &evaluationMemoryStorage{
		store: make(map[string]*types.EvaluationDetail),
		mu:    &sync.RWMutex{},
	}
	return res
}

func (e *evaluationMemoryStorage) register(params *types.EvaluationDetail) {
	e.mu.Lock()
	defer e.mu.Unlock()
	logger.Infof(context.Background(), "Registering evaluation task: %s", params.Task.ID)
	e.store[params.Task.ID] = params
}

func (e *evaluationMemoryStorage) get(taskID string) (*types.EvaluationDetail, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	logger.Infof(context.Background(), "Getting evaluation task: %s", taskID)
	res, ok := e.store[taskID]
	if !ok {
		return nil, errors.New("task not found")
	}
	return res, nil
}

func (e *evaluationMemoryStorage) update(taskID string, fn func(params *types.EvaluationDetail)) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	params, ok := e.store[taskID]
	if !ok {
		return errors.New("task not found")
	}
	fn(params)
	return nil
}

func (e *EvaluationService) EvaluationResult(ctx context.Context, taskID string) (*types.EvaluationDetail, error) {
	logger.Info(ctx, "Start getting evaluation result")
	logger.Infof(ctx, "Task ID: %s", taskID)

	tenantID := types.MustTenantIDFromContext(ctx)
	detail, err := e.evaluationRepo.Get(ctx, tenantID, taskID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get evaluation task: %v", err)
		return nil, err
	}

	logger.Infof(
		ctx,
		"Checking tenant ID match, task tenant ID: %d, current tenant ID: %d",
		detail.Task.TenantID, tenantID,
	)

	if tenantID != detail.Task.TenantID {
		logger.Error(ctx, "Tenant ID mismatch")
		return nil, errors.New("tenant ID does not match")
	}

	logger.Info(ctx, "Evaluation result retrieved successfully")
	return detail, nil
}

func (e *EvaluationService) EvaluationHistory(ctx context.Context, limit int) ([]*types.EvaluationDetail, error) {
	return e.evaluationRepo.List(ctx, types.MustTenantIDFromContext(ctx), limit)
}

func (e *EvaluationService) persistUpdate(ctx context.Context, taskID string, fn func(*types.EvaluationDetail)) error {
	var detail *types.EvaluationDetail
	if err := e.evaluationMemoryStorage.update(taskID, func(params *types.EvaluationDetail) {
		fn(params)
		detail = params
	}); err != nil {
		return err
	}
	return e.evaluationRepo.Update(ctx, detail)
}

// Evaluation starts a new evaluation task with given parameters
// datasetID: ID of the dataset to evaluate against
// knowledgeBaseID: ID of the knowledge base to use (empty to create new)
// chatModelID: ID of the chat model to evaluate
// rerankModelID: ID of the rerank model to evaluate
func (e *EvaluationService) Evaluation(ctx context.Context,
	datasetID string, knowledgeBaseID string, chatModelID string, rerankModelID string,
) (*types.EvaluationDetail, error) {
	logger.Info(ctx, "Start evaluation")
	logger.Infof(ctx, "Dataset ID: %s, Knowledge Base ID: %s, Chat Model ID: %s, Rerank Model ID: %s",
		datasetID, knowledgeBaseID, chatModelID, rerankModelID)

	// Get tenant ID from context for multi-tenancy support
	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Tenant ID: %d", tenantID)
	sourceKnowledgeBaseID := knowledgeBaseID
	selectedEmbeddingModelID := ""

	// Handle knowledge base creation if not provided
	if knowledgeBaseID == "" {
		logger.Info(ctx, "No knowledge base ID provided, creating new knowledge base")
		// Create new knowledge base with default evaluation settings
		// 获取默认的嵌入模型和LLM模型
		models, err := e.modelService.ListModels(ctx)
		if err != nil {
			logger.Errorf(ctx, "Failed to list models: %v", err)
			return nil, err
		}

		var embeddingModelID, llmModelID string
		for _, model := range models {
			if model == nil {
				continue
			}
			if model.Type == types.ModelTypeEmbedding {
				embeddingModelID = model.ID
			}
			if model.Type == types.ModelTypeKnowledgeQA {
				llmModelID = model.ID
			}
		}

		if embeddingModelID == "" || llmModelID == "" {
			return nil, fmt.Errorf("no default models found for evaluation")
		}
		selectedEmbeddingModelID = embeddingModelID

		kb, err := e.knowledgeBaseService.CreateKnowledgeBase(ctx, &types.KnowledgeBase{
			Name:             "evaluation",
			Description:      "evaluation",
			EmbeddingModelID: embeddingModelID,
			SummaryModelID:   llmModelID,
		})
		if err != nil {
			logger.Errorf(ctx, "Failed to create knowledge base: %v", err)
			return nil, err
		}
		knowledgeBaseID = kb.ID
		logger.Infof(ctx, "Created new knowledge base with ID: %s", knowledgeBaseID)
	} else {
		logger.Infof(ctx, "Using existing knowledge base ID: %s", knowledgeBaseID)
		// Create evaluation-specific knowledge base based on existing one
		kb, err := e.knowledgeBaseService.GetKnowledgeBaseByID(ctx, knowledgeBaseID)
		if err != nil {
			logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
			return nil, err
		}
		selectedEmbeddingModelID = kb.EmbeddingModelID

		kb, err = e.knowledgeBaseService.CreateKnowledgeBase(ctx, &types.KnowledgeBase{
			Name:             "evaluation",
			Description:      "evaluation",
			EmbeddingModelID: kb.EmbeddingModelID,
			SummaryModelID:   kb.SummaryModelID,
		})
		if err != nil {
			logger.Errorf(ctx, "Failed to create knowledge base: %v", err)
			return nil, err
		}
		knowledgeBaseID = kb.ID
		logger.Infof(ctx, "Created new knowledge base with ID: %s based on existing one", knowledgeBaseID)
	}

	// Set default values for optional parameters
	if datasetID == "" {
		datasetID = "default"
		logger.Info(ctx, "Using default dataset")
	}

	if rerankModelID == "" {
		// 获取默认的重排模型
		models, err := e.modelService.ListModels(ctx)
		if err == nil {
			for _, model := range models {
				if model == nil {
					continue
				}
				if model.Type == types.ModelTypeRerank {
					rerankModelID = model.ID
					break
				}
			}
		}
		if rerankModelID == "" {
			logger.Warnf(ctx, "No rerank model found, skipping rerank")
		} else {
			logger.Infof(ctx, "Using default rerank model: %s", rerankModelID)
		}
	}

	if chatModelID == "" {
		// 获取默认的LLM模型
		models, err := e.modelService.ListModels(ctx)
		if err == nil {
			for _, model := range models {
				if model == nil {
					continue
				}
				if model.Type == types.ModelTypeKnowledgeQA {
					chatModelID = model.ID
					break
				}
			}
		}
		if chatModelID == "" {
			return nil, fmt.Errorf("no default chat model found")
		}
		logger.Infof(ctx, "Using default chat model: %s", chatModelID)
	}

	// Create evaluation task with unique ID
	logger.Info(ctx, "Creating evaluation task")
	taskID := utils.GenerateTaskID("evaluation", tenantID, datasetID)
	logger.Infof(ctx, "Generated task ID: %s", taskID)
	datasetHash, err := datasetFingerprint(datasetID)
	if err != nil {
		return nil, fmt.Errorf("fingerprint dataset: %w", err)
	}
	workerCount := evaluationWorkerCount()

	// Prepare evaluation detail with all parameters
	detail := &types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID:        taskID,
			TenantID:  tenantID,
			DatasetID: datasetID,
			Status:    types.EvaluationStatuePending,
			StartTime: time.Now(),
		},
		Params: &types.ChatManage{
			PipelineRequest: types.PipelineRequest{
				VectorThreshold:  e.config.Conversation.VectorThreshold,
				KeywordThreshold: e.config.Conversation.KeywordThreshold,
				EmbeddingTopK:    e.config.Conversation.EmbeddingTopK,
				MaxRounds:        e.config.Conversation.MaxRounds,
				RerankModelID:    rerankModelID,
				RerankTopK:       e.config.Conversation.RerankTopK,
				RerankThreshold:  e.config.Conversation.RerankThreshold,
				ChatModelID:      chatModelID,
				SummaryConfig: types.SummaryConfig{
					MaxTokens:           e.config.Conversation.Summary.MaxTokens,
					RepeatPenalty:       e.config.Conversation.Summary.RepeatPenalty,
					TopK:                e.config.Conversation.Summary.TopK,
					TopP:                e.config.Conversation.Summary.TopP,
					Prompt:              e.config.Conversation.Summary.Prompt,
					ContextTemplate:     e.config.Conversation.Summary.ContextTemplate,
					FrequencyPenalty:    e.config.Conversation.Summary.FrequencyPenalty,
					PresencePenalty:     e.config.Conversation.Summary.PresencePenalty,
					NoMatchPrefix:       e.config.Conversation.Summary.NoMatchPrefix,
					Temperature:         e.config.Conversation.Summary.Temperature,
					Seed:                e.config.Conversation.Summary.Seed,
					MaxCompletionTokens: e.config.Conversation.Summary.MaxCompletionTokens,
				},
				FallbackResponse:    e.config.Conversation.FallbackResponse,
				RewritePromptSystem: e.config.Conversation.RewritePromptSystem,
				RewritePromptUser:   e.config.Conversation.RewritePromptUser,
			},
		},
		Snapshot: &types.EvaluationSnapshot{
			DatasetSHA256: datasetHash, SourceKnowledgeBaseID: sourceKnowledgeBaseID,
			RuntimeKnowledgeBaseID: knowledgeBaseID, ChatModelID: chatModelID,
			EmbeddingModelID: selectedEmbeddingModelID, RerankModelID: rerankModelID,
			CodeCommit: envOrUnknown("TOPIC3_CODE_COMMIT"), PromptVersion: envOrUnknown("TOPIC3_PROMPT_VERSION"),
			PriceVersion: envOrUnknown("TOPIC3_PRICE_VERSION"), Currency: "CNY", Concurrency: workerCount,
		},
		Items: make([]*types.EvaluationItem, 0),
	}

	// Store evaluation task in memory storage
	logger.Info(ctx, "Registering evaluation task")
	e.evaluationMemoryStorage.register(detail)
	if err := e.evaluationRepo.Create(ctx, detail); err != nil {
		return nil, fmt.Errorf("persist evaluation task: %w", err)
	}

	// Start evaluation in background goroutine
	logger.Info(ctx, "Starting evaluation in background")
	go func() {
		// Create new context with logger for background task
		newCtx := logger.CloneContext(ctx)
		logger.Infof(newCtx, "Background evaluation started for task ID: %s", taskID)

		// Update task status to running
		if err := e.persistUpdate(newCtx, taskID, func(params *types.EvaluationDetail) {
			params.Task.Status = types.EvaluationStatueRunning
		}); err != nil {
			logger.Errorf(newCtx, "Failed to persist running evaluation: %v", err)
			return
		}
		logger.Info(newCtx, "Evaluation task status set to running")

		// Execute actual evaluation
		if err := e.EvalDataset(newCtx, detail, knowledgeBaseID); err != nil {
			endTime := time.Now()
			_ = e.persistUpdate(newCtx, taskID, func(params *types.EvaluationDetail) {
				params.Task.Status = types.EvaluationStatueFailed
				params.Task.ErrMsg = err.Error()
				params.Task.EndTime = &endTime
				params.Task.DurationMS = endTime.Sub(params.Task.StartTime).Milliseconds()
			})
			logger.Errorf(newCtx, "Evaluation task failed: %v, task ID: %s", err, taskID)
			return
		}

		// Mark task as completed successfully
		logger.Infof(newCtx, "Evaluation task completed successfully, task ID: %s", taskID)
		endTime := time.Now()
		if err := e.persistUpdate(newCtx, taskID, func(params *types.EvaluationDetail) {
			params.Task.Status = types.EvaluationStatueSuccess
			params.Task.EndTime = &endTime
			params.Task.DurationMS = endTime.Sub(params.Task.StartTime).Milliseconds()
		}); err != nil {
			logger.Errorf(newCtx, "Failed to persist completed evaluation: %v", err)
		}
	}()

	logger.Infof(ctx, "Evaluation task created successfully, task ID: %s", taskID)
	return detail, nil
}

// EvalDataset performs the actual evaluation of a dataset
// Processes each QA pair in parallel and records metrics
func (e *EvaluationService) EvalDataset(ctx context.Context, detail *types.EvaluationDetail, knowledgeBaseID string) error {
	logger.Info(ctx, "Start evaluating dataset")
	logger.Infof(ctx, "Task ID: %s, Dataset ID: %s", detail.Task.ID, detail.Task.DatasetID)

	// Retrieve dataset from storage
	dataset, err := e.dataset.GetDatasetByID(ctx, detail.Task.DatasetID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get dataset: %v", err)
		return err
	}
	logger.Infof(ctx, "Dataset retrieved successfully with %d QA pairs", len(dataset))

	// Update total QA pairs count in task details
	if err := e.persistUpdate(ctx, detail.Task.ID, func(params *types.EvaluationDetail) {
		params.Task.Total = len(dataset)
		logger.Infof(ctx, "Updated task total to %d QA pairs", params.Task.Total)
	}); err != nil {
		return err
	}

	// Extract and organize passages from dataset
	passages := getPassageList(dataset)
	logger.Infof(ctx, "Creating knowledge from %d passages", len(passages))

	// Create knowledge base from passages (sync: wait for indexing to complete before querying)
	knowledge, err := e.knowledgeService.CreateKnowledgeFromPassageSync(ctx, knowledgeBaseID, passages, "")
	if err != nil {
		logger.Errorf(ctx, "Failed to create knowledge from passages: %v", err)
		return err
	}
	logger.Infof(ctx, "Knowledge created and indexed successfully, ID: %s", knowledge.ID)

	// Setup cleanup of temporary resources
	defer func() {
		logger.Infof(ctx, "Cleaning up resources - deleting knowledge: %s", knowledge.ID)
		if err := e.knowledgeService.DeleteKnowledge(ctx, knowledge.ID); err != nil {
			logger.Errorf(ctx, "Failed to delete knowledge: %v, knowledge ID: %s", err, knowledge.ID)
		}

		logger.Infof(ctx, "Cleaning up resources - deleting knowledge base: %s", knowledgeBaseID)
		if err := e.knowledgeBaseService.DeleteKnowledgeBase(ctx, knowledgeBaseID); err != nil {
			logger.Errorf(
				ctx,
				"Failed to delete knowledge base: %v, knowledge base ID: %s",
				err, knowledgeBaseID,
			)
		}
	}()

	// Initialize parallel evaluation metrics
	var finished int
	var mu sync.Mutex
	var g errgroup.Group
	metricHook := NewHookMetric(len(dataset))

	workerCount := evaluationWorkerCount()
	g.SetLimit(workerCount)
	logger.Infof(ctx, "Starting evaluation with %d parallel workers", workerCount)

	// Process each QA pair in parallel
	for i, qaPair := range dataset {
		qaPair := qaPair
		i := i
		g.Go(func() error {
			itemStart := time.Now()
			logger.Infof(ctx, "Processing QA pair %d, question: %s", i, qaPair.Question)

			// Prepare chat management parameters for this QA pair
			chatManage := detail.Params.Clone()
			chatManage.Query = qaPair.Question
			chatManage.RewriteQuery = qaPair.Question
			// Set knowledge base ID and search targets for this evaluation
			chatManage.KnowledgeBaseIDs = []string{knowledgeBaseID}
			chatManage.SearchTargets = types.SearchTargets{
				&types.SearchTarget{
					Type:            types.SearchTargetTypeKnowledgeBase,
					KnowledgeBaseID: knowledgeBaseID,
				},
			}

			// Execute knowledge QA pipeline
			logger.Infof(ctx, "Running knowledge QA for question: %s", qaPair.Question)
			err = e.sessionService.KnowledgeQAByEvent(ctx, chatManage, types.Pipline["rag"])
			if err != nil {
				logger.Errorf(ctx, "Failed to process question %d: %v", i, err)
				return err
			}

			// The negative-control switch is deliberately limited to evaluation
			// runs. It removes genuinely relevant retrieved passages before the
			// normal metric calculation, so regression evidence is produced by
			// real result filtering rather than by editing metric numbers.
			searchResults := chatManage.SearchResult
			rerankResults := chatManage.RerankResult
			if evaluationDropRelevantEnabled() {
				searchResults = filterRelevantEvaluationResults(qaPair, searchResults)
				rerankResults = filterRelevantEvaluationResults(qaPair, rerankResults)
			}

			// Record evaluation metrics
			logger.Infof(ctx, "Recording metrics for QA pair %d", i)
			metricHook.recordInit(i)
			metricHook.recordQaPair(i, qaPair)
			metricHook.recordSearchResult(i, searchResults)
			metricHook.recordRerankResult(i, rerankResults)
			metricHook.recordChatResponse(i, chatManage.ChatResponse)
			metricHook.recordFinish(i)
			itemHook := NewHookMetric(1)
			itemHook.recordInit(0)
			itemHook.recordQaPair(0, qaPair)
			itemHook.recordSearchResult(0, searchResults)
			itemHook.recordRerankResult(0, rerankResults)
			itemHook.recordChatResponse(0, chatManage.ChatResponse)
			itemHook.recordFinish(0)
			generated := ""
			if chatManage.ChatResponse != nil {
				generated = chatManage.ChatResponse.Content
			}
			item := &types.EvaluationItem{
				Index: i, QuestionID: qaPair.QID, Question: qaPair.Question,
				ReferenceAnswer: qaPair.Answer, GeneratedAnswer: generated,
				SearchResults: searchResults, RerankResults: rerankResults,
				Metric: itemHook.MetricResult(), DurationMS: time.Since(itemStart).Milliseconds(),
			}

			// Update progress metrics
			mu.Lock()
			finished += 1
			metricResult := metricHook.MetricResult()
			mu.Unlock()
			if err := e.persistUpdate(ctx, detail.Task.ID, func(params *types.EvaluationDetail) {
				params.Metric = metricResult
				params.Task.Finished = finished
				params.Items = append(params.Items, item)
				logger.Infof(ctx, "Updated task progress: %d/%d completed", finished, params.Task.Total)
			}); err != nil {
				return err
			}
			return nil
		})
	}

	// Wait for all parallel evaluations to complete
	logger.Info(ctx, "Waiting for all evaluation tasks to complete")
	if err := g.Wait(); err != nil {
		logger.Errorf(ctx, "Evaluation error: %v", err)
		return err
	}

	// Final update of evaluation metrics
	if err := e.persistUpdate(ctx, detail.Task.ID, func(params *types.EvaluationDetail) {
		params.Metric = metricHook.MetricResult()
		params.Task.Finished = finished
	}); err != nil {
		return err
	}

	logger.Infof(ctx, "Dataset evaluation completed successfully, task ID: %s", detail.Task.ID)
	return nil
}

func envOrUnknown(name string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return "unknown"
}

func evaluationWorkerCount() int {
	value, err := strconv.Atoi(os.Getenv("TOPIC3_EVAL_CONCURRENCY"))
	if err != nil || value < 1 {
		return 1
	}
	return value
}

func evaluationDropRelevantEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("TOPIC3_EVAL_DROP_RELEVANT")), "true")
}

func filterRelevantEvaluationResults(qaPair *types.QAPair, results []*types.SearchResult) []*types.SearchResult {
	if qaPair == nil || len(results) == 0 {
		return results
	}
	filtered := make([]*types.SearchResult, 0, len(results))
	for _, result := range results {
		if result == nil || result.Content == "" {
			filtered = append(filtered, result)
			continue
		}
		relevant := false
		for _, passage := range qaPair.Passages {
			if passage != "" && (strings.Contains(passage, result.Content) || strings.Contains(result.Content, passage)) {
				relevant = true
				break
			}
		}
		if !relevant {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

// getPassageList extracts and organizes passages from QA pairs
// Returns a slice of passages indexed by their passage IDs
func getPassageList(dataset []*types.QAPair) []string {
	pIDMap := make(map[int]string)
	maxPID := 0
	for _, qaPair := range dataset {
		for i := 0; i < len(qaPair.PIDs); i++ {
			pIDMap[qaPair.PIDs[i]] = qaPair.Passages[i]
			maxPID = max(maxPID, qaPair.PIDs[i])
		}
	}
	passages := make([]string, maxPID+1)
	for i := 0; i <= maxPID; i++ {
		if _, ok := pIDMap[i]; ok {
			passages[i] = pIDMap[i]
		}
	}
	return passages
}
