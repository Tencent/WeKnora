package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
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

	evaluationStorage *evaluationStorage // Persistent storage for evaluation tasks
}

func NewEvaluationService(
	config *config.Config,
	dataset interfaces.DatasetService,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	sessionService interfaces.SessionService,
	modelService interfaces.ModelService,
	db *gorm.DB,
) interfaces.EvaluationService {
	storage := newEvaluationStorage(db)
	if recovered, err := storage.reconcileInterrupted(context.Background(), time.Now().UTC()); err != nil {
		logger.Errorf(context.Background(), "Failed to reconcile interrupted evaluations: %v", err)
	} else if recovered > 0 {
		logger.Infof(context.Background(), "Marked %d interrupted evaluations as failed", recovered)
	}
	return &EvaluationService{
		config:               config,
		dataset:              dataset,
		knowledgeBaseService: knowledgeBaseService,
		knowledgeService:     knowledgeService,
		sessionService:       sessionService,
		modelService:         modelService,
		evaluationStorage:    storage,
	}
}

// evaluationRecord is the database representation of an evaluation run. Params
// and metrics are stored as JSON so a run remains reproducible even when the
// application's default model or retrieval settings change later.
type evaluationRecord struct {
	ID         string                 `gorm:"primaryKey;size:255"`
	TenantID   uint64                 `gorm:"index;not null"`
	DatasetID  string                 `gorm:"size:255;not null"`
	Status     types.EvaluationStatue `gorm:"not null"`
	ErrMsg     string                 `gorm:"type:text"`
	Total      int
	Finished   int
	Params     json.RawMessage `gorm:"type:jsonb;not null"`
	RunConfig  json.RawMessage `gorm:"type:jsonb;not null"`
	Metric     json.RawMessage `gorm:"type:jsonb"`
	Usage      json.RawMessage `gorm:"type:jsonb;not null"`
	StartedAt  time.Time       `gorm:"not null"`
	FinishedAt *time.Time
	DurationMS int64 `gorm:"not null;default:0"`
}

func (evaluationRecord) TableName() string {
	return "evaluation_tasks"
}

// evaluationStorage serializes updates made by the parallel evaluation workers
// and persists every state transition to the database.
type evaluationStorage struct {
	db             *gorm.DB
	fingerprintKey []byte
	mu             sync.Mutex
}

func newEvaluationStorage(db *gorm.DB) *evaluationStorage {
	return &evaluationStorage{
		db:             db,
		fingerprintKey: []byte(os.Getenv("WEKNORA_MODEL_CALL_FINGERPRINT_KEY")),
	}
}

func (e *evaluationStorage) register(ctx context.Context, detail *types.EvaluationDetail) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	record, err := evaluationDetailToRecord(detail)
	if err != nil {
		return err
	}
	return e.db.WithContext(ctx).Create(record).Error
}

func (e *evaluationStorage) get(ctx context.Context, taskID string) (*types.EvaluationDetail, error) {
	detail, err := e.getStoredDetail(ctx, taskID)
	if err != nil {
		return nil, err
	}
	calls, usage, err := e.getModelCalls(ctx, taskID)
	if err != nil {
		return nil, err
	}
	detail.ModelCalls = calls
	// Call-level telemetry is intentionally retained for a limited period. The
	// immutable aggregate stored on the task keeps old evaluation reports useful
	// after those privacy-sensitive detail rows expire.
	if len(calls) > 0 || detail.Usage == nil {
		detail.Usage = usage
	}
	return detail, nil
}

func (e *evaluationStorage) getStoredDetail(ctx context.Context, taskID string) (*types.EvaluationDetail, error) {
	var record evaluationRecord
	if err := e.db.WithContext(ctx).Where("id = ?", taskID).First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("task not found")
		}
		return nil, err
	}
	return evaluationRecordToDetail(&record)
}

