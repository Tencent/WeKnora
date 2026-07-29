package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

const (
	// tableDescriptionPromptTemplate is the prompt template for generating table descriptions
	tableDescriptionPromptTemplate = `You are a data analysis expert. Based on the following table structure information and data samples, generate a concise table metadata description (200-300 words).

Table name: %s

%s

%s

Please describe the table from the following dimensions:
1. **Data Subject**: What type of data does this table record? (e.g., user information, sales records, log data, etc.)
2. **Core Fields**: List 3-5 most important fields and their meanings
3. **Data Scale**: Total number of rows and columns
4. **Business Scenarios**: What business analysis or application scenarios might this table be used for?
5. **Key Characteristics**: What notable features does the data have? (e.g., contains geographic locations, has category labels, has hierarchical relationships, etc.)

**Important Notes**:
- Do not output specific data values or sample content
- Use general descriptions so users can quickly determine if this table contains the information they need
- Use concise and professional language for easy retrieval and understanding
- Write the description in the same language as the data content`

	// columnDescriptionsPromptTemplate is the prompt template for generating column descriptions
	columnDescriptionsPromptTemplate = `You are a data analysis expert. Based on the following table structure information and data samples, generate structured description information for each column.

Table name: %s

%s

%s

Please generate a detailed description for each column, including the following information:
1. **Field Meaning**: What information does this column store? (e.g., user ID, order amount, creation time, etc.)
2. **Data Type**: The type and format of the data (e.g., integer, string, datetime, boolean, etc.)
3. **Business Purpose**: The role of this field in business (e.g., for user identification, amount calculation, time sorting, etc.)
4. **Data Characteristics**: Notable features of the data (e.g., unique identifier, nullable, has enum values, has units, etc.)

Please output in the following format (one paragraph per column):

**Column1** (data type)
- Field Meaning: xxx
- Business Purpose: xxx
- Data Characteristics: xxx

**Column2** (data type)
- Field Meaning: xxx
- Business Purpose: xxx
- Data Characteristics: xxx

**Important Notes**:
- Do not output specific data values, only describe the field metadata
- Use clear business terms for easy user understanding and search
- If enum value ranges can be inferred from sample data, provide a summary (e.g., status field contains pending/in-progress/completed states)
- Write descriptions in the same language as the data content`
)

// NewChunkExtractTask creates a new chunk extract task. It returns
// (enqueued, err): enqueued is true only when a task was actually placed on
// the queue. When NEO4J is disabled the call is a no-op and returns
// (false, nil) — callers that seeded a pending-subtask counter for this chunk
// MUST release that slot, otherwise the parent knowledge stays stuck in
// "finalizing" forever (the graph subtask it's waiting on was never enqueued).
func NewChunkExtractTask(
	ctx context.Context,
	client interfaces.TaskEnqueuer,
	tenantID uint64,
	chunkID string,
	modelID string,
	knowledgeID string,
	attempt int,
	chunkIndex int,
) (bool, error) {
	if strings.ToLower(os.Getenv("NEO4J_ENABLE")) != "true" {
		logger.Warn(ctx, "NEO4J is not enabled, skip chunk extract task")
		return false, nil
	}
	taskPayload := types.ExtractChunkPayload{
		TenantID:    tenantID,
		ChunkID:     chunkID,
		ModelID:     modelID,
		KnowledgeID: knowledgeID,
		Attempt:     attempt,
		ChunkIndex:  chunkIndex,
	}
	langfuse.InjectTracing(ctx, &taskPayload)
	payload, err := json.Marshal(taskPayload)
	if err != nil {
		return false, err
	}
	task := asynq.NewTask(types.TypeChunkExtract, payload,
		asynq.Queue(types.QueueGraph), asynq.MaxRetry(3), asynq.Timeout(30*time.Minute))
	info, err := client.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "failed to enqueue task: %v", err)
		return false, fmt.Errorf("failed to enqueue task: %v", err)
	}
	logger.Infof(ctx, "enqueued task: id=%s queue=%s chunk=%s", info.ID, info.Queue, chunkID)
	return true, nil
}

// NewTableExtractTask creates a new table extract task
func NewDataTableSummaryTask(
	ctx context.Context,
	client interfaces.TaskEnqueuer,
	tenantID uint64,
	knowledgeID string,
	summaryModel string,
	embeddingModel string,
	attempt int,
) error {
	if attempt <= 0 {
		return fmt.Errorf("data table summary requires a positive knowledge attempt")
	}
	taskPayload := DataTableSummaryPayload{
		TenantID:       tenantID,
		KnowledgeID:    knowledgeID,
		SummaryModel:   summaryModel,
		EmbeddingModel: embeddingModel,
		Attempt:        attempt,
	}
	langfuse.InjectTracing(ctx, &taskPayload)
	payload, err := json.Marshal(taskPayload)
	if err != nil {
		return fmt.Errorf("marshal data table summary task: %w", err)
	}
	task := asynq.NewTask(types.TypeDataTableSummary, payload,
		asynq.Queue(types.QueueSummary), asynq.MaxRetry(3), asynq.Timeout(30*time.Minute))
	info, err := client.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "failed to enqueue data table summary task: %v", err)
		return fmt.Errorf("failed to enqueue data table summary task: %w", err)
	}
	logger.Infof(ctx, "enqueued data table summary task: id=%s queue=%s knowledge=%s attempt=%d",
		info.ID, info.Queue, knowledgeID, attempt)
	return nil
}

