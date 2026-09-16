package service

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/evaluation/metricregistry"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/modelobs"
	"github.com/Tencent/WeKnora/internal/models/call"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

const evaluationCleanupTimeout = 30 * time.Second

// evaluationTaskCanceledMessage is the stable terminal message for tasks
// canceled through a persistent user request.
const evaluationTaskCanceledMessage = "evaluation task canceled"

var (
	errEvaluationTaskTimeout = errors.New("evaluation task timeout")
	// errEvaluationTaskCancelRequested cancels a running worker after the
	// database recorded a persistent cancel request. It is not an ownership
	// loss: the owner still cleans resources and publishes Canceled.
	errEvaluationTaskCancelRequested = errors.New("evaluation task cancel requested")
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
	config                   *config.Config                  // Application configuration
	dataset                  interfaces.DatasetService       // Service for dataset operations
	knowledgeBaseService     interfaces.KnowledgeBaseService // Service for knowledge base operations
	knowledgeService         interfaces.KnowledgeService     // Service for knowledge operations
	sessionService           interfaces.SessionService       // Service for chat sessions
	modelService             interfaces.ModelService         // Service for model operations
	evaluationTaskRepository interfaces.EvaluationTaskRepository
	datasetRegistry          interfaces.EvaluationDatasetRegistryService
	questionResultRepository interfaces.EvaluationQuestionResultRepository
	metricRegistry           *metricregistry.Registry
	evaluationCostStore      modelobs.EvaluationCostStore
	ownerID                  string
	heartbeatInterval        time.Duration
	heartbeatTimeout         time.Duration
	runningLeaseDuration     time.Duration

	// runHandles maps "tenantID/taskID" to the cancel function of the task
	// running on this instance, so a cancel request persisted by any replica
	// can stop the local worker immediately.
	runHandles sync.Map
}

func evaluationRunHandleKey(tenantID uint64, taskID string) string {
	return fmt.Sprintf("%d/%s", tenantID, taskID)
}

func NewEvaluationService(
	config *config.Config,
	dataset interfaces.DatasetService,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	sessionService interfaces.SessionService,
	modelService interfaces.ModelService,
	evaluationTaskRepository interfaces.EvaluationTaskRepository,
	datasetRegistry interfaces.EvaluationDatasetRegistryService,
	questionResultRepository interfaces.EvaluationQuestionResultRepository,
) interfaces.EvaluationService {
	registry, err := metricregistry.NewDefaultRegistry()
	if err != nil {
		panic(err)
	}
	return newEvaluationService(
		config,
		dataset,
		knowledgeBaseService,
		knowledgeService,
		sessionService,
		modelService,
		evaluationTaskRepository,
		datasetRegistry,
		questionResultRepository,
		registry,
	)
}

// NewEvaluationServiceWithRegistry is the container constructor. The
// registry provider can fail startup before any task accepts work.
func NewEvaluationServiceWithRegistry(
	config *config.Config,
	dataset interfaces.DatasetService,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	sessionService interfaces.SessionService,
	modelService interfaces.ModelService,
	evaluationTaskRepository interfaces.EvaluationTaskRepository,
	datasetRegistry interfaces.EvaluationDatasetRegistryService,
	questionResultRepository interfaces.EvaluationQuestionResultRepository,
	registry *metricregistry.Registry,
	evaluationCostStore modelobs.EvaluationCostStore,
) interfaces.EvaluationService {
	return newEvaluationService(
		config,
		dataset,
		knowledgeBaseService,
		knowledgeService,
		sessionService,
		modelService,
		evaluationTaskRepository,
		datasetRegistry,
		questionResultRepository,
		registry,
		evaluationCostStore,
	)
}

func newEvaluationService(
	config *config.Config,
	dataset interfaces.DatasetService,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	sessionService interfaces.SessionService,
	modelService interfaces.ModelService,
	evaluationTaskRepository interfaces.EvaluationTaskRepository,
	datasetRegistry interfaces.EvaluationDatasetRegistryService,
	questionResultRepository interfaces.EvaluationQuestionResultRepository,
	registry *metricregistry.Registry,
	evaluationCostStore ...modelobs.EvaluationCostStore,
) interfaces.EvaluationService {
	var costStore modelobs.EvaluationCostStore
	if len(evaluationCostStore) > 0 {
		costStore = evaluationCostStore[0]
	}
	return &EvaluationService{
		config:                   config,
		dataset:                  dataset,
		knowledgeBaseService:     knowledgeBaseService,
		knowledgeService:         knowledgeService,
		sessionService:           sessionService,
		modelService:             modelService,
		evaluationTaskRepository: evaluationTaskRepository,
		datasetRegistry:          datasetRegistry,
		questionResultRepository: questionResultRepository,
		metricRegistry:           registry,
		evaluationCostStore:      costStore,
		ownerID:                  uuid.NewString(),
	}
}

// evaluationCleanupContext builds the bounded context for one cleanup call.
// The context always detaches from cancellation that already happened before
// entry, so a reached request or task deadline never blocks resource cleanup.
// Cancellation that happens after entry, such as heartbeat ownership loss,
// still propagates and interrupts the in-flight cleanup.
func evaluationCleanupContext(parent context.Context) (context.Context, context.CancelFunc) {
	cleanupCtx, cancel := context.WithTimeout(logger.CloneContext(parent), evaluationCleanupTimeout)
	if parent.Err() != nil {
		return cleanupCtx, cancel
	}
	stop := context.AfterFunc(parent, cancel)
	return cleanupCtx, func() {
		stop()
		cancel()
	}
}