func (e *evaluationStorage) list(
	ctx context.Context, tenantID uint64, limit, offset int,
) (*types.EvaluationRunPage, error) {
	query := e.db.WithContext(ctx).Model(&evaluationRecord{}).Where("tenant_id = ?", tenantID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	var records []evaluationRecord
	if err := query.Order("started_at DESC, id DESC").Limit(limit).Offset(offset).Find(&records).Error; err != nil {
		return nil, err
	}
	page := &types.EvaluationRunPage{
		Items: make([]types.EvaluationRunSummary, 0, len(records)),
		Total: total, Limit: limit, Offset: offset,
	}
	for i := range records {
		detail, err := evaluationRecordToDetail(&records[i])
		if err != nil {
			return nil, fmt.Errorf("decode evaluation run %s: %w", records[i].ID, err)
		}
		metric := detail.Metric
		if metric != nil && len(metric.Samples) > 0 {
			copy := *metric
			copy.Samples = nil
			metric = &copy
		}
		page.Items = append(page.Items, types.EvaluationRunSummary{
			Task: detail.Task, RunConfig: detail.RunConfig, Metric: metric, Usage: detail.Usage,
		})
	}
	return page, nil
}

func (e *evaluationStorage) update(
	ctx context.Context,
	taskID string,
	fn func(detail *types.EvaluationDetail),
) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	detail, err := e.getStoredDetail(ctx, taskID)
	if err != nil {
		return err
	}
	fn(detail)
	if detail.Task.Status == types.EvaluationStatueSuccess || detail.Task.Status == types.EvaluationStatueFailed {
		_, usage, usageErr := e.getModelCalls(ctx, taskID)
		if usageErr != nil {
			return fmt.Errorf("aggregate terminal evaluation usage: %w", usageErr)
		}
		if usage.CallCount > 0 || detail.Usage == nil {
			detail.Usage = usage
		}
	}
	record, err := evaluationDetailToRecord(detail)
	if err != nil {
		return err
	}
	return e.db.WithContext(ctx).Save(record).Error
}

const evaluationInterruptedMessage = "evaluation interrupted by application restart"

