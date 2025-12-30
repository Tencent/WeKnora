package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	chatpipline "github.com/Tencent/WeKnora/internal/application/service/chat_pipline"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

const (
	// tableDescriptionPromptTemplate 表格描述生成的 prompt 模板
	tableDescriptionPromptTemplate = `你是一个数据分析专家。请根据以下表格的结构信息和数据样本，生成一段简洁的表格元数据描述（200-300字）。

表名: %s

%s

%s

请从以下维度描述这个表格：
1. **数据主题**：这个表格记录的是什么类型的数据？（如：用户信息、销售记录、日志数据等）
2. **核心字段**：列出3-5个最重要的字段及其含义
3. **数据规模**：总行数和列数
4. **业务场景**：这个表格可能用于什么业务分析或应用场景？
5. **关键特征**：数据有什么显著特点？（如：包含地理位置、有分类标签、存在层级关系等）

**重要提示**：
- 不要输出具体的数据值或样本内容
- 使用概括性的描述，让用户能快速判断这个表格是否包含他们需要的信息
- 用简洁专业的语言，便于检索和理解`

	// columnDescriptionsPromptTemplate 列描述生成的 prompt 模板
	columnDescriptionsPromptTemplate = `你是一个数据分析专家。请根据以下表格的结构信息和数据样本，为每一列生成结构化的描述信息。

表名: %s

%s

%s

请为每一列生成详细的描述，包含以下信息：
1. **字段含义**：这一列存储的是什么信息？（如：用户ID、订单金额、创建时间等）
2. **数据类型**：数据的类型和格式（如：整数、字符串、日期时间、布尔值等）
3. **业务用途**：这个字段在业务中的作用（如：用于用户识别、金额计算、时间排序等）
4. **数据特征**：数据的显著特点（如：唯一标识、可为空、有枚举值、有单位等）

请按以下格式输出（每列一个段落）：

**列名1** (数据类型)
- 字段含义：xxx
- 业务用途：xxx
- 数据特征：xxx

**列名2** (数据类型)
- 字段含义：xxx
- 业务用途：xxx
- 数据特征：xxx

**重要提示**：
- 不要输出具体的数据值，只描述字段的元信息
- 使用清晰的业务术语，便于用户理解和搜索
- 如果从样本数据中能推断出枚举值范围，可以概括说明（如：状态字段包含待处理/进行中/已完成等状态）`
)

// NewChunkExtractTask creates a new chunk extract task
func NewChunkExtractTask(
	ctx context.Context,
	client *asynq.Client,
	tenantID uint64,
	chunkID string,
	modelID string,
) error {
	if strings.ToLower(os.Getenv("NEO4J_ENABLE")) != "true" {
		logger.Warn(ctx, "NEO4J is not enabled, skip chunk extract task")
		return nil
	}
	payload, err := json.Marshal(types.ExtractChunkPayload{
		TenantID: tenantID,
		ChunkID:  chunkID,
		ModelID:  modelID,
	})
	if err != nil {
		return err
	}
	task := asynq.NewTask(types.TypeChunkExtract, payload, asynq.MaxRetry(3))
	info, err := client.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "failed to enqueue task: %v", err)
		return fmt.Errorf("failed to enqueue task: %v", err)
	}
	logger.Infof(ctx, "enqueued task: id=%s queue=%s chunk=%s", info.ID, info.Queue, chunkID)
	return nil
}

// NewTableExtractTask creates a new table extract task
func NewDataTableSummaryTask(
	ctx context.Context,
	client *asynq.Client,
	tenantID uint64,
	knowledgeID string,
	summaryModel string,
	embeddingModel string,
) error {
	payload, err := json.Marshal(DataTableSummaryPayload{
		TenantID:       tenantID,
		KnowledgeID:    knowledgeID,
		SummaryModel:   summaryModel,
		EmbeddingModel: embeddingModel,
	})
	if err != nil {
		return err
	}
	task := asynq.NewTask(types.TypeDataTableSummary, payload, asynq.MaxRetry(3))
	info, err := client.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "failed to enqueue data table summary task: %v", err)
		return fmt.Errorf("failed to enqueue data table summary task: %v", err)
	}
	logger.Infof(ctx, "enqueued data table summary task: id=%s queue=%s knowledge=%s",
		info.ID, info.Queue, knowledgeID)
	return nil
}