// ChunkExtractService is a service for extracting chunks
type ChunkExtractService struct {
	template          *types.PromptTemplateStructured
	modelService      interfaces.ModelService
	knowledgeBaseRepo interfaces.KnowledgeBaseRepository
	knowledgeRepo     interfaces.KnowledgeRepository
	chunkRepo         interfaces.ChunkRepository
	graphEngine       interfaces.RetrieveGraphRepository
	// spanTracker records this graph-extract task's subspan under the
	// parent attempt's postprocess stage so the trace viewer shows real
	// per-chunk graph extraction time rather than the upstream's enqueue.
	spanTracker SpanTracker
}

// NewChunkExtractService creates a new chunk extract service
func NewChunkExtractService(
	config *config.Config,
	modelService interfaces.ModelService,
	knowledgeBaseRepo interfaces.KnowledgeBaseRepository,
	knowledgeRepo interfaces.KnowledgeRepository,
	chunkRepo interfaces.ChunkRepository,
	graphEngine interfaces.RetrieveGraphRepository,
	spanTracker SpanTracker,
) interfaces.TaskHandler {
	return &ChunkExtractService{
		template:          config.ExtractManager.ExtractGraph,
		modelService:      modelService,
		knowledgeBaseRepo: knowledgeBaseRepo,
		knowledgeRepo:     knowledgeRepo,
		chunkRepo:         chunkRepo,
		graphEngine:       graphEngine,
		spanTracker:       spanTracker,
	}
}

func (s *ChunkExtractService) tracker() SpanTracker {
	if s.spanTracker == nil {
		return noopSpanTracker{}
	}
	return s.spanTracker
}