// reconcileInterrupted closes durable tasks whose in-process worker was lost
// when the previous server stopped. Evaluation workers are not replayable, so
// reporting an explicit failure is safer than leaving an endless pending row.
func (e *evaluationStorage) reconcileInterrupted(ctx context.Context, finishedAt time.Time) (int64, error) {
	if e == nil || e.db == nil {
		return 0, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	var records []evaluationRecord
	if err := e.db.WithContext(ctx).
		Where("status IN ?", []types.EvaluationStatue{
			types.EvaluationStatuePending, types.EvaluationStatueRunning,
		}).Find(&records).Error; err != nil {
		return 0, err
	}
	for i := range records {
		durationMS := finishedAt.Sub(records[i].StartedAt).Milliseconds()
		if durationMS < 0 {
			durationMS = 0
		}
		if err := e.db.WithContext(ctx).Model(&evaluationRecord{}).
			Where("id = ? AND status IN ?", records[i].ID, []types.EvaluationStatue{
				types.EvaluationStatuePending, types.EvaluationStatueRunning,
			}).Updates(map[string]interface{}{
			"status": types.EvaluationStatueFailed, "err_msg": evaluationInterruptedMessage,
			"finished_at": finishedAt, "duration_ms": durationMS,
		}).Error; err != nil {
			return int64(i), err
		}
	}
	return int64(len(records)), nil
}

func evaluationDetailToRecord(detail *types.EvaluationDetail) (*evaluationRecord, error) {
	params, err := json.Marshal(detail.Params)
	if err != nil {
		return nil, fmt.Errorf("marshal evaluation params: %w", err)
	}
	runConfig, err := json.Marshal(detail.RunConfig)
	if err != nil {
		return nil, fmt.Errorf("marshal evaluation run config: %w", err)
	}
	metric, err := json.Marshal(detail.Metric)
	if err != nil {
		return nil, fmt.Errorf("marshal evaluation metric: %w", err)
	}
	usage, err := json.Marshal(detail.Usage)
	if err != nil {
		return nil, fmt.Errorf("marshal evaluation usage: %w", err)
	}
	return &evaluationRecord{
		ID: detail.Task.ID, TenantID: detail.Task.TenantID, DatasetID: detail.Task.DatasetID,
		Status: detail.Task.Status, ErrMsg: detail.Task.ErrMsg, Total: detail.Task.Total,
		Finished: detail.Task.Finished, Params: params, RunConfig: runConfig, Metric: metric, Usage: usage,
		StartedAt: detail.Task.StartTime, FinishedAt: detail.Task.EndTime,
		DurationMS: detail.Task.DurationMS,
	}, nil
}

func evaluationRecordToDetail(record *evaluationRecord) (*types.EvaluationDetail, error) {
	var params types.ChatManage
	if err := json.Unmarshal(record.Params, &params); err != nil {
		return nil, fmt.Errorf("unmarshal evaluation params: %w", err)
	}
	var metric *types.MetricResult
	if len(record.Metric) > 0 && string(record.Metric) != "null" {
		metric = &types.MetricResult{}
		if err := json.Unmarshal(record.Metric, metric); err != nil {
			return nil, fmt.Errorf("unmarshal evaluation metric: %w", err)
		}
	}
	var runConfig *types.EvaluationRunConfig
	if len(record.RunConfig) > 0 && string(record.RunConfig) != "null" && string(record.RunConfig) != "{}" {
		runConfig = &types.EvaluationRunConfig{}
		if err := json.Unmarshal(record.RunConfig, runConfig); err != nil {
			return nil, fmt.Errorf("unmarshal evaluation run config: %w", err)
		}
	}
	var usage *types.EvaluationUsage
	if len(record.Usage) > 0 && string(record.Usage) != "null" && string(record.Usage) != "{}" {
		usage = &types.EvaluationUsage{}
		if err := json.Unmarshal(record.Usage, usage); err != nil {
			return nil, fmt.Errorf("unmarshal evaluation usage: %w", err)
		}
	}
	return &types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID: record.ID, TenantID: record.TenantID, DatasetID: record.DatasetID,
			StartTime: record.StartedAt, EndTime: record.FinishedAt,
			DurationMS: record.DurationMS, Status: record.Status, ErrMsg: record.ErrMsg,
			Total: record.Total, Finished: record.Finished,
		},
		Params:    &params,
		RunConfig: runConfig,
		Metric:    metric,
		Usage:     usage,
	}, nil
}

func (e *EvaluationService) EvaluationResult(ctx context.Context, taskID string) (*types.EvaluationDetail, error) {
	logger.Info(ctx, "Start getting evaluation result")
	logger.Infof(ctx, "Task ID: %s", taskID)

	detail, err := e.evaluationStorage.get(ctx, taskID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get evaluation task: %v", err)
		return nil, err
	}

	tenantID := types.MustTenantIDFromContext(ctx)
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

// EvaluationEvidence returns a portable, deterministic and tenant-scoped proof
// bundle. It intentionally excludes ChatManage because that structure contains
// prompt templates and request text.
func (e *EvaluationService) EvaluationEvidence(
	ctx context.Context, taskID string,
) (*types.EvaluationEvidenceReport, error) {
	detail, err := e.EvaluationResult(ctx, taskID)
	if err != nil {
		return nil, err
	}
	report := buildEvaluationEvidenceReport(detail)
	if err := sealEvaluationEvidenceReport(report); err != nil {
		return nil, err
	}
	return report, nil
}

func sealEvaluationEvidenceReport(report *types.EvaluationEvidenceReport) error {
	report.ReportSHA256 = ""
	encoded, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encode evaluation evidence: %w", err)
	}
	report.ReportSHA256 = fmt.Sprintf("sha256:%x", sha256.Sum256(encoded))
	return nil
}