// ChunkExtractService is a service for extracting chunks
type ChunkExtractService struct {
	template          *types.PromptTemplateStructured
	modelService      interfaces.ModelService
	knowledgeBaseRepo interfaces.KnowledgeBaseRepository
	chunkRepo         interfaces.ChunkRepository
	graphEngine       interfaces.RetrieveGraphRepository
}

// NewChunkExtractService creates a new chunk extract service
func NewChunkExtractService(
	config *config.Config,
	modelService interfaces.ModelService,
	knowledgeBaseRepo interfaces.KnowledgeBaseRepository,
	chunkRepo interfaces.ChunkRepository,
	graphEngine interfaces.RetrieveGraphRepository,
) interfaces.TaskHandler {
	// generator := chatpipline.NewQAPromptGenerator(chatpipline.NewFormater(), config.ExtractManager.ExtractGraph)
	// ctx := context.Background()
	// logger.Debugf(ctx, "chunk extract system prompt: %s", generator.System(ctx))
	// logger.Debugf(ctx, "chunk extract user prompt: %s", generator.User(ctx, "demo"))
	return &ChunkExtractService{
		template:          config.ExtractManager.ExtractGraph,
		modelService:      modelService,
		knowledgeBaseRepo: knowledgeBaseRepo,
		chunkRepo:         chunkRepo,
		graphEngine:       graphEngine,
	}
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

	chunk, err := s.chunkRepo.GetChunkByID(ctx, p.TenantID, p.ChunkID)
	if err != nil {
		logger.Errorf(ctx, "failed to get chunk: %v", err)
		return err
	}
	kb, err := s.knowledgeBaseRepo.GetKnowledgeBaseByID(ctx, chunk.KnowledgeBaseID)
	if err != nil {
		logger.Errorf(ctx, "failed to get knowledge base: %v", err)
		return err
	}
	if kb.ExtractConfig == nil {
		logger.Warnf(ctx, "failed to get extract config")
		return err
	}

	chatModel, err := s.modelService.GetChatModel(ctx, p.ModelID)
	if err != nil {
		logger.Errorf(ctx, "failed to get chat model: %v", err)
		return err
	}

	template := &types.PromptTemplateStructured{
		Description: s.template.Description,
		Tags:        kb.ExtractConfig.Tags,
		Examples: []types.GraphData{
			{
				Text:     kb.ExtractConfig.Text,
				Node:     kb.ExtractConfig.Nodes,
				Relation: kb.ExtractConfig.Relations,
			},
		},
	}
	extractor := chatpipline.NewExtractor(chatModel, template)
	graph, err := extractor.Extract(ctx, chunk.Content)
	if err != nil {
		return err
	}

	chunk, err = s.chunkRepo.GetChunkByID(ctx, p.TenantID, p.ChunkID)
	if err != nil {
		logger.Warnf(ctx, "graph ignore chunk %s: %v", p.ChunkID, err)
		return nil
	}

	for _, node := range graph.Node {
		node.Chunks = []string{chunk.ID}
	}
	if err = s.graphEngine.AddGraph(ctx,
		types.NameSpace{KnowledgeBase: chunk.KnowledgeBaseID, Knowledge: chunk.KnowledgeID},
		[]*types.GraphData{graph},
	); err != nil {
		logger.Errorf(ctx, "failed to add graph: %v", err)
		return err
	}
	return nil
}

// DataTableExtractPayload represents the table extract task payload
type DataTableSummaryPayload struct {
	TenantID       uint64 `json:"tenant_id"`
	KnowledgeID    string `json:"knowledge_id"`
	SummaryModel   string `json:"summary_model"`
	EmbeddingModel string `json:"embedding_model"`
}

// DataTableSummaryService is a service for extracting tables
type DataTableSummaryService struct {
	modelService     interfaces.ModelService
	knowledgeService interfaces.KnowledgeService
	chunkService     interfaces.ChunkService
	tenantService    interfaces.TenantService
	retrieveEngine   interfaces.RetrieveEngineRegistry
	sqlDB            *sql.DB
}

