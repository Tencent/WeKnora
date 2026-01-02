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
	// tableDescriptionPromptTemplate 테이블 설명 생성 프롬프트 템플릿
	tableDescriptionPromptTemplate = `당신은 데이터 분석 전문가입니다. 다음 테이블 구조 정보와 데이터 샘플을 바탕으로 간결한 테이블 메타데이터 설명을 작성해 주세요(200-300자).

테이블명: %s

%s

%s

다음 차원에서 이 테이블을 설명해 주세요:
1. **데이터 주제**: 이 테이블은 어떤 유형의 데이터를 기록합니까? (예: 사용자 정보, 판매 기록, 로그 데이터 등)
2. **핵심 필드**: 가장 중요한 3-5개의 필드와 그 의미를 나열해 주세요.
3. **데이터 규모**: 총 행 수와 열 수
4. **비즈니스 시나리오**: 이 테이블은 어떤 비즈니스 분석이나 응용 시나리오에 사용될 수 있습니까?
5. **주요 특징**: 데이터의 두드러진 특징은 무엇입니까? (예: 지리적 위치 포함, 분류 태그 있음, 계층 구조 존재 등)

**중요 팁**:
- 구체적인 데이터 값이나 샘플 내용을 출력하지 마세요.
- 사용자가 필요한 정보가 포함되어 있는지 빠르게 판단할 수 있도록 개괄적인 설명을 사용하세요.
- 검색과 이해가 쉽도록 간결하고 전문적인 언어를 사용하세요.`

	// columnDescriptionsPromptTemplate 열 설명 생성 프롬프트 템플릿
	columnDescriptionsPromptTemplate = `당신은 데이터 분석 전문가입니다. 다음 테이블 구조 정보와 데이터 샘플을 바탕으로 각 열에 대한 구조화된 설명 정보를 생성해 주세요.

테이블명: %s

%s

%s

각 열에 대해 다음 정보를 포함하는 자세한 설명을 생성해 주세요:
1. **필드 의미**: 이 열은 어떤 정보를 저장합니까? (예: 사용자 ID, 주문 금액, 생성 시간 등)
2. **데이터 유형**: 데이터의 유형과 형식 (예: 정수, 문자열, 날짜/시간, 불리언 등)
3. **비즈니스 용도**: 비즈니스에서 이 필드의 역할 (예: 사용자 식별, 금액 계산, 시간 정렬 등)
4. **데이터 특징**: 데이터의 두드러진 특징 (예: 고유 식별자, null 허용, 열거형 값 있음, 단위 있음 등)

다음 형식에 따라 출력해 주세요(열당 하나의 단락):

**열 이름 1** (데이터 유형)
- 필드 의미: xxx
- 비즈니스 용도: xxx
- 데이터 특징: xxx

**열 이름 2** (데이터 유형)
- 필드 의미: xxx
- 비즈니스 용도: xxx
- 데이터 특징: xxx

**중요 팁**:
- 구체적인 데이터 값을 출력하지 말고 필드의 메타 정보만 설명하세요.
- 사용자가 이해하고 검색하기 쉽도록 명확한 비즈니스 용어를 사용하세요.
- 샘플 데이터에서 열거형 값 범위를 추론할 수 있는 경우 요약하여 설명할 수 있습니다(예: 상태 필드에는 대기 중/진행 중/완료 등 상태 포함).`
)

// NewChunkExtractTask 새로운 청크 추출 작업 생성
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

// NewTableExtractTask 새로운 테이블 추출 작업 생성
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

// ChunkExtractService 청크 추출 서비스
type ChunkExtractService struct {
	template          *types.PromptTemplateStructured
	modelService      interfaces.ModelService
	knowledgeBaseRepo interfaces.KnowledgeBaseRepository
	chunkRepo         interfaces.ChunkRepository
	graphEngine       interfaces.RetrieveGraphRepository
}

// NewChunkExtractService 새로운 청크 추출 서비스 생성
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

// Handle 청크 추출 작업 처리
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

// DataTableExtractPayload 테이블 추출 작업 페이로드
type DataTableSummaryPayload struct {
	TenantID       uint64 `json:"tenant_id"`
	KnowledgeID    string `json:"knowledge_id"`
	SummaryModel   string `json:"summary_model"`
	EmbeddingModel string `json:"embedding_model"`
}

// DataTableSummaryService 테이블 추출 서비스
type DataTableSummaryService struct {
	modelService     interfaces.ModelService
	knowledgeService interfaces.KnowledgeService
	chunkService     interfaces.ChunkService
	tenantService    interfaces.TenantService
	retrieveEngine   interfaces.RetrieveEngineRegistry
	sqlDB            *sql.DB
}

// NewDataTableSummaryService 새로운 DataTableSummaryService 생성
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

