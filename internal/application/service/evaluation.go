package service

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

/*
corpus: pid -> content
queries: qid -> content
answers: aid -> content
qrels: qid -> pid
arels: qid -> aid
*/

// EvaluationService 지식베이스 및 채팅 모델 평가 작업 처리
type EvaluationService struct {
	config               *config.Config                  // 애플리케이션 구성
	dataset              interfaces.DatasetService       // 데이터셋 작업 서비스
	knowledgeBaseService interfaces.KnowledgeBaseService // 지식베이스 작업 서비스
	knowledgeService     interfaces.KnowledgeService     // 지식 작업 서비스
	sessionService       interfaces.SessionService       // 채팅 세션 서비스
	modelService         interfaces.ModelService         // 모델 작업 서비스

	evaluationMemoryStorage *evaluationMemoryStorage // 평가 작업을 위한 인메모리 저장소
}

func NewEvaluationService(
	config *config.Config,
	dataset interfaces.DatasetService,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	sessionService interfaces.SessionService,
	modelService interfaces.ModelService,
) interfaces.EvaluationService {
	evaluationMemoryStorage := newEvaluationMemoryStorage()
	return &EvaluationService{
		config:                  config,
		dataset:                 dataset,
		knowledgeBaseService:    knowledgeBaseService,
		knowledgeService:        knowledgeService,
		sessionService:          sessionService,
		modelService:            modelService,
		evaluationMemoryStorage: evaluationMemoryStorage,
	}
}