// NewDataTableSummaryService creates a new DataTableSummaryService
func NewDataTableSummaryService(
	modelService interfaces.ModelService,
	knowledgeService interfaces.KnowledgeService,
	chunkService interfaces.ChunkService,
	tenantService interfaces.TenantService,
	retrieveEngine interfaces.RetrieveEngineRegistry,
	sqlDB *sql.DB,
) interfaces.TaskHandler {
	return &DataTableSummaryService{
		modelService:     modelService,
		knowledgeService: knowledgeService,
		chunkService:     chunkService,
		tenantService:    tenantService,
		retrieveEngine:   retrieveEngine,
		sqlDB:            sqlDB,
	}
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

	logger.Infof(ctx, "Processing table extraction for knowledge: %s", payload.KnowledgeID)

	// 2. 准备所有必需的资源（知识、模型、引擎等）
	resources, err := s.prepareResources(ctx, payload)
	if err != nil {
		return err
	}

	// 3. 加载表格数据并生成摘要
	chunks, err := s.processTableData(ctx, resources)
	if err != nil {
		return err
	}

	// 4. 索引到向量数据库
	if err := s.indexToVectorDB(ctx, chunks, resources.retrieveEngine, resources.embeddingModel); err != nil {
		s.cleanupOnFailure(ctx, resources, chunks, err)
		return err
	}

	logger.Infof(ctx, "Table extraction completed for knowledge: %s", payload.KnowledgeID)
	return nil
}

// extractionResources 封装提取过程所需的所有资源
type extractionResources struct {
	knowledge      *types.Knowledge
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

	// 获取租户信息
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

	// 获取检索引擎
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
	if err != nil {
		logger.Errorf(ctx, "failed to get retrieve engine: %v", err)
		return nil, err
	}

	return &extractionResources{
		knowledge:      knowledge,
		chatModel:      chatModel,
		embeddingModel: embeddingModel,
		retrieveEngine: retrieveEngine,
	}, nil
}

// processTableData 处理表格数据：加载 -> 分析 -> 生成摘要 -> 创建chunks
// 思路：将数据处理的核心流程集中在一起，保持逻辑连贯性
func (s *DataTableSummaryService) processTableData(ctx context.Context, resources *extractionResources) ([]*types.Chunk, error) {
	// 创建DuckDB会话并加载数据
	sessionID := fmt.Sprintf("table_summary_%s", resources.knowledge.ID)
	duckdbTool := tools.NewDataAnalysisTool(s.knowledgeService, s.sqlDB, sessionID)
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
	tableDescription, err := s.generateTableDescription(ctx, resources.chatModel, tableSchema.TableName, schemaDesc, sampleDesc)
	if err != nil {
		logger.Errorf(ctx, "failed to generate table description: %v", err)
		return nil, err
	}
	logger.Debugf(ctx, "table describe of knowledge %s: %s", resources.knowledge.ID, tableDescription)

	columnDescription, err := s.generateColumnDescriptions(ctx, resources.chatModel, tableSchema.TableName, schemaDesc, sampleDesc)
	if err != nil {
		logger.Errorf(ctx, "failed to generate column descriptions: %v", err)
		return nil, err
	}
	logger.Debugf(ctx, "column describe of knowledge %s: %s", resources.knowledge.ID, columnDescription)

	// 构建chunks：一个表格摘要chunk + 多个列描述chunks
	chunks := s.buildChunks(resources, tableDescription, columnDescription)
	return chunks, nil
}