func buildEvaluationEvidenceReport(detail *types.EvaluationDetail) *types.EvaluationEvidenceReport {
	report := &types.EvaluationEvidenceReport{
		SchemaVersion: 1, Task: detail.Task, RunConfig: detail.RunConfig,
		Metric: detail.Metric, Usage: detail.Usage,
		ModelCalls: make([]types.EvaluationEvidenceCall, 0, len(detail.ModelCalls)),
	}
	for _, call := range detail.ModelCalls {
		report.ModelCalls = append(report.ModelCalls, types.EvaluationEvidenceCall{
			ID: call.ID, ModelID: call.ModelID, ModelName: call.ModelName,
			ModelType: call.ModelType, Purpose: call.Purpose,
			PromptPrefixFingerprint: call.PromptPrefixFingerprint,
			RequestFingerprint:      call.RequestFingerprint,
			Usage:                   call.Usage, Pricing: call.Pricing, EstimatedCost: call.EstimatedCost,
			DurationMS: call.DurationMS, Success: call.Success, CreatedAt: call.CreatedAt,
		})
	}
	sort.Slice(report.ModelCalls, func(i, j int) bool {
		if report.ModelCalls[i].CreatedAt.Equal(report.ModelCalls[j].CreatedAt) {
			return report.ModelCalls[i].ID < report.ModelCalls[j].ID
		}
		return report.ModelCalls[i].CreatedAt.Before(report.ModelCalls[j].CreatedAt)
	})
	if detail.RunConfig == nil || detail.RunConfig.CodeVersion == "" || detail.RunConfig.CodeVersion == "unknown" {
		report.Warnings = append(report.Warnings, "code_version_unknown")
	} else if strings.HasSuffix(detail.RunConfig.CodeVersion, "-dirty") {
		report.Warnings = append(report.Warnings, "code_version_dirty")
	}
	if detail.Metric == nil || len(detail.Metric.Samples) == 0 {
		report.Warnings = append(report.Warnings, "per_sample_evidence_unavailable")
	}
	return report
}

// ModelUsage returns model-level evaluation usage for the current tenant and
// optional inclusive time interval.
func (e *EvaluationService) ModelUsage(
	ctx context.Context, startTime, endTime *time.Time,
) ([]types.ModelUsageStat, error) {
	return e.evaluationStorage.modelUsage(ctx, types.MustTenantIDFromContext(ctx), startTime, endTime)
}

// EvaluationDatasets returns the manifest-backed dataset catalog used by the
// evaluation UI. It performs no model calls and is safe for Viewer access.
func (e *EvaluationService) EvaluationDatasets(ctx context.Context) ([]types.EvaluationDataset, error) {
	return e.dataset.ListDatasets(ctx)
}