// evaluationMemoryStorage 평가 작업을 메모리에 저장하고 스레드로부터 안전한 액세스 제공
type evaluationMemoryStorage struct {
	store map[string]*types.EvaluationDetail // 작업 ID와 평가 세부 정보 매핑
	mu    *sync.RWMutex                      // 동시 액세스를 위한 읽기-쓰기 잠금
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

	detail, err := e.evaluationMemoryStorage.get(taskID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get evaluation task: %v", err)
		return nil, err
	}

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
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

// Evaluation 지정된 매개변수로 새로운 평가 작업 시작
// datasetID: 평가할 데이터셋 ID
// knowledgeBaseID: 사용할 지식베이스 ID (비어 있으면 새로 생성)
// chatModelID: 평가할 채팅 모델 ID
// rerankModelID: 평가할 재순위 모델 ID
func (e *EvaluationService) Evaluation(ctx context.Context,
	datasetID string, knowledgeBaseID string, chatModelID string, rerankModelID string,
) (*types.EvaluationDetail, error) {
	logger.Info(ctx, "Start evaluation")
	logger.Infof(ctx, "Dataset ID: %s, Knowledge Base ID: %s, Chat Model ID: %s, Rerank Model ID: %s",
		datasetID, knowledgeBaseID, chatModelID, rerankModelID)

	// 멀티 테넌트 지원을 위해 컨텍스트에서 테넌트 ID 가져오기
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	logger.Infof(ctx, "Tenant ID: %d", tenantID)

	// 지식베이스 ID가 제공되지 않은 경우 생성 처리
	if knowledgeBaseID == "" {
		logger.Info(ctx, "No knowledge base ID provided, creating new knowledge base")
		// 기본 평가 설정으로 새 지식베이스 생성
		// 기본 임베딩 모델과 LLM 모델 가져오기
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
		// 기존 지식베이스를 기반으로 평가 전용 지식베이스 생성
		kb, err := e.knowledgeBaseService.GetKnowledgeBaseByID(ctx, knowledgeBaseID)
		if err != nil {
			logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
			return nil, err
		}

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

	// 선택적 매개변수의 기본값 설정
	if datasetID == "" {
		datasetID = "default"
		logger.Info(ctx, "Using default dataset")
	}

	if rerankModelID == "" {
		// 기본 재순위 모델 가져오기
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
		// 기본 LLM 모델 가져오기
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

	// 고유 ID로 평가 작업 생성
	logger.Info(ctx, "Creating evaluation task")
	taskID := uuid.New().String()
	logger.Infof(ctx, "Generated task ID: %s", taskID)

	// 모든 매개변수를 포함한 평가 세부 정보 준비
	detail := &types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID:        taskID,
			TenantID:  tenantID,
			DatasetID: datasetID,
			Status:    types.EvaluationStatuePending,
			StartTime: time.Now(),
		},
		Params: &types.ChatManage{
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
	}

	// 메모리 저장소에 평가 작업 저장
	logger.Info(ctx, "Registering evaluation task")
	e.evaluationMemoryStorage.register(detail)

	// 백그라운드 고루틴에서 평가 시작
	logger.Info(ctx, "Starting evaluation in background")
	go func() {
		// 백그라운드 작업을 위한 로거가 포함된 새 컨텍스트 생성
		newCtx := logger.CloneContext(ctx)
		logger.Infof(newCtx, "Background evaluation started for task ID: %s", taskID)

		// 작업 상태를 실행 중으로 업데이트
		detail.Task.Status = types.EvaluationStatueRunning
		logger.Info(newCtx, "Evaluation task status set to running")

		// 실제 평가 실행
		if err := e.EvalDataset(newCtx, detail, knowledgeBaseID); err != nil {
			detail.Task.Status = types.EvaluationStatueFailed
			detail.Task.ErrMsg = err.Error()
			logger.Errorf(newCtx, "Evaluation task failed: %v, task ID: %s", err, taskID)
			return
		}

		// 작업을 성공적으로 완료된 것으로 표시
		logger.Infof(newCtx, "Evaluation task completed successfully, task ID: %s", taskID)
		detail.Task.Status = types.EvaluationStatueSuccess
	}()

	logger.Infof(ctx, "Evaluation task created successfully, task ID: %s", taskID)
	return detail, nil
}

// EvalDataset 실제 데이터셋 평가 수행
// 각 QA 쌍을 병렬로 처리하고 메트릭 기록
func (e *EvaluationService) EvalDataset(ctx context.Context, detail *types.EvaluationDetail, knowledgeBaseID string) error {
	logger.Info(ctx, "Start evaluating dataset")
	logger.Infof(ctx, "Task ID: %s, Dataset ID: %s", detail.Task.ID, detail.Task.DatasetID)

	// 저장소에서 데이터셋 검색
	dataset, err := e.dataset.GetDatasetByID(ctx, detail.Task.DatasetID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get dataset: %v", err)
		return err
	}
	logger.Infof(ctx, "Dataset retrieved successfully with %d QA pairs", len(dataset))

	// 작업 세부 정보의 총 QA 쌍 수 업데이트
	e.evaluationMemoryStorage.update(detail.Task.ID, func(params *types.EvaluationDetail) {
		params.Task.Total = len(dataset)
		logger.Infof(ctx, "Updated task total to %d QA pairs", params.Task.Total)
	})

	// 데이터셋에서 패시지 추출 및 정리
	passages := getPassageList(dataset)
	logger.Infof(ctx, "Creating knowledge from %d passages", len(passages))

	// 패시지에서 지식베이스 생성
	knowledge, err := e.knowledgeService.CreateKnowledgeFromPassage(ctx, knowledgeBaseID, passages)
	if err != nil {
		logger.Errorf(ctx, "Failed to create knowledge from passages: %v", err)
		return err
	}
	logger.Infof(ctx, "Knowledge created successfully, ID: %s", knowledge.ID)

	// 임시 리소스 정리 설정
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

	// 병렬 평가 메트릭 초기화
	var finished int
	var mu sync.Mutex
	var g errgroup.Group
	metricHook := NewHookMetric(len(dataset))

	// 사용 가능한 CPU에 따라 워커 제한 설정
	g.SetLimit(max(runtime.GOMAXPROCS(0)-1, 1))
	logger.Infof(ctx, "Starting evaluation with %d parallel workers", max(runtime.GOMAXPROCS(0)-1, 1))

	// 각 QA 쌍을 병렬로 처리
	for i, qaPair := range dataset {
		qaPair := qaPair
		i := i
		g.Go(func() error {
			logger.Infof(ctx, "Processing QA pair %d, question: %s", i, qaPair.Question)

			// 이 QA 쌍에 대한 채팅 관리 매개변수 준비
			chatManage := detail.Params.Clone()
			chatManage.Query = qaPair.Question
			chatManage.RewriteQuery = qaPair.Question
			// 이 평가를 위한 지식베이스 ID 및 검색 대상 설정
			chatManage.KnowledgeBaseIDs = []string{knowledgeBaseID}
			chatManage.SearchTargets = types.SearchTargets{
				&types.SearchTarget{
					Type:            types.SearchTargetTypeKnowledgeBase,
					KnowledgeBaseID: knowledgeBaseID,
				},
			}

			// 지식 QA 파이프라인 실행
			logger.Infof(ctx, "Running knowledge QA for question: %s", qaPair.Question)
			err = e.sessionService.KnowledgeQAByEvent(ctx, chatManage, types.Pipline["rag"])
			if err != nil {
				logger.Errorf(ctx, "Failed to process question %d: %v", i, err)
				return err
			}

			// 평가 메트릭 기록
			logger.Infof(ctx, "Recording metrics for QA pair %d", i)
			metricHook.recordInit(i)
			metricHook.recordQaPair(i, qaPair)
			metricHook.recordSearchResult(i, chatManage.SearchResult)
			metricHook.recordRerankResult(i, chatManage.RerankResult)
			metricHook.recordChatResponse(i, chatManage.ChatResponse)
			metricHook.recordFinish(i)

			// 진행 상황 메트릭 업데이트
			mu.Lock()
			finished += 1
			metricResult := metricHook.MetricResult()
			mu.Unlock()
			e.evaluationMemoryStorage.update(detail.Task.ID, func(params *types.EvaluationDetail) {
				params.Metric = metricResult
				params.Task.Finished = finished
				logger.Infof(ctx, "Updated task progress: %d/%d completed", finished, params.Task.Total)
			})
			return nil
		})
	}

	// 모든 병렬 평가가 완료될 때까지 대기
	logger.Info(ctx, "Waiting for all evaluation tasks to complete")
	if err := g.Wait(); err != nil {
		logger.Errorf(ctx, "Evaluation error: %v", err)
		return err
	}

	// 평가 메트릭 최종 업데이트
	e.evaluationMemoryStorage.update(detail.Task.ID, func(params *types.EvaluationDetail) {
		params.Metric = metricHook.MetricResult()
		params.Task.Finished = finished
	})

	logger.Infof(ctx, "Dataset evaluation completed successfully, task ID: %s", detail.Task.ID)
	return nil
}

// getPassageList QA 쌍에서 패시지를 추출하고 정리
// 패시지 ID로 인덱싱된 패시지 슬라이스 반환
func getPassageList(dataset []*types.QAPair) []string {
	pIDMap := make(map[int]string)
	maxPID := 0
	for _, qaPair := range dataset {
		for i := 0; i < len(qaPair.PIDs); i++ {
			pIDMap[qaPair.PIDs[i]] = qaPair.Passages[i]
			maxPID = max(maxPID, qaPair.PIDs[i])
		}
	}
	passages := make([]string, maxPID)
	for i := 0; i < maxPID; i++ {
		if _, ok := pIDMap[i]; ok {
			passages[i] = pIDMap[i]
		}
	}
	return passages
}