// Handle handles the chunk extraction task
func (s *ChunkExtractService) Handle(ctx context.Context, t *asynq.Task) error {
	var p types.ExtractChunkPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		logger.Errorf(ctx, "failed to unmarshal task payload: %v", err)
		return err
	}
	ctx = logger.WithRequestID(ctx, uuid.New().String())
	ctx = logger.WithField(ctx, "extract", p.ChunkID)
	ctx = context.WithValue(ctx, types.TenantIDContextKey, p.TenantID)

	current, err := requireCarriedKnowledgeAttempt(ctx, s.tracker(), p.KnowledgeID, p.Attempt)
	if err != nil {
		return err
	}
	if !current {
		logger.Infof(ctx, "graph extract: dropping stale attempt=%d knowledge=%s",
			p.Attempt, p.KnowledgeID)
		return nil
	}
	ctx = withAttempt(ctx, p.Attempt)

	// Open a postprocess subspan keyed by chunk ordinal so the trace
	// shows real per-chunk graph extraction time. Skipped silently when
	// upstream didn't pass the parent attempt (legacy in-flight tasks)
	// or when the postprocess stage span isn't found.
	var gSpan *Span
	if p.KnowledgeID != "" && p.Attempt > 0 {
		parent := s.tracker().LookupStage(ctx, p.KnowledgeID, p.Attempt, types.StagePostProcess)
		if parent != nil {
			gSpan = s.tracker().BeginSubSpan(ctx, parent,
				fmt.Sprintf("postprocess.graph.chunk[%d]", p.ChunkIndex),
				types.SpanKindSubSpan,
				types.JSONMap{
					"chunk_id":    p.ChunkID,
					"chunk_index": p.ChunkIndex,
					"model_id":    p.ModelID,
				})
		}
	}
	var handleErr error
	graphOut := types.JSONMap{}
	defer func() {
		// Decrement the parent's enrichment counter on terminal exit so a
		// completed (or terminally-failed) per-chunk extract releases its
		// slot in pending_subtasks_count. KnowledgeID is the new (post-#? )
		// payload field; legacy in-flight tasks without it are skipped.
		finalizeSubtaskDetached(ctx, s.knowledgeRepo, p.KnowledgeID, p.Attempt,
			fmt.Sprintf("graph_chunk[%d]", p.ChunkIndex),
			handleErr, false, isFinalAsynqAttempt(ctx))
		if gSpan == nil {
			return
		}
		if handleErr != nil {
			s.tracker().FailSpan(ctx, gSpan, "GRAPH_EXTRACT_FAILED", handleErr.Error(), handleErr)
		} else {
			s.tracker().EndSpan(ctx, gSpan, graphOut)
		}
	}()

	// Short-circuit when the parent knowledge has been cancelled / deleted.
	// Each graph extract is per-chunk and runs one LLM call — the most
	// expensive enrichment fan-out in the pipeline. Skipping on cancel
	// is the whole point of the finalizing-state machinery above.
	if p.KnowledgeID != "" && s.knowledgeRepo != nil {
		if k, kerr := s.knowledgeRepo.GetKnowledgeByIDOnly(ctx, p.KnowledgeID); kerr == nil && k != nil {
			switch k.ParseStatus {
			case types.ParseStatusCancelled, types.ParseStatusDeleting:
				logger.Infof(ctx, "graph extract: knowledge %s aborted (%s), skipping chunk %s",
					p.KnowledgeID, k.ParseStatus, p.ChunkID)
				graphOut["skipped"] = "knowledge_" + k.ParseStatus
				return nil
			}
		}
	}

	chunk, err := s.chunkRepo.GetChunkByID(ctx, p.TenantID, p.ChunkID)
	if err != nil {
		logger.Errorf(ctx, "failed to get chunk: %v", err)
		handleErr = err
		return err
	}
	// Capture chunk content shape on output — lets traces answer "WHAT
	// did the LLM call see?" without joining back to the chunk store.
	// Preview is truncated to keep span rows reasonable.
	if gSpan != nil {
		graphOut["chunk_chars"] = len([]rune(chunk.Content))
		graphOut["chunk_preview"] = previewText(chunk.Content, 200)
	}
	kb, err := s.knowledgeBaseRepo.GetKnowledgeBaseByID(ctx, chunk.KnowledgeBaseID)
	if err != nil {
		logger.Errorf(ctx, "failed to get knowledge base: %v", err)
		handleErr = err
		return err
	}

	var processOverrides *types.KnowledgeProcessOverrides
	knowledgeID := p.KnowledgeID
	if knowledgeID == "" {
		knowledgeID = chunk.KnowledgeID
	}
	if knowledgeID != "" && s.knowledgeRepo != nil {
		if k, kerr := s.knowledgeRepo.GetKnowledgeByIDOnly(ctx, knowledgeID); kerr == nil && k != nil {
			processOverrides, _ = k.ProcessOverrides()
		}
	}
	extractCfg := ResolveProcessConfig(kb, processOverrides).ExtractConfig
	if !extractCfg.Enabled {
		logger.Warnf(ctx, "extract config not enabled")
		graphOut["skipped"] = "extract_disabled"
		return nil
	}

	chatModel, err := s.modelService.GetChatModel(ctx, p.ModelID)
	if err != nil {
		logger.Errorf(ctx, "failed to get chat model: %v", err)
		handleErr = err
		return err
	}

	template := &types.PromptTemplateStructured{
		Description: types.AppendCustomPromptInstructions(
			s.template.Description, extractCfg.CustomInstructions, "graph_extraction"),
		Tags: extractCfg.Tags,
		Examples: []types.GraphData{
			{
				Text:     extractCfg.Text,
				Node:     extractCfg.Nodes,
				Relation: extractCfg.Relations,
			},
		},
	}
	extractor := chatpipeline.NewExtractor(chatModel, template)
	graph, err := extractor.Extract(ctx, chunk.Content)
	if err != nil {
		handleErr = err
		return err
	}

	chunk, err = s.chunkRepo.GetChunkByID(ctx, p.TenantID, p.ChunkID)
	if err != nil {
		logger.Warnf(ctx, "graph ignore chunk %s: %v", p.ChunkID, err)
		graphOut["skipped"] = "chunk_disappeared"
		return nil
	}

	for _, node := range graph.Node {
		node.Chunks = []string{chunk.ID}
	}
	current, err = requireCarriedKnowledgeAttempt(ctx, s.tracker(), p.KnowledgeID, p.Attempt)
	if err != nil {
		handleErr = err
		return err
	}
	if !current {
		logger.Infof(ctx, "graph extract: attempt became stale after LLM knowledge=%s attempt=%d",
			p.KnowledgeID, p.Attempt)
		graphOut["skipped"] = "stale_attempt"
		return nil
	}
	addGraph := func() error {
		return s.graphEngine.AddGraph(ctx,
			types.NameSpace{KnowledgeBase: chunk.KnowledgeBaseID, Knowledge: chunk.KnowledgeID},
			[]*types.GraphData{graph},
		)
	}
	if p.Attempt > 0 {
		guard, ok := s.knowledgeRepo.(interfaces.KnowledgeAttemptMutationGuard)
		if !ok {
			handleErr = errors.New("graph repository does not support attempt-guarded mutations")
			return handleErr
		}
		var applied bool
		applied, err = guard.RunWithKnowledgeAttemptMutation(
			ctx,
			p.KnowledgeID,
			p.Attempt,
			[]string{types.ParseStatusProcessing, types.ParseStatusFinalizing},
			addGraph,
		)
		if err == nil && !applied {
			graphOut["skipped"] = "stale_attempt_before_graph_write"
			return nil
		}
	} else {
		err = addGraph()
	}
	if err != nil {
		logger.Errorf(ctx, "failed to add graph: %v", err)
		handleErr = err
		return err
	}
	graphOut["nodes_added"] = len(graph.Node)
	graphOut["relations_added"] = len(graph.Relation)
	// Capture a couple of sample nodes/relations so the trace viewer can
	// answer "what did the LLM actually extract?" without round-tripping
	// to the graph store. Cap to two each — anything more bloats span
	// rows and the full graph is queryable elsewhere.
	if len(graph.Node) > 0 {
		samples := graph.Node
		if len(samples) > 2 {
			samples = samples[:2]
		}
		names := make([]string, 0, len(samples))
		for _, n := range samples {
			names = append(names, n.Name)
		}
		graphOut["sample_nodes"] = names
	}
	if len(graph.Relation) > 0 {
		samples := graph.Relation
		if len(samples) > 2 {
			samples = samples[:2]
		}
		out := make([]string, 0, len(samples))
		for _, r := range samples {
			out = append(out, fmt.Sprintf("%s --[%s]--> %s", r.Node1, r.Type, r.Node2))
		}
		graphOut["sample_relations"] = out
	}
	return nil
}