// EvaluationRuns returns newest-first history for the current tenant.
func (e *EvaluationService) EvaluationRuns(
	ctx context.Context, limit, offset int,
) (*types.EvaluationRunPage, error) {
	return e.evaluationStorage.list(ctx, types.MustTenantIDFromContext(ctx), limit, offset)
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
	if datasetID == "" {
		datasetID = "default"
		logger.Info(ctx, "Using default dataset")
	}
	dataset, err := e.dataset.GetDatasetByID(ctx, datasetID)
	if err != nil {
		return nil, fmt.Errorf("load evaluation dataset: %w", err)
	}
	datasetFingerprint, err := fingerprintEvaluationDataset(dataset)
	if err != nil {
		return nil, err
	}
	sourceKnowledgeBaseID := knowledgeBaseID
	var evaluationKB *types.KnowledgeBase

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
		evaluationKB = kb
		logger.Infof(ctx, "Created new knowledge base with ID: %s", knowledgeBaseID)
	} else {
		logger.Infof(ctx, "Using existing knowledge base ID: %s", knowledgeBaseID)
		// Create evaluation-specific knowledge base based on existing one
		sourceKB, err := e.knowledgeBaseService.GetKnowledgeBaseByID(ctx, knowledgeBaseID)
		if err != nil {
			logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
			return nil, err
		}

		kb, err := e.knowledgeBaseService.CreateKnowledgeBase(ctx, &types.KnowledgeBase{
			Name:             "evaluation",
			Description:      "evaluation",
			EmbeddingModelID: sourceKB.EmbeddingModelID,
			SummaryModelID:   sourceKB.SummaryModelID,
			ChunkingConfig:   sourceKB.ChunkingConfig,
		})
		if err != nil {
			logger.Errorf(ctx, "Failed to create knowledge base: %v", err)
			return nil, err
		}
		knowledgeBaseID = kb.ID
		evaluationKB = kb
		logger.Infof(ctx, "Created new knowledge base with ID: %s based on existing one", knowledgeBaseID)
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
	}
	runConfig, err := e.buildEvaluationRunConfig(
		ctx, datasetID, datasetFingerprint, len(dataset), sourceKnowledgeBaseID,
		evaluationKB, detail.Params.PipelineRequest, chatModelID, rerankModelID,
	)
	if err != nil {
		return nil, err
	}
	detail.RunConfig = runConfig

	// Persist the evaluation task before starting background work.
	logger.Info(ctx, "Registering evaluation task")
	if err := e.evaluationStorage.register(ctx, detail); err != nil {
		return nil, fmt.Errorf("register evaluation task: %w", err)
	}

	// Start evaluation in background goroutine
	logger.Info(ctx, "Starting evaluation in background")
	go func() {
		// Create new context with logger for background task
		newCtx := logger.CloneContext(ctx)
		newCtx = types.WithLLMCallObserver(newCtx, &evaluationCallObserver{
			ctx: context.WithoutCancel(newCtx), storage: e.evaluationStorage,
			taskID: taskID, tenantID: tenantID,
		})
		logger.Infof(newCtx, "Background evaluation started for task ID: %s", taskID)

		// Update task status to running
		if err := e.evaluationStorage.update(newCtx, taskID, func(current *types.EvaluationDetail) {
			current.Task.Status = types.EvaluationStatueRunning
		}); err != nil {
			logger.Errorf(newCtx, "Failed to persist running status: %v", err)
			return
		}
		logger.Info(newCtx, "Evaluation task status set to running")

		// Execute the evaluation and reject superficially successful runs that
		// never completed an answer-model call. A retrieval/provider failure can
		// otherwise fall through to a fallback response and produce zero metrics
		// while the task is misleadingly marked successful.
		evaluationErr := e.EvalDataset(newCtx, detail, knowledgeBaseID, dataset)
		if evaluationErr == nil {
			evaluationErr = e.validateSuccessfulEvaluationCalls(newCtx, taskID)
		}
		if evaluationErr != nil {
			finishedAt := time.Now()
			if updateErr := e.evaluationStorage.update(newCtx, taskID, func(current *types.EvaluationDetail) {
				current.Task.Status = types.EvaluationStatueFailed
				current.Task.ErrMsg = evaluationErr.Error()
				current.Task.EndTime = &finishedAt
				current.Task.DurationMS = finishedAt.Sub(current.Task.StartTime).Milliseconds()
			}); updateErr != nil {
				logger.Errorf(newCtx, "Failed to persist failed status: %v", updateErr)
			}
			logger.Errorf(newCtx, "Evaluation task failed: %v, task ID: %s", evaluationErr, taskID)
			return
		}

		// Mark task as completed successfully
		logger.Infof(newCtx, "Evaluation task completed successfully, task ID: %s", taskID)
		finishedAt := time.Now()
		if err := e.evaluationStorage.update(newCtx, taskID, func(current *types.EvaluationDetail) {
			current.Task.Status = types.EvaluationStatueSuccess
			current.Task.EndTime = &finishedAt
			current.Task.DurationMS = finishedAt.Sub(current.Task.StartTime).Milliseconds()
		}); err != nil {
			logger.Errorf(newCtx, "Failed to persist successful status: %v", err)
		}
	}()

	logger.Infof(ctx, "Evaluation task created successfully, task ID: %s", taskID)
	return detail, nil
}

func (e *EvaluationService) validateSuccessfulEvaluationCalls(ctx context.Context, taskID string) error {
	calls, _, err := e.evaluationStorage.getModelCalls(ctx, taskID)
	if err != nil {
		return fmt.Errorf("read evaluation model calls: %w", err)
	}
	if hasSuccessfulEvaluationChatCall(calls) {
		return nil
	}
	return errors.New("evaluation produced no successful chat model call")
}