func (e *EvaluationService) deleteEvaluationKnowledge(ctx context.Context, knowledgeBaseID, knowledgeID string) error {
	cleanupCtx, cancel := evaluationCleanupContext(ctx)
	defer cancel()
	// These bindings come from the evaluation-created knowledge, never user input.
	// The upstream delete planner verifies the persisted tenant and KB again.
	tenant, err := writeExecutionTenant(cleanupCtx)
	if err != nil {
		return err
	}
	if knowledgeBaseID == "" || knowledgeID == "" {
		return fmt.Errorf("evaluation cleanup requires exact resource bindings")
	}
	cleanupCtx = withKnowledgeCleanup(cleanupCtx, tenant, map[string]string{knowledgeID: knowledgeBaseID})
	return e.knowledgeService.DeleteKnowledge(cleanupCtx, knowledgeID)
}

func (e *EvaluationService) deleteEvaluationKnowledgeBase(ctx context.Context, knowledgeBaseID string) error {
	cleanupCtx, cancel := evaluationCleanupContext(ctx)
	defer cancel()
	return e.knowledgeBaseService.DeleteKnowledgeBase(cleanupCtx, knowledgeBaseID)
}

func (e *EvaluationService) cleanupEvaluationKnowledgeBase(ctx context.Context, knowledgeBaseID string) {
	logger.Infof(ctx, "Cleaning up evaluation knowledge base: %s", knowledgeBaseID)
	if err := e.deleteEvaluationKnowledgeBase(logger.CloneContext(ctx), knowledgeBaseID); err != nil {
		logger.Errorf(
			ctx,
			"Failed to delete evaluation knowledge base: %v, knowledge base ID: %s",
			err,
			knowledgeBaseID,
		)
	}
}

func appendEvaluationCleanupError(cleanupErrors *[]string, resourceType, resourceID string, err error) {
	if cleanupErrors == nil || err == nil {
		return
	}
	*cleanupErrors = append(*cleanupErrors, fmt.Sprintf("delete %s %s: %v", resourceType, resourceID, err))
}

func evaluationTaskDeadlineStopped(ctx context.Context, runErr error) bool {
	return errors.Is(context.Cause(ctx), errEvaluationTaskTimeout) &&
		(errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runErr, context.Canceled))
}

func captureEvaluationTaskDeadline(ctx context.Context, runErr error, stopped *bool) {
	if stopped != nil {
		*stopped = evaluationTaskDeadlineStopped(ctx, runErr)
	}
}