// DataTableExtractPayload represents the table extract task payload
type DataTableSummaryPayload struct {
	types.TracingContext
	TenantID       uint64 `json:"tenant_id"`
	KnowledgeID    string `json:"knowledge_id"`
	SummaryModel   string `json:"summary_model"`
	EmbeddingModel string `json:"embedding_model"`
	Attempt        int    `json:"attempt"`
}

// DataTableSummaryService is a service for extracting tables
type DataTableSummaryService struct {
	modelService         interfaces.ModelService
	knowledgeBaseService interfaces.KnowledgeBaseService
	knowledgeService     interfaces.KnowledgeService
	fileService          interfaces.FileService
	chunkService         interfaces.ChunkService
	tenantService        interfaces.TenantService
	retrieveEngine       interfaces.RetrieveEngineRegistry
	ownership            retriever.TenantStoreOwnership
	sqlDB                *sql.DB
	storageResolver      interfaces.StorageBackendResolver
	spanTracker          SpanTracker
}

// NewDataTableSummaryService creates a new DataTableSummaryService
func NewDataTableSummaryService(
	modelService interfaces.ModelService,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	fileService interfaces.FileService,
	chunkService interfaces.ChunkService,
	tenantService interfaces.TenantService,
	retrieveEngine interfaces.RetrieveEngineRegistry,
	ownership retriever.TenantStoreOwnership,
	sqlDB *sql.DB,
	storageResolver interfaces.StorageBackendResolver,
	spanTracker SpanTracker,
) interfaces.TaskHandler {
	return &DataTableSummaryService{
		modelService:         modelService,
		knowledgeBaseService: knowledgeBaseService,
		knowledgeService:     knowledgeService,
		fileService:          fileService,
		chunkService:         chunkService,
		tenantService:        tenantService,
		retrieveEngine:       retrieveEngine,
		ownership:            ownership,
		sqlDB:                sqlDB,
		storageResolver:      storageResolver,
		spanTracker:          spanTracker,
	}
}

func (s *DataTableSummaryService) tracker() SpanTracker {
	if s.spanTracker == nil {
		return noopSpanTracker{}
	}
	return s.spanTracker
}

var errDataTableSummaryStale = errors.New("data table summary attempt is stale")

func (s *DataTableSummaryService) requireCurrentAttempt(
	ctx context.Context, knowledgeID string, attempt int,
) error {
	if attempt <= 0 {
		return fmt.Errorf("data table summary requires a positive knowledge attempt")
	}
	current, err := requireCarriedKnowledgeAttempt(ctx, s.tracker(), knowledgeID, attempt)
	if err != nil {
		return err
	}
	if !current {
		return errDataTableSummaryStale
	}
	return nil
}

func (s *DataTableSummaryService) requireAttemptAwareRepository(attempt int) error {
	if attempt <= 0 {
		return fmt.Errorf("data table summary requires a positive knowledge attempt")
	}
	if s.knowledgeService == nil {
		return fmt.Errorf("data table summary knowledge repository is unavailable")
	}
	repo := s.knowledgeService.GetRepository()
	if repo == nil {
		return fmt.Errorf("data table summary knowledge repository is unavailable")
	}
	if _, ok := repo.(interfaces.KnowledgeAttemptUpdater); !ok {
		return fmt.Errorf("data table summary knowledge repository does not support attempt-aware updates")
	}
	return nil
}