// buildChunks 构建chunk对象
// tableDescription和columnDescriptions分别生成一个chunk
func (s *DataTableSummaryService) buildChunks(resources *extractionResources, tableDescription string, columnDescription string) []*types.Chunk {
	chunks := make([]*types.Chunk, 0, 2)

	// 表格摘要chunk
	summaryChunk := &types.Chunk{
		ID:              uuid.New().String(),
		TenantID:        resources.knowledge.TenantID,
		KnowledgeID:     resources.knowledge.ID,
		KnowledgeBaseID: resources.knowledge.KnowledgeBaseID,
		Content:         tableDescription,
		ChunkIndex:      0,
		IsEnabled:       true,
		ChunkType:       types.ChunkTypeTableSummary,
		Status:          int(types.ChunkStatusStored),
	}
	chunks = append(chunks, summaryChunk)

	// 列描述chunk（所有列的描述合并为一个chunk）
	columnChunk := &types.Chunk{
		ID:              uuid.New().String(),
		TenantID:        resources.knowledge.TenantID,
		KnowledgeID:     resources.knowledge.ID,
		KnowledgeBaseID: resources.knowledge.KnowledgeBaseID,
		Content:         columnDescription,
		ChunkIndex:      1,
		IsEnabled:       true,
		ChunkType:       types.ChunkTypeTableColumn,
		ParentChunkID:   summaryChunk.ID,
		Status:          int(types.ChunkStatusStored),
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
	chunks []*types.Chunk,
	engine *retriever.CompositeRetrieveEngine,
	embedder embedding.Embedder,
) error {
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
		})
	}

	// 保存到数据库
	if err := s.chunkService.CreateChunks(ctx, chunks); err != nil {
		logger.Errorf(ctx, "failed to create chunks: %v", err)
		return err
	}
	logger.Infof(ctx, "Created %d chunks for data table", len(chunks))

	// 批量索引
	if err := engine.BatchIndex(ctx, embedder, indexInfoList); err != nil {
		logger.Errorf(ctx, "failed to index chunks: %v", err)
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

	return nil
}

// cleanupOnFailure 索引失败时的清理工作
// 思路：删除已创建的chunk和对应的向量索引，避免脏数据残留
func (s *DataTableSummaryService) cleanupOnFailure(ctx context.Context, resources *extractionResources, chunks []*types.Chunk, indexErr error) {
	logger.Warnf(ctx, "Starting cleanup due to failure: %v", indexErr)

	// 1. 更新知识状态为失败
	resources.knowledge.ParseStatus = types.ParseStatusFailed
	resources.knowledge.ErrorMessage = indexErr.Error()
	if err := s.knowledgeService.UpdateKnowledge(ctx, resources.knowledge); err != nil {
		logger.Errorf(ctx, "Failed to update knowledge status: %v", err)
	} else {
		logger.Infof(ctx, "Updated knowledge %s status to failed", resources.knowledge.ID)
	}

	// 提取chunk IDs
	chunkIDs := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		chunkIDs = append(chunkIDs, chunk.ID)
	}

	// 删除已创建的chunks
	if len(chunkIDs) > 0 {
		if err := s.chunkService.DeleteChunks(ctx, chunkIDs); err != nil {
			logger.Errorf(ctx, "Failed to delete chunks: %v", err)
		} else {
			logger.Infof(ctx, "Deleted %d chunks", len(chunkIDs))
		}
	}

	// 删除对应的向量索引
	if len(chunkIDs) > 0 {
		if err := resources.retrieveEngine.DeleteBySourceIDList(
			ctx, chunkIDs, resources.embeddingModel.GetDimensions(), types.KnowledgeBaseTypeDocument,
		); err != nil {
			logger.Errorf(ctx, "Failed to delete vector index: %v", err)
		} else {
			logger.Infof(ctx, "Deleted vector index for %d chunks", len(chunkIDs))
		}
	}

	logger.Infof(ctx, "Cleanup completed")
}

// generateTableDescription generates a summary description for the entire table
func (s *DataTableSummaryService) generateTableDescription(ctx context.Context, chatModel chat.Chat, tableName, schemaDesc, sampleDesc string) (string, error) {
	prompt := fmt.Sprintf(tableDescriptionPromptTemplate, tableName, schemaDesc, sampleDesc)
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

	return fmt.Sprintf("# 表格摘要\n\n表名: %s\n\n%s", tableName, response.Content), nil
}

// generateColumnDescriptions generates descriptions for each column in batch
func (s *DataTableSummaryService) generateColumnDescriptions(ctx context.Context, chatModel chat.Chat, tableName, schemaDesc, sampleDesc string) (string, error) {
	// Build batch prompt for all columns
	prompt := fmt.Sprintf(columnDescriptionsPromptTemplate, tableName, schemaDesc, sampleDesc)
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

	return fmt.Sprintf("# 表格列信息\n\n表名: %s\n\n%s", tableName, response.Content), nil
}

// buildSampleDataDescription builds a formatted sample data description
func (s *DataTableSummaryService) buildSampleDataDescription(sampleData *types.ToolResult, maxRows int) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("前%d行数据示例:\n", maxRows))

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