// Handle 테이블 추출을 위한 TaskHandler 인터페이스 구현
// 전체 흐름: 초기화 -> 리소스 준비 -> 데이터 로드 -> 요약 생성 -> 인덱스 생성
func (s *DataTableSummaryService) Handle(ctx context.Context, t *asynq.Task) error {
	// 1. 작업 파싱 및 컨텍스트 초기화
	var payload DataTableSummaryPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "failed to unmarshal table extract task payload: %v", err)
		return err
	}

	ctx = logger.WithRequestID(ctx, uuid.New().String())
	ctx = logger.WithField(ctx, "knowledge", payload.KnowledgeID)
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)

	logger.Infof(ctx, "Processing table extraction for knowledge: %s", payload.KnowledgeID)

	// 2. 모든 필수 리소스 준비 (지식, 모델, 엔진 등)
	resources, err := s.prepareResources(ctx, payload)
	if err != nil {
		return err
	}

	// 3. 테이블 데이터 로드 및 요약 생성
	chunks, err := s.processTableData(ctx, resources)
	if err != nil {
		return err
	}

	// 4. 벡터 데이터베이스에 인덱싱
	if err := s.indexToVectorDB(ctx, chunks, resources.retrieveEngine, resources.embeddingModel); err != nil {
		s.cleanupOnFailure(ctx, resources, chunks, err)
		return err
	}

	logger.Infof(ctx, "Table extraction completed for knowledge: %s", payload.KnowledgeID)
	return nil
}

// extractionResources 추출 과정에 필요한 모든 리소스 캡슐화
type extractionResources struct {
	knowledge      *types.Knowledge
	chatModel      chat.Chat
	embeddingModel embedding.Embedder
	retrieveEngine *retriever.CompositeRetrieveEngine
}

// prepareResources 추출에 필요한 모든 리소스 준비
// 아이디어: 모든 의존성을 중앙 집중식으로 로드하고 오류 처리를 통합하여 분산된 리소스 가져오기 로직 방지
func (s *DataTableSummaryService) prepareResources(ctx context.Context, payload DataTableSummaryPayload) (*extractionResources, error) {
	// 지식 파일 가져오기 및 검증
	knowledge, err := s.knowledgeService.GetKnowledgeByID(ctx, payload.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "failed to get knowledge: %v", err)
		return nil, err
	}

	// 파일 유형 검증
	fileType := strings.ToLower(knowledge.FileType)
	if fileType != "csv" && fileType != "xlsx" && fileType != "xls" {
		logger.Warnf(ctx, "knowledge %s is not a CSV or Excel file, skipping table summary", payload.KnowledgeID)
		return nil, fmt.Errorf("unsupported file type: %s", fileType)
	}

	// 테넌트 정보 가져오기
	tenantInfo, err := s.tenantService.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Errorf(ctx, "failed to get tenant: %v", err)
		return nil, err
	}

	// 채팅 모델 가져오기 (요약 생성용)
	chatModel, err := s.modelService.GetChatModel(ctx, payload.SummaryModel)
	if err != nil {
		logger.Errorf(ctx, "failed to get chat model: %v", err)
		return nil, err
	}

	// 임베딩 모델 가져오기 (벡터화용)
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, payload.EmbeddingModel)
	if err != nil {
		logger.Errorf(ctx, "failed to get embedding model: %v", err)
		return nil, err
	}

	// 검색 엔진 가져오기
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

// processTableData 테이블 데이터 처리: 로드 -> 분석 -> 요약 생성 -> 청크 생성
// 아이디어: 데이터 처리의 핵심 흐름을 한곳에 집중시켜 논리적 일관성 유지
func (s *DataTableSummaryService) processTableData(ctx context.Context, resources *extractionResources) ([]*types.Chunk, error) {
	// DuckDB 세션 생성 및 데이터 로드
	sessionID := fmt.Sprintf("table_summary_%s", resources.knowledge.ID)
	duckdbTool := tools.NewDataAnalysisTool(s.knowledgeService, s.sqlDB, sessionID)
	defer duckdbTool.Cleanup(ctx)

	// knowledge.ID를 테이블 이름으로 사용하여 파일 유형에 따라 데이터 자동 로드
	tableSchema, err := duckdbTool.LoadFromKnowledge(ctx, resources.knowledge)
	if err != nil {
		logger.Errorf(ctx, "failed to load data into DuckDB: %v", err)
		return nil, err
	}

	logger.Infof(ctx, "Loaded table %s with %d columns and %d rows", tableSchema.TableName, len(tableSchema.Columns), tableSchema.RowCount)

	// 요약 생성을 위한 샘플 데이터 가져오기
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

	// 공통 스키마 및 샘플 데이터 설명 구성
	schemaDesc := tableSchema.Description()
	sampleDesc := s.buildSampleDataDescription(sampleResult, 10)

	// AI를 사용하여 테이블 요약 및 열 설명 생성
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

	// 청크 구성: 하나의 테이블 요약 청크 + 여러 열 설명 청크
	chunks := s.buildChunks(resources, tableDescription, columnDescription)
	return chunks, nil
}