// Handle implements the TaskHandler interface for table extraction
// 整体流程：初始化 -> 准备资源 -> 加载数据 -> 生成摘要 -> 创建索引
func (s *DataTableSummaryService) Handle(ctx context.Context, t *asynq.Task) error {
	// 1. 解析任务并初始化上下文
	var payload DataTableSummaryPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "failed to unmarshal table extract task payload: %v", err)
		return err
	}

	ctx = logger.WithRequestID(ctx, uuid.New().String())
	ctx = logger.WithField(ctx, "knowledge", payload.KnowledgeID)
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if err := s.requireCurrentAttempt(ctx, payload.KnowledgeID, payload.Attempt); err != nil {
		if errors.Is(err, errDataTableSummaryStale) {
			logger.Infof(ctx, "data table summary: dropping stale attempt=%d knowledge=%s",
				payload.Attempt, payload.KnowledgeID)
			return nil
		}
		return err
	}
	if err := s.requireAttemptAwareRepository(payload.Attempt); err != nil {
		return err
	}
	ctx = withAttempt(ctx, payload.Attempt)

	logger.Infof(ctx, "Processing table extraction for knowledge: %s attempt=%d",
		payload.KnowledgeID, payload.Attempt)

	// 2. 准备所有必需的资源（知识、模型、引擎等）
	resources, err := s.prepareResources(ctx, payload)
	if err != nil {
		return err
	}

	// 3. 加载表格数据并生成摘要
	chunks, err := s.processTableData(ctx, resources, payload.Attempt)
	if err != nil {
		return err
	}
	if err := s.requireCurrentAttempt(ctx, payload.KnowledgeID, payload.Attempt); err != nil {
		if errors.Is(err, errDataTableSummaryStale) {
			logger.Infof(ctx, "data table summary: attempt became stale after LLM knowledge=%s attempt=%d",
				payload.KnowledgeID, payload.Attempt)
			return nil
		}
		return err
	}

	// 4. 索引到向量数据库
	if err := s.indexToVectorDB(
		ctx, payload.KnowledgeID, payload.Attempt, chunks, resources.retrieveEngine, resources.embeddingModel,
	); err != nil {
		if errors.Is(err, errDataTableSummaryStale) {
			logger.Infof(ctx, "data table summary: attempt became stale while writing knowledge=%s attempt=%d",
				payload.KnowledgeID, payload.Attempt)
			return nil
		}
		cleanupErr := s.cleanupOnFailure(ctx, payload.Attempt, resources, chunks, err)
		return errors.Join(err, cleanupErr)
	}

	logger.Infof(ctx, "Table extraction completed for knowledge: %s", payload.KnowledgeID)
	return nil
}

// extractionResources 封装提取过程所需的所有资源
type extractionResources struct {
	knowledge      *types.Knowledge
	knowledgeBase  *types.KnowledgeBase
	tenant         *types.Tenant
	chatModel      chat.Chat
	embeddingModel embedding.Embedder
	retrieveEngine *retriever.CompositeRetrieveEngine
}

// prepareResources 准备提取所需的所有资源
// 思路：集中加载所有依赖，统一错误处理，避免分散的资源获取逻辑
func (s *DataTableSummaryService) prepareResources(ctx context.Context, payload DataTableSummaryPayload) (*extractionResources, error) {
	// 获取并验证知识文件
	knowledge, err := s.knowledgeService.GetKnowledgeByID(ctx, payload.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "failed to get knowledge: %v", err)
		return nil, err
	}

	// 验证文件类型
	fileType := strings.ToLower(knowledge.FileType)
	if fileType != "csv" && fileType != "xlsx" && fileType != "xls" {
		logger.Warnf(ctx, "knowledge %s is not a CSV or Excel file, skipping table summary", payload.KnowledgeID)
		return nil, fmt.Errorf("unsupported file type: %s", fileType)
	}

	// 获取空间信息
	tenantInfo, err := s.tenantService.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Errorf(ctx, "failed to get tenant: %v", err)
		return nil, err
	}

	// 获取聊天模型（用于生成摘要）
	chatModel, err := s.modelService.GetChatModel(ctx, payload.SummaryModel)
	if err != nil {
		logger.Errorf(ctx, "failed to get chat model: %v", err)
		return nil, err
	}

	// 获取嵌入模型（用于向量化）
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, payload.EmbeddingModel)
	if err != nil {
		logger.Errorf(ctx, "failed to get embedding model: %v", err)
		return nil, err
	}

	// Load the KB to discover its VectorStoreID binding so the factory can
	// route to the bound store (or fall back to tenant engines if unbound).
	kb, err := s.knowledgeBaseService.GetKnowledgeBaseByID(ctx, knowledge.KnowledgeBaseID)
	if err != nil {
		logger.Errorf(ctx, "failed to get knowledge base for vector store lookup: %v", err)
		return nil, err
	}
	var vectorStoreID *string
	if kb != nil {
		vectorStoreID = kb.VectorStoreID
	}

	// The factory's unbound path reads TenantInfo from ctx.
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

	// Resolve the engine via the factory using the KB's VectorStore binding
	// (nil -> tenant effective engines fallback; verified tenant ownership otherwise).
	retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, payload.TenantID, vectorStoreID)
	if err != nil {
		logger.Errorf(ctx, "failed to get retrieve engine: %v", err)
		return nil, err
	}

	return &extractionResources{
		knowledge:      knowledge,
		knowledgeBase:  kb,
		tenant:         tenantInfo,
		chatModel:      chatModel,
		embeddingModel: embeddingModel,
		retrieveEngine: retrieveEngine,
	}, nil
}