func (e *EvaluationService) runEvaluation(
	ctx context.Context,
	detail *types.EvaluationDetail,
	knowledgeBaseID string,
) error {
	if detail == nil || detail.Task == nil {
		return errors.New("run evaluation: task detail is required")
	}
	taskID := detail.Task.ID
	taskCtx, cancelTask := context.WithTimeoutCause(
		ctx,
		config.EvaluationTaskTimeout(e.config),
		errEvaluationTaskTimeout,
	)
	defer cancelTask()
	runCtx, cancelRun := context.WithCancelCause(types.WithModelAccountingState(taskCtx))
	defer cancelRun(nil)

	entity, err := e.evaluationTaskRepository.GetTask(runCtx, detail.Task.TenantID, taskID)
	if err != nil {
		return fmt.Errorf("load evaluation task before start: %w", err)
	}
	if entity.TemporaryKnowledgeBaseID == "" {
		return errors.New("run evaluation: persisted temporary knowledge base ID is required")
	}
	if knowledgeBaseID != "" && knowledgeBaseID != entity.TemporaryKnowledgeBaseID {
		logger.Warnf(
			runCtx,
			"Ignoring local evaluation knowledge base ID %s; persisted task uses %s",
			knowledgeBaseID,
			entity.TemporaryKnowledgeBaseID,
		)
	}
	knowledgeBaseID = entity.TemporaryKnowledgeBaseID
	startTime := time.Now().UTC()
	runtimeCollector := newEvaluationRuntimeCollector(startTime)
	started, err := e.evaluationTaskRepository.TryStartTask(runCtx, types.EvaluationTaskStartCommand{
		TenantID:        entity.TenantID,
		TaskID:          entity.ID,
		OwnerID:         e.ownerID,
		ExpectedVersion: entity.Version,
		Now:             startTime,
		LeaseExpiresAt:  e.evaluationLeaseExpiresAt(startTime),
	})
	if err != nil {
		return fmt.Errorf("mark evaluation task as running: %w", err)
	}
	runState, err := newEvaluationRunState(started)
	if err != nil {
		return err
	}
	persistedDetail, err := evaluationEntityToDetail(started)
	if err != nil {
		return err
	}
	logger.Info(runCtx, "Evaluation task status set to running")
	runHandleKey := evaluationRunHandleKey(runState.tenantID, runState.taskID)
	e.runHandles.Store(runHandleKey, cancelRun)
	defer e.runHandles.Delete(runHandleKey)
	heartbeat := e.startEvaluationHeartbeat(runCtx, runState, cancelRun)
	if ownershipErr := context.Cause(runCtx); evaluationHeartbeatOwnershipLost(ownershipErr) {
		heartbeatErr := heartbeat.StopAndWait()
		if heartbeatErr != nil {
			return heartbeatErr
		}
		return ownershipErr
	}

	cleanupErrors := make([]string, 0, 2)
	taskDeadlineStoppedRun := false
	runErr := e.evalDataset(
		runCtx,
		heartbeat.ctx,
		persistedDetail,
		knowledgeBaseID,
		runState,
		&cleanupErrors,
		&taskDeadlineStoppedRun,
		runtimeCollector,
	)
	if ownershipErr := context.Cause(runCtx); evaluationHeartbeatOwnershipLost(ownershipErr) {
		heartbeatErr := heartbeat.StopAndWait()
		if runErr != nil {
			return errors.Join(runErr, heartbeatErr)
		}
		return heartbeatErr
	}
	if evaluationTaskWriteAuthorityLost(runErr) {
		heartbeatErr := heartbeat.StopAndWait()
		if heartbeatErr != nil {
			return errors.Join(runErr, heartbeatErr)
		}
		return runErr
	}
	logger.Infof(runCtx, "Cleaning up evaluation knowledge base: %s", knowledgeBaseID)
	cleanupStart := time.Now()
	if cleanupErr := e.deleteEvaluationKnowledgeBase(heartbeat.ctx, knowledgeBaseID); cleanupErr != nil {
		logger.Errorf(
			runCtx,
			"Failed to delete evaluation knowledge base: %v, knowledge base ID: %s",
			cleanupErr,
			knowledgeBaseID,
		)
		appendEvaluationCleanupError(&cleanupErrors, "knowledge base", knowledgeBaseID, cleanupErr)
	}
	runtimeCollector.addCleanup(time.Since(cleanupStart))
	heartbeatErr := heartbeat.StopAndWait()
	if heartbeatErr != nil {
		if runErr != nil {
			return errors.Join(runErr, heartbeatErr)
		}
		return heartbeatErr
	}
	endTime := time.Now().UTC()
	runErr = errors.Join(runErr, types.ModelAccountingError(runCtx))
	terminalErr := runErr
	status := types.EvaluationStatueSuccess
	errMsg := ""
	// Terminal selection priority: a persistent cancel request wins over the
	// task deadline, business errors, and success.
	if errors.Is(context.Cause(runCtx), errEvaluationTaskCancelRequested) {
		terminalErr = errEvaluationTaskCancelRequested
		status = types.EvaluationStatueCanceled
		errMsg = evaluationTaskCanceledMessage
	} else if taskDeadlineStoppedRun {
		terminalErr = context.DeadlineExceeded
		status = types.EvaluationStatueTimedOut
		errMsg = context.DeadlineExceeded.Error()
	} else if runErr != nil {
		status = types.EvaluationStatueFailed
		errMsg = runErr.Error()
	}

	cleanupJSON, encodeErr := encodeEvaluationCleanupErrors(cleanupErrors)
	if encodeErr != nil {
		if terminalErr != nil {
			return errors.Join(terminalErr, encodeErr)
		}
		return encodeErr
	}
	runtimeMetrics := runtimeCollector.snapshot(endTime)
	publicationCtx, publicationCancel := context.WithTimeout(
		logger.CloneContext(runCtx),
		evaluationCleanupTimeout,
	)
	defer publicationCancel()
	if e.evaluationCostStore != nil {
		cost, costErr := e.evaluationCostStore.EvaluationCost(
			publicationCtx,
			runState.tenantID,
			runState.taskID,
		)
		if costErr != nil {
			if terminalErr != nil {
				return errors.Join(terminalErr, costErr)
			}
			return costErr
		}
		runtimeMetrics.Cost = cost
		if cost != nil && cost.StartedCalls > 0 && status == types.EvaluationStatueSuccess {
			terminalErr = fmt.Errorf(
				"%w: %d provider calls have no persisted terminal", types.ErrModelAccounting, cost.StartedCalls,
			)
			status = types.EvaluationStatueFailed
			errMsg = terminalErr.Error()
		}
	}
	runtimeMetricsJSON, encodeErr := encodeEvaluationRuntimeMetrics(runtimeMetrics)
	if encodeErr != nil {
		if terminalErr != nil {
			return errors.Join(terminalErr, encodeErr)
		}
		return encodeErr
	}
	terminalCommand := types.EvaluationTaskTerminalCommand{
		TenantID:        runState.tenantID,
		TaskID:          runState.taskID,
		OwnerID:         runState.ownerID,
		ExpectedVersion: runState.version,
		Status:          status,
		EndTime:         endTime,
		ErrMsg:          errMsg,
		CleanupErrors:   cleanupJSON,
		Metric:          append(types.JSON(nil), runState.metric...),
		RuntimeMetrics:  runtimeMetricsJSON,
	}
	publicationStart := time.Now()
	if _, err := e.evaluationTaskRepository.PublishTerminal(publicationCtx, terminalCommand); err != nil {
		// A cancel request persisted concurrently invalidates the terminal
		// compare-and-swap truth. Re-read the task and republish Canceled
		// instead of silently reporting the original terminal state.
		if status != types.EvaluationStatueCanceled {
			current, getErr := e.evaluationTaskRepository.GetTask(
				publicationCtx,
				runState.tenantID,
				runState.taskID,
			)
			if getErr == nil && current.CancelRequestedAt != nil &&
				current.OwnerID == runState.ownerID && current.Version == runState.version {
				terminalCommand.Status = types.EvaluationStatueCanceled
				terminalCommand.ErrMsg = evaluationTaskCanceledMessage
				if terminalErr == nil {
					terminalErr = errEvaluationTaskCancelRequested
				}
				_, err = e.evaluationTaskRepository.PublishTerminal(publicationCtx, terminalCommand)
			}
		}
		if err != nil {
			publicationErr := fmt.Errorf("publish evaluation terminal state: %w", err)
			if terminalErr != nil {
				return errors.Join(terminalErr, publicationErr)
			}
			return publicationErr
		}
	}
	runtimeCollector.addPersistence(time.Since(publicationStart))

	if terminalErr != nil {
		return terminalErr
	}
	if len(cleanupErrors) > 0 {
		logger.Warnf(runCtx, "Evaluation task completed with cleanup warnings, task ID: %s", taskID)
		return nil
	}
	logger.Infof(runCtx, "Evaluation task completed successfully, task ID: %s", taskID)
	return nil
}