// buildChunks 청크 객체 생성
// tableDescription 및 columnDescriptions는 각각 하나의 청크를 생성합니다.
func (s *DataTableSummaryService) buildChunks(resources *extractionResources, tableDescription string, columnDescription string) []*types.Chunk {
	chunks := make([]*types.Chunk, 0, 2)

	// 테이블 요약 청크
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

	// 열 설명 청크 (모든 열 설명이 하나의 청크로 병합됨)
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

// indexToVectorDB 청크를 벡터 데이터베이스에 인덱싱
// 아이디어: 인덱스 정보를 일괄 구성하고, 통합 인덱싱 후 상태 업데이트
func (s *DataTableSummaryService) indexToVectorDB(
	ctx context.Context,
	chunks []*types.Chunk,
	engine *retriever.CompositeRetrieveEngine,
	embedder embedding.Embedder,
) error {
	// 인덱스 정보 목록 구성
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

	// 데이터베이스에 저장
	if err := s.chunkService.CreateChunks(ctx, chunks); err != nil {
		logger.Errorf(ctx, "failed to create chunks: %v", err)
		return err
	}
	logger.Infof(ctx, "Created %d chunks for data table", len(chunks))

	// 일괄 인덱싱
	if err := engine.BatchIndex(ctx, embedder, indexInfoList); err != nil {
		logger.Errorf(ctx, "failed to index chunks: %v", err)
		return err
	}

	// 청크 상태를 인덱싱됨으로 업데이트
	for _, chunk := range chunks {
		chunk.Status = int(types.ChunkStatusIndexed)
	}
	if err := s.chunkService.UpdateChunks(ctx, chunks); err != nil {
		logger.Errorf(ctx, "failed to update chunk status: %v", err)
		return err
	}

	return nil
}

// cleanupOnFailure 인덱싱 실패 시 정리 작업
// 아이디어: 더티 데이터 잔류를 방지하기 위해 생성된 청크 및 해당 벡터 인덱스 삭제
func (s *DataTableSummaryService) cleanupOnFailure(ctx context.Context, resources *extractionResources, chunks []*types.Chunk, indexErr error) {
	logger.Warnf(ctx, "Starting cleanup due to failure: %v", indexErr)

	// 1. 지식 상태를 실패로 업데이트
	resources.knowledge.ParseStatus = types.ParseStatusFailed
	resources.knowledge.ErrorMessage = indexErr.Error()
	if err := s.knowledgeService.UpdateKnowledge(ctx, resources.knowledge); err != nil {
		logger.Errorf(ctx, "Failed to update knowledge status: %v", err)
	} else {
		logger.Infof(ctx, "Updated knowledge %s status to failed", resources.knowledge.ID)
	}

	// 청크 ID 추출
	chunkIDs := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		chunkIDs = append(chunkIDs, chunk.ID)
	}

	// 생성된 청크 삭제
	if len(chunkIDs) > 0 {
		if err := s.chunkService.DeleteChunks(ctx, chunkIDs); err != nil {
			logger.Errorf(ctx, "Failed to delete chunks: %v", err)
		} else {
			logger.Infof(ctx, "Deleted %d chunks", len(chunkIDs))
		}
	}

	// 해당 벡터 인덱스 삭제
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

// generateTableDescription 테이블 전체에 대한 요약 설명 생성
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

	return fmt.Sprintf("# 테이블 요약\n\n테이블명: %s\n\n%s", tableName, response.Content), nil
}

// generateColumnDescriptions 각 열에 대한 설명을 일괄 생성
func (s *DataTableSummaryService) generateColumnDescriptions(ctx context.Context, chatModel chat.Chat, tableName, schemaDesc, sampleDesc string) (string, error) {
	// 모든 열에 대한 일괄 프롬프트 구성
	prompt := fmt.Sprintf(columnDescriptionsPromptTemplate, tableName, schemaDesc, sampleDesc)
	// logger.Debugf(ctx, "generateColumnDescriptions prompt: %s", prompt)

	// 모든 열에 대해 LLM 한 번 호출
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

	return fmt.Sprintf("# 테이블 열 정보\n\n테이블명: %s\n\n%s", tableName, response.Content), nil
}

// buildSampleDataDescription 형식이 지정된 샘플 데이터 설명 생성
func (s *DataTableSummaryService) buildSampleDataDescription(sampleData *types.ToolResult, maxRows int) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("상위 %d행 데이터 예시:\n", maxRows))

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