// resolveFileServiceForKnowledge resolves a provider-specific file service for the current knowledge file.
// It falls back to the global service when tenant storage config is unavailable.
func (s *DataTableSummaryService) resolveFileServiceForKnowledge(ctx context.Context, resources *extractionResources) interfaces.FileService {
	if resources == nil || resources.knowledge == nil {
		return s.fileService
	}
	if resources.tenant == nil {
		return s.fileService
	}

	provider := types.InferStorageFromFilePath(resources.knowledge.FilePath)
	if provider == "" && resources.tenant.StorageEngineConfig != nil {
		provider = strings.ToLower(strings.TrimSpace(resources.tenant.StorageEngineConfig.DefaultProvider))
	}

	baseDir := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	backendID, _, _ := types.ParseStorageBackendPath(resources.knowledge.FilePath)
	if backendID == "" && resources.knowledgeBase != nil && resources.knowledgeBase.StorageBackendID != nil {
		backendID = strings.TrimSpace(*resources.knowledgeBase.StorageBackendID)
	}

	// New-model workspaces resolve via DefaultStorageBackendID even when no
	// legacy StorageEngineConfig / provider is present, so gate on the resolver
	// and a usable backendID/provider rather than requiring a non-empty provider.
	if s.storageResolver == nil || (backendID == "" && provider == "") {
		return s.fileService
	}

	resolvedSvc, resolvedProvider, err := s.storageResolver.ResolveFileService(ctx, resources.tenant, backendID, provider, baseDir)
	if err != nil {
		logger.Warnf(ctx, "[TableSummary] Failed to resolve file service for provider=%s, fallback to default: %v", provider, err)
		return s.fileService
	}
	logger.Infof(ctx, "[TableSummary] Resolved file service for knowledge=%s provider=%s", resources.knowledge.ID, resolvedProvider)
	return resolvedSvc
}

// processTableData 处理表格数据：加载 -> 分析 -> 生成摘要 -> 创建chunks
// 思路：将数据处理的核心流程集中在一起，保持逻辑连贯性
func (s *DataTableSummaryService) processTableData(
	ctx context.Context, resources *extractionResources, attempt int,
) ([]*types.Chunk, error) {
	// 创建DuckDB会话并加载数据
	sessionID := fmt.Sprintf("table_summary_%s", resources.knowledge.ID)
	fileSvc := s.resolveFileServiceForKnowledge(ctx, resources)
	duckdbTool := tools.NewDataAnalysisTool(s.knowledgeBaseService, s.knowledgeService, s.tenantService, fileSvc, s.sqlDB, sessionID, s.storageResolver)
	defer duckdbTool.Cleanup(ctx)

	// 使用knowledge.ID作为表名，根据文件类型自动加载数据
	tableSchema, err := duckdbTool.LoadFromKnowledge(ctx, resources.knowledge)
	if err != nil {
		logger.Errorf(ctx, "failed to load data into DuckDB: %v", err)
		return nil, err
	}

	logger.Infof(ctx, "Loaded table %s with %d columns and %d rows", tableSchema.TableName, len(tableSchema.Columns), tableSchema.RowCount)

	// 获取样本数据用于生成摘要
	input := tools.DataAnalysisInput{
		KnowledgeID: resources.knowledge.ID,
		Sql:         fmt.Sprintf("SELECT * FROM \"%s\" LIMIT 10", tableSchema.TableName),
	}
	jsonData, err := json.Marshal(input)
	if err != nil {
		logger.Errorf(ctx, "failed to marshal input: %v", err)
		return nil, err
	}
	sampleResult, err := duckdbTool.Execute(ctx, jsonData)
	if err != nil {
		logger.Errorf(ctx, "failed to get sample data: %v", err)
		return nil, err
	}

	// 构建共用的schema和样本数据描述
	schemaDesc := tableSchema.Description()
	sampleDesc := s.buildSampleDataDescription(sampleResult, 10)

	// 使用AI生成表格摘要和列描述
	customInstructions := ""
	if resources.knowledgeBase != nil {
		var processOverrides *types.KnowledgeProcessOverrides
		if resources.knowledge != nil {
			processOverrides, _ = resources.knowledge.ProcessOverrides()
		}
		customInstructions = ResolveProcessConfig(resources.knowledgeBase, processOverrides).ChunkingConfig.TableMetadataInstructions
	}
	tableDescription, err := s.generateTableDescription(ctx, resources.chatModel, tableSchema.TableName,
		schemaDesc, sampleDesc, customInstructions)
	if err != nil {
		logger.Errorf(ctx, "failed to generate table description: %v", err)
		return nil, err
	}
	logger.Debugf(ctx, "table describe of knowledge %s: %s", resources.knowledge.ID, tableDescription)

	columnDescription, err := s.generateColumnDescriptions(ctx, resources.chatModel, tableSchema.TableName,
		schemaDesc, sampleDesc, customInstructions)
	if err != nil {
		logger.Errorf(ctx, "failed to generate column descriptions: %v", err)
		return nil, err
	}
	logger.Debugf(ctx, "column describe of knowledge %s: %s", resources.knowledge.ID, columnDescription)

	// 构建chunks：一个表格摘要chunk + 多个列描述chunks
	chunks := s.buildChunks(resources, attempt, tableDescription, columnDescription)
	return chunks, nil
}

// buildChunks 构建chunk对象
// tableDescription和columnDescriptions分别生成一个chunk
func dataTableSummaryChunkID(knowledgeID string, attempt int, role string) string {
	seed := fmt.Sprintf("datatable-summary\x00%s\x00%d\x00%s", knowledgeID, attempt, role)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(seed)).String()
}