// CancelEvaluation persists a user cancel request and immediately cancels the
// local run handle when the task runs on this instance. Other instances
// observe the request through their next heartbeat. The first request time
// wins; terminal tasks are returned unchanged.
func (e *EvaluationService) CancelEvaluation(ctx context.Context, taskID string) (*types.EvaluationDetail, error) {
	logger.Infof(ctx, "Requesting evaluation task cancel, task ID: %s", taskID)

	tenantID := types.MustTenantIDFromContext(ctx)
	current, err := e.evaluationTaskRepository.GetTask(ctx, tenantID, taskID)
	if err != nil {
		return nil, err
	}
	if err := AuthorizeEvaluationTaskForAPIKey(ctx, current); err != nil {
		return nil, err
	}
	entity, err := e.evaluationTaskRepository.RequestCancel(ctx, types.EvaluationTaskCancelCommand{
		TenantID: tenantID,
		TaskID:   taskID,
		Now:      time.Now().UTC(),
	})
	if err != nil {
		logger.Errorf(ctx, "Failed to request evaluation task cancel: %v", err)
		return nil, err
	}
	if handle, ok := e.runHandles.Load(evaluationRunHandleKey(tenantID, taskID)); ok {
		if cancelRun, ok := handle.(context.CancelCauseFunc); ok {
			cancelRun(errEvaluationTaskCancelRequested)
		}
	}

	detail, err := evaluationEntityToDetail(entity)
	if err != nil {
		return nil, err
	}
	logger.Infof(ctx, "Evaluation task cancel requested, task ID: %s", taskID)
	return detail, nil
}

// DeleteEvaluation soft-deletes one terminal task owned by the current
// tenant. Missing, cross-tenant, and already deleted tasks are idempotent
// successes; active tasks are rejected with a state conflict.
func (e *EvaluationService) DeleteEvaluation(ctx context.Context, taskID string) error {
	logger.Infof(ctx, "Deleting evaluation task, task ID: %s", taskID)

	tenantID := types.MustTenantIDFromContext(ctx)
	if err := e.evaluationTaskRepository.DeleteTask(ctx, tenantID, taskID, time.Now().UTC()); err != nil {
		logger.Errorf(ctx, "Failed to delete evaluation task: %v", err)
		return err
	}
	logger.Infof(ctx, "Evaluation task deleted, task ID: %s", taskID)
	return nil
}

func (e *EvaluationService) EvaluationResult(ctx context.Context, taskID string) (*types.EvaluationDetail, error) {
	logger.Info(ctx, "Start getting evaluation result")
	logger.Infof(ctx, "Task ID: %s", taskID)

	tenantID := types.MustTenantIDFromContext(ctx)
	entity, err := e.evaluationTaskRepository.GetTask(ctx, tenantID, taskID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get evaluation task: %v", err)
		return nil, err
	}
	if err := AuthorizeEvaluationTaskForAPIKey(ctx, entity); err != nil {
		return nil, err
	}
	detail, err := evaluationEntityToDetail(entity)
	if err != nil {
		logger.Errorf(ctx, "Failed to decode evaluation task: %v", err)
		return nil, err
	}

	logger.Info(ctx, "Evaluation result retrieved successfully")
	return detail, nil
}

// Evaluation starts a new evaluation task with given parameters
// datasetID: ID of the dataset to evaluate against
// knowledgeBaseID: ID of the knowledge base to use (empty to create new)
// chatModelID: ID of the chat model to evaluate
// rerankModelID: ID of the rerank model to evaluate
func (e *EvaluationService) Evaluation(ctx context.Context,
	datasetID string, knowledgeBaseID string, chatModelID string, rerankModelID string,
) (*types.EvaluationDetail, error) {
	return e.EvaluationWithOptions(ctx, &types.EvaluationOptions{
		DatasetID:       datasetID,
		KnowledgeBaseID: knowledgeBaseID,
		ChatModelID:     chatModelID,
		RerankModelID:   rerankModelID,
	})
}