func hasSuccessfulEvaluationChatCall(calls []types.EvaluationModelCall) bool {
	for _, call := range calls {
		if call.ModelType == types.ModelTypeKnowledgeQA && call.Success {
			return true
		}
	}
	return false
}

// EvalDataset performs the actual evaluation of a dataset
// Processes each QA pair in parallel and records metrics
func (e *EvaluationService) EvalDataset(
	ctx context.Context,
	detail *types.EvaluationDetail,
	knowledgeBaseID string,
	dataset []*types.QAPair,
) error {
	logger.Info(ctx, "Start evaluating dataset")
	logger.Infof(ctx, "Task ID: %s, Dataset ID: %s", detail.Task.ID, detail.Task.DatasetID)

	logger.Infof(ctx, "Dataset retrieved successfully with %d QA pairs", len(dataset))

	// Update total QA pairs count in task details
	if err := e.evaluationStorage.update(ctx, detail.Task.ID, func(params *types.EvaluationDetail) {
		params.Task.Total = len(dataset)
		logger.Infof(ctx, "Updated task total to %d QA pairs", params.Task.Total)
	}); err != nil {
		return fmt.Errorf("persist evaluation total: %w", err)
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
		if err := deleteReferencedKnowledge(ctx,
			e.knowledgeService,
			knowledgeBaseID,
			[]string{knowledge.ID}); err != nil {
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

	// Set worker limit based on available CPUs
	g.SetLimit(max(runtime.GOMAXPROCS(0)-1, 1))
	logger.Infof(ctx, "Starting evaluation with %d parallel workers", max(runtime.GOMAXPROCS(0)-1, 1))

	// Process each QA pair in parallel
	for i, qaPair := range dataset {
		qaPair := qaPair
		i := i
		g.Go(func() error {
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

			// Record evaluation metrics
			logger.Infof(ctx, "Recording metrics for QA pair %d", i)
			metricHook.recordInit(i)
			metricHook.recordQaPair(i, qaPair)
			metricHook.recordSearchResult(i, chatManage.SearchResult)
			metricHook.recordRerankResult(i, chatManage.RerankResult)
			metricHook.recordChatResponse(i, chatManage.ChatResponse)
			metricHook.recordFinish(i)

			// Update progress metrics
			mu.Lock()
			finished += 1
			metricResult := metricHook.MetricResult()
			mu.Unlock()
			if err := e.evaluationStorage.update(ctx, detail.Task.ID, func(params *types.EvaluationDetail) {
				params.Metric = metricResult
				params.Task.Finished = finished
				logger.Infof(ctx, "Updated task progress: %d/%d completed", finished, params.Task.Total)
			}); err != nil {
				return fmt.Errorf("persist evaluation progress: %w", err)
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
	if err := e.evaluationStorage.update(ctx, detail.Task.ID, func(params *types.EvaluationDetail) {
		params.Metric = metricHook.MetricResult()
		params.Task.Finished = finished
	}); err != nil {
		return fmt.Errorf("persist final evaluation metrics: %w", err)
	}

	logger.Infof(ctx, "Dataset evaluation completed successfully, task ID: %s", detail.Task.ID)
	return nil
}

func fingerprintEvaluationDataset(dataset []*types.QAPair) (string, error) {
	encoded, err := json.Marshal(dataset)
	if err != nil {
		return "", fmt.Errorf("fingerprint evaluation dataset: %w", err)
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(encoded)), nil
}

func (e *EvaluationService) buildEvaluationRunConfig(
	ctx context.Context,
	datasetID, datasetFingerprint string,
	datasetSamples int,
	sourceKnowledgeBaseID string,
	evaluationKB *types.KnowledgeBase,
	pipeline types.PipelineRequest,
	chatModelID, rerankModelID string,
) (*types.EvaluationRunConfig, error) {
	if evaluationKB == nil {
		return nil, errors.New("evaluation knowledge base was not created")
	}
	modelRoles := []struct{ role, id string }{
		{role: "embedding", id: evaluationKB.EmbeddingModelID},
		{role: "chat", id: chatModelID},
		{role: "rerank", id: rerankModelID},
	}
	models := make([]types.EvaluationModelSnapshot, 0, len(modelRoles))
	for _, item := range modelRoles {
		if item.id == "" {
			continue
		}
		model, err := e.modelService.GetModelByID(ctx, item.id)
		if err != nil {
			return nil, fmt.Errorf("snapshot %s model %q: %w", item.role, item.id, err)
		}
		models = append(models, snapshotEvaluationModel(item.role, model))
	}
	config := &types.EvaluationRunConfig{
		SchemaVersion: 2, DatasetID: datasetID, DatasetFingerprint: datasetFingerprint,
		DatasetSamples: datasetSamples, SourceKnowledgeBaseID: sourceKnowledgeBaseID,
		EvaluationKnowledgeBaseID: evaluationKB.ID, Chunking: evaluationKB.ChunkingConfig,
		Pipeline: snapshotEvaluationPipeline(pipeline), Models: models, CodeVersion: evaluationCodeVersion(),
	}
	config.ConfigFingerprint = fingerprintEvaluationConfig(config, false)
	config.ControlledFingerprint = fingerprintEvaluationConfig(config, true)
	return config, nil
}

func snapshotEvaluationPipeline(pipeline types.PipelineRequest) types.EvaluationPipelineSnapshot {
	summary := pipeline.SummaryConfig
	return types.EvaluationPipelineSnapshot{
		MaxRounds: pipeline.MaxRounds, VectorThreshold: pipeline.VectorThreshold,
		KeywordThreshold: pipeline.KeywordThreshold, EmbeddingTopK: pipeline.EmbeddingTopK,
		RerankModelID: pipeline.RerankModelID, RerankTopK: pipeline.RerankTopK,
		RerankThreshold: pipeline.RerankThreshold, ChatModelID: pipeline.ChatModelID,
		FallbackStrategy: pipeline.FallbackStrategy, CitationEnabled: pipeline.CitationEnabled,
		EnableRewrite: pipeline.EnableRewrite, EnableQueryExpansion: pipeline.EnableQueryExpansion,
		QueryUnderstandModelID: pipeline.QueryUnderstandModelID,
		Summary: types.EvaluationSummarySnapshot{
			MaxTokens: summary.MaxTokens, RepeatPenalty: summary.RepeatPenalty,
			TopK: summary.TopK, TopP: summary.TopP, FrequencyPenalty: summary.FrequencyPenalty,
			PresencePenalty: summary.PresencePenalty, Temperature: summary.Temperature,
			Seed: summary.Seed, MaxCompletionTokens: summary.MaxCompletionTokens,
			Thinking: summary.Thinking, PromptSHA256: sha256Text(summary.Prompt),
			ContextTemplateSHA256: sha256Text(summary.ContextTemplate),
			NoMatchPrefixSHA256:   sha256Text(summary.NoMatchPrefix),
		},
		FallbackResponseSHA256:    sha256Text(pipeline.FallbackResponse),
		FallbackPromptSHA256:      sha256Text(pipeline.FallbackPrompt),
		RewritePromptSystemSHA256: sha256Text(pipeline.RewritePromptSystem),
		RewritePromptUserSHA256:   sha256Text(pipeline.RewritePromptUser),
	}
}

func fingerprintEvaluationConfig(config *types.EvaluationRunConfig, controlled bool) string {
	pipeline := config.Pipeline
	models := append([]types.EvaluationModelSnapshot(nil), config.Models...)
	if controlled {
		pipeline.ChatModelID = ""
		models = models[:0]
		for _, model := range config.Models {
			if model.Role != "chat" {
				models = append(models, model)
			}
		}
	}
	payload := struct {
		SchemaVersion         int                              `json:"schema_version"`
		DatasetFingerprint    string                           `json:"dataset_fingerprint"`
		DatasetSamples        int                              `json:"dataset_samples"`
		SourceKnowledgeBaseID string                           `json:"source_knowledge_base_id,omitempty"`
		Chunking              types.ChunkingConfig             `json:"chunking"`
		Pipeline              types.EvaluationPipelineSnapshot `json:"pipeline"`
		Models                []types.EvaluationModelSnapshot  `json:"models"`
		CodeVersion           string                           `json:"code_version"`
	}{
		SchemaVersion: config.SchemaVersion, DatasetFingerprint: config.DatasetFingerprint,
		DatasetSamples: config.DatasetSamples, SourceKnowledgeBaseID: config.SourceKnowledgeBaseID,
		Chunking: config.Chunking, Pipeline: pipeline, Models: models, CodeVersion: config.CodeVersion,
	}
	encoded, _ := json.Marshal(payload)
	return fmt.Sprintf("sha256:%x", sha256.Sum256(encoded))
}

func snapshotEvaluationModel(role string, model *types.Model) types.EvaluationModelSnapshot {
	config := struct {
		Name                 string            `json:"name"`
		Type                 types.ModelType   `json:"type"`
		Source               types.ModelSource `json:"source"`
		Provider             string            `json:"provider"`
		Dimensions           int               `json:"dimensions"`
		TruncatePromptTokens int               `json:"truncate_prompt_tokens"`
		InterfaceType        string            `json:"interface_type"`
		BaseURL              string            `json:"base_url"`
		ParameterSize        string            `json:"parameter_size"`
		ExtraConfig          map[string]string `json:"extra_config,omitempty"`
		SupportsVision       bool              `json:"supports_vision"`
		ContextWindow        int               `json:"context_window"`
		MaxOutputTokens      int               `json:"max_output_tokens"`
		MaxConcurrency       int               `json:"max_concurrency"`
	}{
		Name: model.Name, Type: model.Type, Source: model.Source,
		Provider:             model.Parameters.Provider,
		Dimensions:           model.Parameters.EmbeddingParameters.Dimension,
		TruncatePromptTokens: model.Parameters.EmbeddingParameters.TruncatePromptTokens,
		InterfaceType:        model.Parameters.InterfaceType,
		BaseURL:              model.Parameters.BaseURL,
		ParameterSize:        model.Parameters.ParameterSize,
		ExtraConfig:          nonSecretEvaluationModelConfig(model.Parameters.ExtraConfig),
		SupportsVision:       model.Parameters.SupportsVision,
		ContextWindow:        model.Parameters.ContextWindow,
		MaxOutputTokens:      model.Parameters.MaxOutputTokens,
		MaxConcurrency:       model.Parameters.MaxConcurrency,
	}
	encoded, _ := json.Marshal(config)
	return types.EvaluationModelSnapshot{
		Role: role, ID: model.ID, Name: model.Name, DisplayName: model.DisplayName,
		Type: model.Type, Source: model.Source, Provider: model.Parameters.Provider,
		Dimensions:        model.Parameters.EmbeddingParameters.Dimension,
		ConfigFingerprint: fmt.Sprintf("sha256:%x", sha256.Sum256(encoded)), UpdatedAt: model.UpdatedAt,
	}
}

func nonSecretEvaluationModelConfig(config map[string]string) map[string]string {
	filtered := make(map[string]string, len(config))
	for key, value := range config {
		normalized := strings.ToLower(strings.TrimSpace(key))
		compact := strings.NewReplacer("_", "", "-", "", ".", "").Replace(normalized)
		if strings.Contains(normalized, "secret") || strings.Contains(normalized, "password") ||
			strings.Contains(normalized, "token") || strings.Contains(normalized, "credential") ||
			strings.Contains(compact, "apikey") || strings.Contains(normalized, "authorization") {
			continue
		}
		filtered[key] = value
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func evaluationCodeVersion() string {
	if revision := os.Getenv("WEKNORA_BUILD_COMMIT"); revision != "" {
		return revision
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		var revision string
		modified := false
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				revision = setting.Value
			}
			if setting.Key == "vcs.modified" && setting.Value == "true" {
				modified = true
			}
		}
		if revision != "" {
			if modified {
				return revision + "-dirty"
			}
			return revision
		}
	}
	return "unknown"
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