func (s *DataTableSummaryService) buildChunks(
	resources *extractionResources, attempt int, tableDescription string, columnDescription string,
) []*types.Chunk {
	chunks := make([]*types.Chunk, 0, 2)
	now := time.Now()

	// 表格摘要chunk
	summaryChunk := &types.Chunk{
		ID:              dataTableSummaryChunkID(resources.knowledge.ID, attempt, "summary"),
		TenantID:        resources.knowledge.TenantID,
		KnowledgeID:     resources.knowledge.ID,
		KnowledgeBaseID: resources.knowledge.KnowledgeBaseID,
		Content:         tableDescription,
		SourceContent:   tableDescription,
		ChunkIndex:      0,
		IsEnabled:       true,
		ChunkType:       types.ChunkTypeTableSummary,
		Status:          int(types.ChunkStatusStored),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	chunks = append(chunks, summaryChunk)

	// 列描述chunk（所有列的描述合并为一个chunk）
	columnChunk := &types.Chunk{
		ID:              dataTableSummaryChunkID(resources.knowledge.ID, attempt, "columns"),
		TenantID:        resources.knowledge.TenantID,
		KnowledgeID:     resources.knowledge.ID,
		KnowledgeBaseID: resources.knowledge.KnowledgeBaseID,
		Content:         columnDescription,
		SourceContent:   columnDescription,
		ChunkIndex:      1,
		IsEnabled:       true,
		ChunkType:       types.ChunkTypeTableColumn,
		ParentChunkID:   summaryChunk.ID,
		Status:          int(types.ChunkStatusStored),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	chunks = append(chunks, columnChunk)

	summaryChunk.NextChunkID = columnChunk.ID
	columnChunk.PreChunkID = summaryChunk.ID

	return chunks
}

// indexToVectorDB 将chunks索引到向量数据库
// 思路：批量构建索引信息，统一索引，更新状态
func (s *DataTableSummaryService) indexToVectorDB(
	ctx context.Context,
	knowledgeID string,
	attempt int,
	chunks []*types.Chunk,
	engine *retriever.CompositeRetrieveEngine,
	embedder embedding.Embedder,
) error {
	if err := s.requireCurrentAttempt(ctx, knowledgeID, attempt); err != nil {
		return err
	}
	// 构建索引信息列表
	indexInfoList := make([]*types.IndexInfo, 0, len(chunks))
	for _, chunk := range chunks {
		indexInfoList = append(indexInfoList, &types.IndexInfo{
			Content:         chunk.Content,
			SourceID:        chunk.ID,
			SourceType:      types.ChunkSourceType,
			ChunkID:         chunk.ID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			IsEnabled:       true,
		})
	}

	// Save with deterministic IDs so an Asynq retry updates the same two
	// artifacts instead of appending another summary/column pair.
	if err := s.saveTableSummaryChunks(ctx, chunks); err != nil {
		logger.Errorf(ctx, "failed to save data table summary chunks: %v", err)
		return err
	}
	logger.Infof(ctx, "Saved %d chunks for data table", len(chunks))
	if err := s.requireCurrentAttempt(ctx, knowledgeID, attempt); err != nil {
		return err
	}

	// 批量索引
	if engine == nil || embedder == nil {
		return fmt.Errorf("data table summary vector dependencies are unavailable")
	}
	if err := engine.BatchIndex(ctx, embedder, indexInfoList); err != nil {
		logger.Errorf(ctx, "failed to index chunks: %v", err)
		return err
	}
	if err := s.requireCurrentAttempt(ctx, knowledgeID, attempt); err != nil {
		return err
	}

	// 更新chunk状态为已索引
	for _, chunk := range chunks {
		chunk.Status = int(types.ChunkStatusIndexed)
	}
	if err := s.chunkService.UpdateChunks(ctx, chunks); err != nil {
		logger.Errorf(ctx, "failed to update chunk status: %v", err)
		return err
	}
	return s.requireCurrentAttempt(ctx, knowledgeID, attempt)
}

func (s *DataTableSummaryService) saveTableSummaryChunks(
	ctx context.Context, chunks []*types.Chunk,
) error {
	if len(chunks) == 0 {
		return nil
	}
	existing, err := s.chunkService.ListChunksByKnowledgeID(ctx, chunks[0].KnowledgeID)
	if err != nil {
		return err
	}
	existingByID := make(map[string]*types.Chunk, len(existing))
	for _, chunk := range existing {
		existingByID[chunk.ID] = chunk
	}
	toCreate := make([]*types.Chunk, 0, len(chunks))
	for _, chunk := range chunks {
		current := existingByID[chunk.ID]
		if current == nil {
			toCreate = append(toCreate, chunk)
			continue
		}
		chunk.SeqID = current.SeqID
		chunk.CreatedAt = current.CreatedAt
		chunk.DeletedAt = current.DeletedAt
		if err := s.chunkService.UpdateChunk(ctx, chunk); err != nil {
			return err
		}
	}
	if len(toCreate) == 0 {
		return nil
	}
	return s.chunkService.CreateChunks(ctx, toCreate)
}

// cleanupOnFailure marks the still-current attempt failed, disables its
// deterministic chunks for a safe retry, and removes only their vector IDs.
func (s *DataTableSummaryService) cleanupOnFailure(
	ctx context.Context,
	attempt int,
	resources *extractionResources,
	chunks []*types.Chunk,
	indexErr error,
) error {
	logger.Warnf(ctx, "Starting cleanup due to failure: %v", indexErr)
	if resources == nil || resources.knowledge == nil {
		return fmt.Errorf("data table summary cleanup resources are unavailable")
	}
	if err := s.requireCurrentAttempt(ctx, resources.knowledge.ID, attempt); err != nil {
		if errors.Is(err, errDataTableSummaryStale) {
			logger.Infof(ctx, "data table summary: skip stale failure cleanup knowledge=%s attempt=%d",
				resources.knowledge.ID, attempt)
			return nil
		}
		return err
	}
	if s.knowledgeService == nil {
		return fmt.Errorf("data table summary knowledge repository is unavailable")
	}
	repo := s.knowledgeService.GetRepository()
	updater, ok := repo.(interfaces.KnowledgeAttemptUpdater)
	if !ok {
		return fmt.Errorf("data table summary knowledge repository does not support attempt-aware updates")
	}
	updated, err := updater.UpdateKnowledgeColumnsForAttempt(
		ctx,
		resources.knowledge.ID,
		attempt,
		map[string]interface{}{
			"parse_status":  types.ParseStatusFailed,
			"error_message": indexErr.Error(),
			"updated_at":    time.Now(),
		},
	)
	if err != nil {
		return err
	}
	if !updated {
		logger.Infof(ctx, "data table summary: skip failure cleanup after ownership changed knowledge=%s attempt=%d",
			resources.knowledge.ID, attempt)
		return nil
	}

	chunkIDs := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		chunkIDs = append(chunkIDs, chunk.ID)
		chunk.IsEnabled = false
		chunk.Status = int(types.ChunkStatusStored)
	}
	var cleanupErr error
	if len(chunkIDs) > 0 {
		if err := s.chunkService.UpdateChunks(ctx, chunks); err != nil {
			logger.Errorf(ctx, "Failed to disable data table summary chunks: %v", err)
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	if len(chunkIDs) > 0 && resources.retrieveEngine != nil && resources.embeddingModel != nil {
		if err := resources.retrieveEngine.DeleteBySourceIDList(
			ctx, chunkIDs, resources.embeddingModel.GetDimensions(), types.KnowledgeBaseTypeDocument,
		); err != nil {
			logger.Errorf(ctx, "Failed to delete vector index: %v", err)
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

// generateTableDescription generates a summary description for the entire table
func (s *DataTableSummaryService) generateTableDescription(ctx context.Context, chatModel chat.Chat,
	tableName, schemaDesc, sampleDesc, customInstructions string,
) (string, error) {
	prompt := fmt.Sprintf(tableDescriptionPromptTemplate, tableName, schemaDesc, sampleDesc)
	prompt = types.AppendCustomPromptInstructions(prompt, customInstructions, "table_metadata")
	// logger.Debugf(ctx, "generateTableDescription prompt: %s", prompt)

	thinking := false
	response, err := chatModel.Chat(ctx, []chat.Message{
		{Role: "user", Content: prompt},
	}, &chat.ChatOptions{
		Temperature: 0.3,
		MaxTokens:   512,
		Thinking:    &thinking,
	})
	if err != nil {
		return "", fmt.Errorf("failed to generate table description: %w", err)
	}

	return fmt.Sprintf("# Table Summary\n\nTable name: %s\n\n%s", tableName, response.Content), nil
}

// generateColumnDescriptions generates descriptions for each column in batch
func (s *DataTableSummaryService) generateColumnDescriptions(ctx context.Context, chatModel chat.Chat,
	tableName, schemaDesc, sampleDesc, customInstructions string,
) (string, error) {
	// Build batch prompt for all columns
	prompt := fmt.Sprintf(columnDescriptionsPromptTemplate, tableName, schemaDesc, sampleDesc)
	prompt = types.AppendCustomPromptInstructions(prompt, customInstructions, "table_metadata")
	// logger.Debugf(ctx, "generateColumnDescriptions prompt: %s", prompt)

	// Call LLM once for all columns
	thinking := false
	response, err := chatModel.Chat(ctx, []chat.Message{
		{Role: "user", Content: prompt},
	}, &chat.ChatOptions{
		Temperature: 0.3,
		MaxTokens:   2048,
		Thinking:    &thinking,
	})
	if err != nil {
		return "", fmt.Errorf("failed to generate column descriptions: %w", err)
	}

	return fmt.Sprintf("# Table Column Information\n\nTable name: %s\n\n%s", tableName, response.Content), nil
}

// buildSampleDataDescription builds a formatted sample data description
func (s *DataTableSummaryService) buildSampleDataDescription(sampleData *types.ToolResult, maxRows int) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("Sample data (first %d rows):\n", maxRows))

	rows, ok := sampleData.Data["rows"].([]map[string]interface{})
	if !ok {
		return builder.String()
	}

	for i, row := range rows {
		if i >= maxRows {
			break
		}
		jsonBytes, err := json.Marshal(row)
		if err != nil {
			continue
		}
		builder.WriteString(string(jsonBytes))
		builder.WriteString("\n")
	}

	return builder.String()
}