// EvaluationWithOptions starts a new evaluation task with the M3 option set.
// The full effective configuration is resolved and frozen into an immutable
// experiment manifest before the task enters Pending.
func (e *EvaluationService) EvaluationWithOptions(
	ctx context.Context,
	options *types.EvaluationOptions,
) (*types.EvaluationDetail, error) {
	if options == nil {
		return nil, errors.New("start evaluation: options are required")
	}
	if err := authorizeEvaluationSourceKnowledgeBaseForAPIKey(ctx, options.KnowledgeBaseID); err != nil {
		return nil, err
	}
	datasetID := options.DatasetID
	knowledgeBaseID := options.KnowledgeBaseID
	chatModelID := options.ChatModelID
	rerankModelID := options.RerankModelID
	logger.Info(ctx, "Start evaluation")
	logger.Infof(ctx, "Dataset ID: %s, Knowledge Base ID: %s, Chat Model ID: %s, Rerank Model ID: %s",
		datasetID, knowledgeBaseID, chatModelID, rerankModelID)

	// Get tenant ID from context for multi-tenancy support
	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Tenant ID: %d", tenantID)

	// Handle knowledge base creation if not provided
	if knowledgeBaseID == "" {
		logger.Info(ctx, "No knowledge base ID provided, creating new knowledge base")
		// Create a temporary knowledge base with the deterministic default embedding model.
		models, err := e.modelService.ListModels(ctx)
		if err != nil {
			logger.Errorf(ctx, "Failed to list models: %v", err)
			return nil, err
		}

		var embeddingModelID string
		if embedding := SelectEvaluationDefaultModel(models, types.ModelTypeEmbedding); embedding != nil {
			embeddingModelID = embedding.ID
		}

		if embeddingModelID == "" {
			return nil, fmt.Errorf("no default embedding model found for evaluation")
		}

		kb, err := e.knowledgeBaseService.CreateKnowledgeBase(ctx, &types.KnowledgeBase{
			Name:             "evaluation",
			Description:      "evaluation",
			EmbeddingModelID: embeddingModelID,
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

		kb, err = e.knowledgeBaseService.CreateKnowledgeBase(ctx, &types.KnowledgeBase{
			Name:             "evaluation",
			Description:      "evaluation",
			EmbeddingModelID: kb.EmbeddingModelID,
		})
		if err != nil {
			logger.Errorf(ctx, "Failed to create knowledge base: %v", err)
			return nil, err
		}
		knowledgeBaseID = kb.ID
		logger.Infof(ctx, "Created new knowledge base with ID: %s based on existing one", knowledgeBaseID)
	}
	cleanupKnowledgeBase := true
	defer func() {
		if cleanupKnowledgeBase {
			e.cleanupEvaluationKnowledgeBase(ctx, knowledgeBaseID)
		}
	}()

	// Set default values for optional parameters
	if datasetID == "" {
		datasetID = "default"
		logger.Info(ctx, "Using default dataset")
	}

	if rerankModelID == "" {
		// 获取默认的重排模型（确定性选择，不依赖无序数据库返回）
		models, err := e.modelService.ListModels(ctx)
		if err == nil {
			if rerank := SelectEvaluationDefaultModel(models, types.ModelTypeRerank); rerank != nil {
				rerankModelID = rerank.ID
			}
		}
		if rerankModelID == "" {
			logger.Warnf(ctx, "No rerank model found, skipping rerank")
		} else {
			logger.Infof(ctx, "Using default rerank model: %s", rerankModelID)
		}
	}

	if chatModelID == "" {
		// 获取默认的LLM模型（确定性选择，不依赖无序数据库返回）
		models, err := e.modelService.ListModels(ctx)
		if err == nil {
			if chat := SelectEvaluationDefaultModel(models, types.ModelTypeKnowledgeQA); chat != nil {
				chatModelID = chat.ID
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
	now := time.Now().UTC()
	detail := &types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID:        taskID,
			TenantID:  tenantID,
			DatasetID: datasetID,
			Status:    types.EvaluationStatuePending,
			StartTime: now,
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

	// M3: apply creation-time seed and configuration overrides, then freeze
	// the immutable experiment manifest before the task enters Pending.
	if options.Seed != nil {
		detail.Params.SummaryConfig.Seed = *options.Seed
		detail.Params.SummaryConfig.SeedProvided = true
	}
	if err := applyEvaluationConfigurationOverrides(detail.Params, options.Configuration); err != nil {
		return nil, err
	}
	experiment, experimentHash, err := e.buildExperimentForTask(ctx, tenantID, options, detail, knowledgeBaseID)
	if err != nil {
		return nil, err
	}
	if experiment != nil {
		detail.Task.DatasetID = experiment.Dataset.DatasetID
	}

	entity, err := evaluationDetailToEntity(
		detail,
		knowledgeBaseID,
		e.ownerID,
		e.evaluationLeaseExpiresAt(now),
	)
	if err != nil {
		return nil, err
	}
	if err := persistEvaluationExperiment(entity, experiment, experimentHash); err != nil {
		return nil, err
	}
	logger.Info(ctx, "Persisting evaluation task")
	if err := e.evaluationTaskRepository.CreateTask(ctx, tenantID, entity); err != nil {
		return nil, fmt.Errorf("persist evaluation task: %w", err)
	}
	runDetail, err := evaluationEntityToDetail(entity)
	if err != nil {
		return nil, err
	}
	responseDetail, err := evaluationEntityToDetail(entity)
	if err != nil {
		return nil, err
	}

	// Start evaluation in background goroutine
	logger.Info(ctx, "Starting evaluation in background")
	go func(detail *types.EvaluationDetail, knowledgeBaseID string) {
		// Create new context with logger for background task
		newCtx := logger.CloneContext(ctx)
		logger.Infof(newCtx, "Background evaluation started for task ID: %s", taskID)
		if err := e.runEvaluation(newCtx, detail, knowledgeBaseID); err != nil {
			logger.Errorf(newCtx, "Evaluation task run returned an error: %v, task ID: %s", err, taskID)
		}
	}(runDetail, knowledgeBaseID)
	cleanupKnowledgeBase = false

	logger.Infof(ctx, "Evaluation task created successfully, task ID: %s", taskID)
	return responseDetail, nil
}

// EvalDataset performs the actual evaluation of a dataset.
// Processes each QA pair in parallel and records metrics.
//
// Boundary note: this entry claims the persisted lease and verifies ownership,
// but it does NOT start the heartbeat loop or register a run handle — the
// production path is runEvaluation (started by EvaluationWithOptions), which
// owns heartbeat, cancellation, and terminal publication. Callers outside
// tests should not use this wrapper for new production flows.
func (e *EvaluationService) EvalDataset(
	ctx context.Context,
	detail *types.EvaluationDetail,
	knowledgeBaseID string,
) error {
	if detail == nil || detail.Task == nil {
		return errors.New("evaluate dataset: task detail is required")
	}
	entity, err := e.evaluationTaskRepository.GetTask(ctx, detail.Task.TenantID, detail.Task.ID)
	if err != nil {
		return fmt.Errorf("load evaluation task: %w", err)
	}
	if entity.TemporaryKnowledgeBaseID == "" {
		return errors.New("evaluate dataset: persisted temporary knowledge base ID is required")
	}
	if knowledgeBaseID != "" && knowledgeBaseID != entity.TemporaryKnowledgeBaseID {
		logger.Warnf(
			ctx,
			"Ignoring local evaluation knowledge base ID %s; persisted task uses %s",
			knowledgeBaseID,
			entity.TemporaryKnowledgeBaseID,
		)
	}
	knowledgeBaseID = entity.TemporaryKnowledgeBaseID
	if entity.Status == types.EvaluationStatuePending {
		now := time.Now().UTC()
		entity, err = e.evaluationTaskRepository.TryStartTask(ctx, types.EvaluationTaskStartCommand{
			TenantID:        entity.TenantID,
			TaskID:          entity.ID,
			OwnerID:         e.ownerID,
			ExpectedVersion: entity.Version,
			Now:             now,
			LeaseExpiresAt:  e.evaluationLeaseExpiresAt(now),
		})
		if err != nil {
			return fmt.Errorf("mark evaluation task as running: %w", err)
		}
	}
	if entity.Status != types.EvaluationStatueRunning || entity.OwnerID != e.ownerID {
		return errors.New("evaluate dataset: task is not running for this service instance")
	}
	runState, err := newEvaluationRunState(entity)
	if err != nil {
		return err
	}
	persistedDetail, err := evaluationEntityToDetail(entity)
	if err != nil {
		return err
	}
	runtimeCollector := newEvaluationRuntimeCollector(entity.StartTime)
	return e.evalDataset(ctx, logger.CloneContext(ctx), persistedDetail, knowledgeBaseID, runState, nil, nil,
		runtimeCollector)
}

func (e *EvaluationService) evalDataset(
	ctx context.Context,
	cleanupCtx context.Context,
	detail *types.EvaluationDetail,
	knowledgeBaseID string,
	runState *evaluationRunState,
	cleanupErrors *[]string,
	taskDeadlineStoppedRun *bool,
	runtimeCollector *evaluationRuntimeCollector,
) (runErr error) {
	ctx = types.WithBackgroundTask(ctx)
	ctx = modelobs.WithPurpose(ctx, modelobs.PurposeEvaluation, true)
	ctx = modelobs.WithEvaluationTask(ctx, detail.Task.ID)
	defer func() {
		captureEvaluationTaskDeadline(ctx, runErr, taskDeadlineStoppedRun)
	}()
	logger.Info(ctx, "Start evaluating dataset")
	logger.Infof(ctx, "Task ID: %s, Dataset ID: %s", detail.Task.ID, detail.Task.DatasetID)

	// M3: the frozen model fingerprints must still match the live model
	// records; drift fails the task instead of mixing configurations.
	if err := e.verifyExperimentModelFingerprints(ctx, detail.Experiment); err != nil {
		captureEvaluationTaskDeadline(ctx, err, taskDeadlineStoppedRun)
		return err
	}

	// Load the immutable dataset version frozen in the experiment manifest.
	datasetLoadStart := time.Now()
	dataset, err := e.loadEvaluationDataset(ctx, detail)
	runtimeCollector.addDatasetLoad(time.Since(datasetLoadStart))
	if err != nil {
		logger.Errorf(ctx, "Failed to get dataset: %v", err)
		captureEvaluationTaskDeadline(ctx, err, taskDeadlineStoppedRun)
		return err
	}
	logger.Infof(ctx, "Dataset retrieved successfully with %d QA pairs", len(dataset))
	runtimeCollector.setTotal(len(dataset))

	// Publish the total before creating any temporary Knowledge resource.
	if err := e.publishEvaluationProgress(ctx, runState, len(dataset), 0, nil, runtimeCollector); err != nil {
		return fmt.Errorf("publish evaluation total: %w", err)
	}
	logger.Infof(ctx, "Updated task total to %d QA pairs", len(dataset))

	// Extract and organize passages from dataset
	passages := getPassageList(dataset)
	logger.Infof(ctx, "Creating knowledge from %d passages", len(passages))

	// Create knowledge base from passages (sync: wait for indexing to complete before querying)
	indexingStart := time.Now()
	knowledge, err := e.knowledgeService.CreateKnowledgeFromPassageSync(ctx, knowledgeBaseID, passages, "")
	runtimeCollector.addIndexing(time.Since(indexingStart))
	if err != nil {
		logger.Errorf(ctx, "Failed to create knowledge from passages: %v", err)
		captureEvaluationTaskDeadline(ctx, err, taskDeadlineStoppedRun)
		return err
	}
	logger.Infof(ctx, "Knowledge created and indexed successfully, ID: %s", knowledge.ID)

	// Clean up the temporary knowledge created by this method.
	defer func() {
		captureEvaluationTaskDeadline(ctx, runErr, taskDeadlineStoppedRun)
		logger.Infof(ctx, "Cleaning up resources - deleting knowledge: %s", knowledge.ID)
		if evaluationHeartbeatOwnershipLost(context.Cause(ctx)) ||
			evaluationTaskWriteAuthorityLost(runErr) {
			logger.Warnf(
				ctx,
				"Skipping evaluation Knowledge cleanup after ownership loss: %s",
				knowledge.ID,
			)
			return
		}
		cleanupStart := time.Now()
		if err := e.deleteEvaluationKnowledge(cleanupCtx, knowledgeBaseID, knowledge.ID); err != nil {
			logger.Errorf(ctx, "Failed to delete knowledge: %v, knowledge ID: %s", err, knowledge.ID)
			appendEvaluationCleanupError(cleanupErrors, "knowledge", knowledge.ID, err)
		}
		runtimeCollector.addCleanup(time.Since(cleanupStart))
	}()

	recorded, err := e.evaluationTaskRepository.RecordTemporaryKnowledge(
		ctx,
		types.EvaluationTaskKnowledgeCommand{
			TenantID:             runState.tenantID,
			TaskID:               runState.taskID,
			OwnerID:              runState.ownerID,
			ExpectedVersion:      runState.version,
			TemporaryKnowledgeID: knowledge.ID,
			UpdatedAt:            time.Now().UTC(),
		},
	)
	if err != nil {
		return fmt.Errorf("record temporary evaluation knowledge: %w", err)
	}
	runState.version = recorded.Version

	// Initialize parallel evaluation metrics
	var finished int
	var publishMu sync.Mutex
	g, workerCtx := errgroup.WithContext(ctx)
	var metricPlan *types.EvaluationMetricPlanSnapshot
	if detail.Experiment != nil {
		metricPlan = detail.Experiment.MetricPlan
	}
	metricHook, err := NewHookMetricWithRegistry(
		len(dataset),
		knowledge.ID,
		e.metricRegistry,
		metricPlan,
	)
	if err != nil {
		return fmt.Errorf("resolve evaluation metric plan: %w", err)
	}

	// Set worker limit based on available CPUs
	g.SetLimit(max(runtime.GOMAXPROCS(0)-1, 1))
	logger.Infof(ctx, "Starting evaluation with %d parallel workers", max(runtime.GOMAXPROCS(0)-1, 1))
	executionStart := time.Now()

	// Process each QA pair in parallel
	for i, qaPair := range dataset {
		qaPair := qaPair
		i := i
		g.Go(func() error {
			workerCtx := call.WithMetadata(workerCtx, map[string]any{"sample_index": i})
			if err := workerCtx.Err(); err != nil {
				return err
			}

			logger.Infof(ctx, "Processing QA pair %d, question: %s", i, qaPair.Question)
			runtimeCollector.sampleStarted()
			sampleStart := time.Now()

			// Prepare chat management parameters for this QA pair
			chatManage := detail.Params.Clone()
			chatManage.EvaluationTimings = &types.EvaluationPipelineTimings{}
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
			metricHook.recordInit(i)
			metricHook.recordQaPair(i, qaPair)

			// Execute knowledge QA pipeline
			logger.Infof(ctx, "Running knowledge QA for question: %s", qaPair.Question)
			qaErr := e.sessionService.KnowledgeQAByEvent(workerCtx, chatManage, types.Pipline["rag"])
			if qaErr != nil {
				canceled := errors.Is(qaErr, context.Canceled) || errors.Is(workerCtx.Err(), context.Canceled)
				runtimeCollector.sampleFailed(canceled)
				metricHook.recordSearchResult(i, chatManage.SearchResult)
				metricHook.recordRerankResult(i, chatManage.RerankResult)
				metricHook.recordChatResponse(i, chatManage.ChatResponse)
				if publishErr := e.publishFailedEvaluationQuestion(
					ctx, detail, runState, metricHook, runtimeCollector, chatManage,
					&publishMu, &finished, len(dataset), i, time.Since(sampleStart), canceled,
				); publishErr != nil {
					return errors.Join(qaErr, publishErr)
				}
				logger.Errorf(ctx, "Failed to process question %d: %v", i, qaErr)
				return qaErr
			}

			// Record evaluation metrics
			logger.Infof(ctx, "Recording metrics for QA pair %d", i)
			metricHook.recordSearchResult(i, chatManage.SearchResult)
			metricHook.recordRerankResult(i, chatManage.RerankResult)
			metricHook.recordChatResponse(i, chatManage.ChatResponse)
			response := chatManage.ChatResponse
			promptTokens, completionTokens, totalTokens, usageReported := evaluationQuestionUsage(response)

			// Publish each completed QA pair and its aggregate metric as one ordered snapshot.
			publishMu.Lock()
			if err := workerCtx.Err(); err != nil {
				runtimeCollector.sampleFailed(true)
				runtimeCollector.recordSampleTokens(promptTokens, completionTokens, totalTokens, usageReported)
				publishMu.Unlock()
				return err
			}
			if err := metricHook.recordFinishWithContext(workerCtx, i); err != nil {
				runtimeCollector.sampleFailed(false)
				runtimeCollector.recordSampleTokens(promptTokens, completionTokens, totalTokens, usageReported)
				publishMu.Unlock()
				return fmt.Errorf("compute metrics for QA pair %d: %w", i, err)
			}
			runtimeCollector.sampleSucceeded(promptTokens, completionTokens, totalTokens, usageReported)
			finished += 1
			finishedSnapshot := finished
			metricResult := metricHook.MetricResult()
			var updateErr error
			if e.questionResultRepository != nil && detail.Experiment != nil {
				// M3: publish the per-question fact, finished counter, and
				// aggregate metric in one transaction.
				input := metricHook.questionResultInput(i, detail.Experiment.MetricPlan, detail.Params.RerankTopK)
				if input != nil {
					applyEvaluationQuestionRuntime(
						input, chatManage.EvaluationTimings, time.Since(sampleStart), usageReported,
					)
					updateErr = e.publishQuestionResult(workerCtx, runState, len(dataset), finishedSnapshot,
						metricResult, input, runtimeCollector)
				} else {
					updateErr = e.publishEvaluationProgress(workerCtx, runState, len(dataset), finishedSnapshot,
						metricResult, runtimeCollector)
				}
			} else {
				updateErr = e.publishEvaluationProgress(
					workerCtx,
					runState,
					len(dataset),
					finishedSnapshot,
					metricResult,
					runtimeCollector,
				)
			}
			publishMu.Unlock()
			if updateErr != nil {
				return fmt.Errorf("publish progress for QA pair %d: %w", i, updateErr)
			}
			logger.Infof(ctx, "Updated task progress: %d/%d completed", finishedSnapshot, len(dataset))
			return nil
		})
	}

	// Wait for all parallel evaluations to complete
	logger.Info(ctx, "Waiting for all evaluation tasks to complete")
	if err := g.Wait(); err != nil {
		runtimeCollector.addExecution(time.Since(executionStart))
		logger.Errorf(ctx, "Evaluation error: %v", err)
		return err
	}
	runtimeCollector.addExecution(time.Since(executionStart))

	// Final update of evaluation metrics
	finalMetric := metricHook.MetricResult()
	if err := e.publishEvaluationProgress(
		ctx, runState, len(dataset), finished, finalMetric, runtimeCollector,
	); err != nil {
		return fmt.Errorf("publish final evaluation progress: %w", err)
	}

	logger.Infof(ctx, "Dataset evaluation completed successfully, task ID: %s", detail.Task.ID)
	return nil
}

func (e *EvaluationService) publishEvaluationProgress(
	ctx context.Context,
	runState *evaluationRunState,
	total int,
	finished int,
	metric *types.MetricResult,
	runtimeCollector *evaluationRuntimeCollector,
) error {
	if runState == nil {
		return errors.New("publish evaluation progress: run state is required")
	}
	metricJSON, err := encodeEvaluationMetric(metric)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	runtimeMetricsJSON, err := encodeEvaluationRuntimeMetrics(runtimeCollector.currentSnapshot(now))
	if err != nil {
		return err
	}
	persistenceStart := time.Now()
	updated, err := e.evaluationTaskRepository.PublishProgress(
		ctx,
		types.EvaluationTaskProgressCommand{
			TenantID:        runState.tenantID,
			TaskID:          runState.taskID,
			OwnerID:         runState.ownerID,
			ExpectedVersion: runState.version,
			Total:           total,
			Finished:        finished,
			Metric:          metricJSON,
			RuntimeMetrics:  runtimeMetricsJSON,
			Now:             now,
			LeaseExpiresAt:  e.evaluationLeaseExpiresAt(now),
		},
	)
	if err != nil {
		return err
	}
	runtimeCollector.addPersistence(time.Since(persistenceStart))
	runState.version = updated.Version
	runState.metric = append(types.JSON(nil), updated.Metric...)
	runState.runtimeMetrics = append(types.JSON(nil), updated.RuntimeMetrics...)
	return nil
}

// publishQuestionResult publishes one per-question fact, the finished
// counter, and the aggregate metric in one repository transaction. Idempotent
// retries (same result hash) leave the task version untouched.
func (e *EvaluationService) publishQuestionResult(
	ctx context.Context,
	runState *evaluationRunState,
	total int,
	finished int,
	metric *types.MetricResult,
	input *types.EvaluationQuestionResultInput,
	runtimeCollector *evaluationRuntimeCollector,
) error {
	if runState == nil {
		return errors.New("publish evaluation question result: run state is required")
	}
	metricJSON, err := encodeEvaluationMetric(metric)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	runtimeMetricsJSON, err := encodeEvaluationRuntimeMetrics(runtimeCollector.currentSnapshot(now))
	if err != nil {
		return err
	}
	persistenceStart := time.Now()
	updated, inserted, err := e.questionResultRepository.PublishQuestionResult(
		ctx,
		interfaces.EvaluationQuestionResultCommand{
			TenantID:        runState.tenantID,
			TaskID:          runState.taskID,
			OwnerID:         runState.ownerID,
			ExpectedVersion: runState.version,
			Total:           total,
			Finished:        finished,
			Metric:          metricJSON,
			RuntimeMetrics:  runtimeMetricsJSON,
			Now:             now,
			LeaseExpiresAt:  e.evaluationLeaseExpiresAt(now),
			Result:          input,
		},
	)
	if err != nil {
		return err
	}
	runtimeCollector.addPersistence(time.Since(persistenceStart))
	runState.version = updated.Version
	runState.metric = append(types.JSON(nil), updated.Metric...)
	runState.runtimeMetrics = append(types.JSON(nil), updated.RuntimeMetrics...)
	if !inserted {
		logger.Infof(ctx, "Question result for sample %d already published with identical hash", input.SampleIndex)
	}
	return nil
}

func (e *EvaluationService) publishFailedEvaluationQuestion(
	ctx context.Context,
	detail *types.EvaluationDetail,
	runState *evaluationRunState,
	metricHook *HookMetric,
	runtimeCollector *evaluationRuntimeCollector,
	chatManage *types.ChatManage,
	publishMu *sync.Mutex,
	finished *int,
	total int,
	sampleIndex int,
	totalDuration time.Duration,
	canceled bool,
) error {
	if ctx.Err() != nil || e.questionResultRepository == nil || detail.Experiment == nil {
		return nil
	}
	input := metricHook.questionResultInput(
		sampleIndex,
		detail.Experiment.MetricPlan,
		detail.Params.RerankTopK,
	)
	if input == nil {
		return nil
	}
	promptTokens, completionTokens, totalTokens, usageReported := evaluationQuestionUsage(chatManage.ChatResponse)
	runtimeCollector.recordSampleTokens(promptTokens, completionTokens, totalTokens, usageReported)
	applyEvaluationQuestionRuntime(input, chatManage.EvaluationTimings, totalDuration, usageReported)
	input.Status = types.EvaluationQuestionStatusFailed
	input.ErrorCode = "evaluation_question_failed"
	if canceled {
		input.Status = types.EvaluationQuestionStatusCanceled
		input.ErrorCode = "evaluation_question_canceled"
	}

	publishMu.Lock()
	defer publishMu.Unlock()
	*finished++
	finishedSnapshot := *finished
	if err := e.publishQuestionResult(
		ctx,
		runState,
		total,
		finishedSnapshot,
		metricHook.MetricResult(),
		input,
		runtimeCollector,
	); err != nil {
		*finished--
		return fmt.Errorf("publish failed question result for sample %d: %w", sampleIndex, err)
	}
	return nil
}

// getPassageList extracts and organizes passages from QA pairs
// Returns a slice of passages indexed by their passage IDs
func getPassageList(dataset []*types.QAPair) []string {
	for _, qaPair := range dataset {
		if qaPair != nil && len(qaPair.Corpus) > 0 {
			return append([]string(nil), qaPair.Corpus...)
		}
	}
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
