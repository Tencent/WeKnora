package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// ErrInvalidTenantID 테넌트 ID가 유효하지 않음을 나타내는 오류
var ErrInvalidTenantID = errors.New("유효하지 않은 테넌트 ID")

// knowledgeBaseService 지식베이스 서비스 인터페이스 구현
type knowledgeBaseService struct {
	repo           interfaces.KnowledgeBaseRepository
	kgRepo         interfaces.KnowledgeRepository
	chunkRepo      interfaces.ChunkRepository
	modelService   interfaces.ModelService
	retrieveEngine interfaces.RetrieveEngineRegistry
	tenantRepo     interfaces.TenantRepository
	fileSvc        interfaces.FileService
	graphEngine    interfaces.RetrieveGraphRepository
	asynqClient    *asynq.Client
}

// NewKnowledgeBaseService 새로운 지식베이스 서비스 생성
func NewKnowledgeBaseService(repo interfaces.KnowledgeBaseRepository,
	kgRepo interfaces.KnowledgeRepository,
	chunkRepo interfaces.ChunkRepository,
	modelService interfaces.ModelService,
	retrieveEngine interfaces.RetrieveEngineRegistry,
	tenantRepo interfaces.TenantRepository,
	fileSvc interfaces.FileService,
	graphEngine interfaces.RetrieveGraphRepository,
	asynqClient *asynq.Client,
) interfaces.KnowledgeBaseService {
	return &knowledgeBaseService{
		repo:           repo,
		kgRepo:         kgRepo,
		chunkRepo:      chunkRepo,
		modelService:   modelService,
		retrieveEngine: retrieveEngine,
		tenantRepo:     tenantRepo,
		fileSvc:        fileSvc,
		graphEngine:    graphEngine,
		asynqClient:    asynqClient,
	}
}

// GetRepository 지식베이스 리포지토리 반환
// Parameters:
//   - ctx: 인증 및 요청 정보를 포함한 컨텍스트
//
// Returns:
//   - interfaces.KnowledgeBaseRepository: 지식베이스 리포지토리
func (s *knowledgeBaseService) GetRepository() interfaces.KnowledgeBaseRepository {
	return s.repo
}

// CreateKnowledgeBase 새로운 지식베이스 생성
func (s *knowledgeBaseService) CreateKnowledgeBase(ctx context.Context,
	kb *types.KnowledgeBase,
) (*types.KnowledgeBase, error) {
	// UUID 생성 및 생성 타임스탬프 설정
	if kb.ID == "" {
		kb.ID = uuid.New().String()
	}
	kb.CreatedAt = time.Now()
	kb.TenantID = ctx.Value(types.TenantIDContextKey).(uint64)
	kb.UpdatedAt = time.Now()
	kb.EnsureDefaults()

	logger.Infof(ctx, "Creating knowledge base, ID: %s, tenant ID: %d, name: %s", kb.ID, kb.TenantID, kb.Name)

	if err := s.repo.CreateKnowledgeBase(ctx, kb); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id": kb.ID,
			"tenant_id":         kb.TenantID,
		})
		return nil, err
	}

	logger.Infof(ctx, "Knowledge base created successfully, ID: %s, name: %s", kb.ID, kb.Name)
	return kb, nil
}

// GetKnowledgeBaseByID ID로 지식베이스 검색
func (s *knowledgeBaseService) GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error) {
	if id == "" {
		logger.Error(ctx, "Knowledge base ID is empty")
		return nil, errors.New("knowledge base ID cannot be empty")
	}

	kb, err := s.repo.GetKnowledgeBaseByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id": id,
		})
		return nil, err
	}

	kb.EnsureDefaults()
	return kb, nil
}

// ListKnowledgeBases 테넌트의 모든 지식베이스 반환
func (s *knowledgeBaseService) ListKnowledgeBases(ctx context.Context) ([]*types.KnowledgeBase, error) {
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	kbs, err := s.repo.ListKnowledgeBasesByTenantID(ctx, tenantID)
	if err != nil {
		for _, kb := range kbs {
			kb.EnsureDefaults()
		}

		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": tenantID,
		})
		return nil, err
	}

	// 각 지식베이스의 지식 수와 청크 수 조회
	for _, kb := range kbs {
		kb.EnsureDefaults()

		// 지식 수 가져오기
		switch kb.Type {
		case types.KnowledgeBaseTypeDocument:
			knowledgeCount, err := s.kgRepo.CountKnowledgeByKnowledgeBaseID(ctx, tenantID, kb.ID)
			if err != nil {
				logger.Warnf(ctx, "Failed to get knowledge count for knowledge base %s: %v", kb.ID, err)
			} else {
				kb.KnowledgeCount = knowledgeCount
			}
		case types.KnowledgeBaseTypeFAQ:
			// 청크 수 가져오기
			chunkCount, err := s.chunkRepo.CountChunksByKnowledgeBaseID(ctx, tenantID, kb.ID)
			if err != nil {
				logger.Warnf(ctx, "Failed to get chunk count for knowledge base %s: %v", kb.ID, err)
			} else {
				kb.ChunkCount = chunkCount
			}
		}

		// 처리 중인 가져오기 작업이 있는지 확인
		processingCount, err := s.kgRepo.CountKnowledgeByStatus(
			ctx,
			tenantID,
			kb.ID,
			[]string{"pending", "processing"},
		)
		if err != nil {
			logger.Warnf(ctx, "Failed to check processing status for knowledge base %s: %v", kb.ID, err)
		} else {
			kb.IsProcessing = processingCount > 0
			kb.ProcessingCount = processingCount
		}
	}
	return kbs, nil
}

// UpdateKnowledgeBase 지식베이스 속성 업데이트
func (s *knowledgeBaseService) UpdateKnowledgeBase(ctx context.Context,
	id string,
	name string,
	description string,
	config *types.KnowledgeBaseConfig,
) (*types.KnowledgeBase, error) {
	if id == "" {
		logger.Error(ctx, "Knowledge base ID is empty")
		return nil, errors.New("knowledge base ID cannot be empty")
	}

	logger.Infof(ctx, "Updating knowledge base, ID: %s, name: %s", id, name)

	// 기존 지식베이스 가져오기
	kb, err := s.repo.GetKnowledgeBaseByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id": id,
		})
		return nil, err
	}

	// 지식베이스 속성 업데이트
	kb.Name = name
	kb.Description = description
	kb.ChunkingConfig = config.ChunkingConfig
	kb.ImageProcessingConfig = config.ImageProcessingConfig
	// FAQ 구성이 제공된 경우 업데이트
	if config.FAQConfig != nil {
		kb.FAQConfig = config.FAQConfig
	}
	kb.UpdatedAt = time.Now()
	kb.EnsureDefaults()

	logger.Info(ctx, "Saving knowledge base update")
	if err := s.repo.UpdateKnowledgeBase(ctx, kb); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id": id,
		})
		return nil, err
	}

	logger.Infof(ctx, "Knowledge base updated successfully, ID: %s, name: %s", kb.ID, kb.Name)
	return kb, nil
}

// DeleteKnowledgeBase ID로 지식베이스 삭제
// 이 메서드는 지식베이스를 삭제된 것으로 표시하고 비동기 작업을 대기열에 추가하여
// 대규모 정리 작업(임베딩, 청크, 파일, 그래프 데이터)을 처리합니다.
func (s *knowledgeBaseService) DeleteKnowledgeBase(ctx context.Context, id string) error {
	if id == "" {
		logger.Error(ctx, "Knowledge base ID is empty")
		return errors.New("knowledge base ID cannot be empty")
	}

	logger.Infof(ctx, "Deleting knowledge base, ID: %s", id)

	// 컨텍스트에서 테넌트 ID 가져오기
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)

	// 1단계: 데이터베이스에서 지식베이스 레코드 먼저 삭제 (삭제된 것으로 표시)
	logger.Infof(ctx, "Deleting knowledge base from database")
	err := s.repo.DeleteKnowledgeBase(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id": id,
		})
		return err
	}

	// 2단계: 대규모 정리 작업을 위한 비동기 작업 대기열에 추가
	payload := types.KBDeletePayload{
		TenantID:         tenantID,
		KnowledgeBaseID:  id,
		EffectiveEngines: tenantInfo.GetEffectiveEngines(),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		logger.Warnf(ctx, "Failed to marshal KB delete payload: %v", err)
		// KB 레코드가 이미 삭제되었으므로 요청을 실패시키지 않음
		return nil
	}

	task := asynq.NewTask(types.TypeKBDelete, payloadBytes, asynq.Queue("low"), asynq.MaxRetry(3))
	info, err := s.asynqClient.Enqueue(task)
	if err != nil {
		logger.Warnf(ctx, "Failed to enqueue KB delete task: %v", err)
		// KB 레코드가 이미 삭제되었으므로 요청을 실패시키지 않음
		return nil
	}

	logger.Infof(ctx, "KB delete task enqueued: %s, knowledge base ID: %s", info.ID, id)
	logger.Infof(ctx, "Knowledge base deleted successfully, ID: %s", id)
	return nil
}

// ProcessKBDelete 비동기 지식베이스 삭제 작업 처리
// 이 메서드는 대규모 정리 작업을 수행합니다: 임베딩, 청크, 파일 및 그래프 데이터 삭제
func (s *knowledgeBaseService) ProcessKBDelete(ctx context.Context, t *asynq.Task) error {
	var payload types.KBDeletePayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "Failed to unmarshal KB delete payload: %v", err)
		return err
	}

	tenantID := payload.TenantID
	kbID := payload.KnowledgeBaseID

	// 다운스트림 서비스를 위한 테넌트 컨텍스트 설정
	ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)

	logger.Infof(ctx, "Processing KB delete task for knowledge base: %s", kbID)

	// 1단계: 이 지식베이스의 모든 지식 항목 가져오기
	logger.Infof(ctx, "Fetching all knowledge entries in knowledge base, ID: %s", kbID)
	knowledgeList, err := s.kgRepo.ListKnowledgeByKnowledgeBaseID(ctx, tenantID, kbID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id": kbID,
		})
		return err
	}
	logger.Infof(ctx, "Found %d knowledge entries to delete", len(knowledgeList))

	// 2단계: 모든 지식 항목 및 관련 리소스 삭제
	if len(knowledgeList) > 0 {
		knowledgeIDs := make([]string, 0, len(knowledgeList))
		for _, knowledge := range knowledgeList {
			knowledgeIDs = append(knowledgeIDs, knowledge.ID)
		}

		logger.Infof(ctx, "Deleting all knowledge entries and their resources")

		// 벡터 저장소에서 임베딩 삭제
		logger.Infof(ctx, "Deleting embeddings from vector store")
		retrieveEngine, err := retriever.NewCompositeRetrieveEngine(
			s.retrieveEngine,
			payload.EffectiveEngines,
		)
		if err != nil {
			logger.Warnf(ctx, "Failed to create retrieve engine: %v", err)
		} else {
			// 임베딩 모델 및 유형별로 지식 그룹화
			type groupKey struct {
				EmbeddingModelID string
				Type             string
			}
			embeddingGroups := make(map[groupKey][]string)
			for _, knowledge := range knowledgeList {
				key := groupKey{EmbeddingModelID: knowledge.EmbeddingModelID, Type: knowledge.Type}
				embeddingGroups[key] = append(embeddingGroups[key], knowledge.ID)
			}

			for key, knowledgeGroup := range embeddingGroups {
				embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, key.EmbeddingModelID)
				if err != nil {
					logger.Warnf(ctx, "Failed to get embedding model %s: %v", key.EmbeddingModelID, err)
					continue
				}
				if err := retrieveEngine.DeleteByKnowledgeIDList(ctx, knowledgeGroup, embeddingModel.GetDimensions(), key.Type); err != nil {
					logger.Warnf(ctx, "Failed to delete embeddings for model %s: %v", key.EmbeddingModelID, err)
				}
			}
		}

		// 모든 청크 삭제
		logger.Infof(ctx, "Deleting all chunks in knowledge base")
		for _, knowledgeID := range knowledgeIDs {
			if err := s.chunkRepo.DeleteChunksByKnowledgeID(ctx, tenantID, knowledgeID); err != nil {
				logger.Warnf(ctx, "Failed to delete chunks for knowledge %s: %v", knowledgeID, err)
			}
		}

		// 물리적 파일 삭제 및 스토리지 조정
		logger.Infof(ctx, "Deleting physical files")
		storageAdjust := int64(0)
		for _, knowledge := range knowledgeList {
			if knowledge.FilePath != "" {
				if err := s.fileSvc.DeleteFile(ctx, knowledge.FilePath); err != nil {
					logger.Warnf(ctx, "Failed to delete file %s: %v", knowledge.FilePath, err)
				}
			}
			storageAdjust -= knowledge.StorageSize
		}
		if storageAdjust != 0 {
			if err := s.tenantRepo.AdjustStorageUsed(ctx, tenantID, storageAdjust); err != nil {
				logger.Warnf(ctx, "Failed to adjust tenant storage: %v", err)
			}
		}

		// 지식 그래프 데이터 삭제
		logger.Infof(ctx, "Deleting knowledge graph data")
		namespaces := make([]types.NameSpace, 0, len(knowledgeList))
		for _, knowledge := range knowledgeList {
			namespaces = append(namespaces, types.NameSpace{
				KnowledgeBase: knowledge.KnowledgeBaseID,
				Knowledge:     knowledge.ID,
			})
		}
		if s.graphEngine != nil && len(namespaces) > 0 {
			if err := s.graphEngine.DelGraph(ctx, namespaces); err != nil {
				logger.Warnf(ctx, "Failed to delete knowledge graph: %v", err)
			}
		}

		// 데이터베이스에서 모든 지식 항목 삭제
		logger.Infof(ctx, "Deleting knowledge entries from database")
		if err := s.kgRepo.DeleteKnowledgeList(ctx, tenantID, knowledgeIDs); err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"knowledge_base_id": kbID,
			})
			return err
		}
	}

	logger.Infof(ctx, "KB delete task completed successfully, knowledge base ID: %s", kbID)
	return nil
}

// SetEmbeddingModel 지식베이스의 임베딩 모델 설정
func (s *knowledgeBaseService) SetEmbeddingModel(ctx context.Context, id string, modelID string) error {
	if id == "" {
		logger.Error(ctx, "Knowledge base ID is empty")
		return errors.New("knowledge base ID cannot be empty")
	}

	if modelID == "" {
		logger.Error(ctx, "Model ID is empty")
		return errors.New("model ID cannot be empty")
	}

	logger.Infof(ctx, "Setting embedding model for knowledge base, knowledge base ID: %s, model ID: %s", id, modelID)

	// 지식베이스 가져오기
	kb, err := s.repo.GetKnowledgeBaseByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id": id,
		})
		return err
	}

	// 지식베이스의 임베딩 모델 업데이트
	kb.EmbeddingModelID = modelID
	kb.UpdatedAt = time.Now()

	logger.Info(ctx, "Saving knowledge base embedding model update")
	err = s.repo.UpdateKnowledgeBase(ctx, kb)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id":  id,
			"embedding_model_id": modelID,
		})
		return err
	}

	logger.Infof(
		ctx,
		"Knowledge base embedding model set successfully, knowledge base ID: %s, model ID: %s",
		id,
		modelID,
	)
	return nil
}

// CopyKnowledgeBase 지식베이스를 새 지식베이스로 복사
// 얕은 복사
func (s *knowledgeBaseService) CopyKnowledgeBase(ctx context.Context,
	srcKB string, dstKB string,
) (*types.KnowledgeBase, *types.KnowledgeBase, error) {
	sourceKB, err := s.repo.GetKnowledgeBaseByID(ctx, srcKB)
	if err != nil {
		logger.Errorf(ctx, "Get source knowledge base failed: %v", err)
		return nil, nil, err
	}
	sourceKB.EnsureDefaults()
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	var targetKB *types.KnowledgeBase
	if dstKB != "" {
		targetKB, err = s.repo.GetKnowledgeBaseByID(ctx, dstKB)
		if err != nil {
			return nil, nil, err
		}
	} else {
		var faqConfig *types.FAQConfig
		if sourceKB.FAQConfig != nil {
			cfg := *sourceKB.FAQConfig
			faqConfig = &cfg
		}
		targetKB = &types.KnowledgeBase{
			ID:                    uuid.New().String(),
			Name:                  sourceKB.Name,
			Type:                  sourceKB.Type,
			Description:           sourceKB.Description,
			TenantID:              tenantID,
			ChunkingConfig:        sourceKB.ChunkingConfig,
			ImageProcessingConfig: sourceKB.ImageProcessingConfig,
			EmbeddingModelID:      sourceKB.EmbeddingModelID,
			SummaryModelID:        sourceKB.SummaryModelID,
			VLMConfig:             sourceKB.VLMConfig,
			StorageConfig:         sourceKB.StorageConfig,
			FAQConfig:             faqConfig,
		}
		targetKB.EnsureDefaults()
		if err := s.repo.CreateKnowledgeBase(ctx, targetKB); err != nil {
			return nil, nil, err
		}
	}
	return sourceKB, targetKB, nil
}

// HybridSearch 벡터 검색 및 키워드 검색을 포함한 하이브리드 검색 수행
func (s *knowledgeBaseService) HybridSearch(ctx context.Context,
	id string,
	params types.SearchParams,
) ([]*types.SearchResult, error) {
	logger.Infof(ctx, "Hybrid search parameters, knowledge base ID: %s, query text: %s", id, params.QueryText)

	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)

	// 테넌트의 구성된 리트리버로 복합 검색 엔진 생성
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
	if err != nil {
		logger.Errorf(ctx, "Failed to create retrieval engine: %v", err)
		return nil, err
	}

	var retrieveParams []types.RetrieveParams
	var embeddingModel embedding.Embedder
	var kb *types.KnowledgeBase

	kb, err = s.repo.GetKnowledgeBaseByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id": id,
		})
		return nil, err
	}

	matchCount := params.MatchCount * 3

	// 벡터 검색이 지원되고 비활성화되지 않은 경우 매개변수 추가
	if retrieveEngine.SupportRetriever(types.VectorRetrieverType) && !params.DisableVectorMatch {
		logger.Info(ctx, "Vector retrieval supported, preparing vector retrieval parameters")

		logger.Infof(ctx, "Getting embedding model, model ID: %s", kb.EmbeddingModelID)
		embeddingModel, err = s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
		if err != nil {
			logger.Errorf(ctx, "Failed to get embedding model, model ID: %s, error: %v", kb.EmbeddingModelID, err)
			return nil, err
		}
		logger.Infof(ctx, "Embedding model retrieved: %v", embeddingModel)

		// 쿼리 텍스트에 대한 임베딩 벡터 생성
		logger.Info(ctx, "Starting to generate query embedding")
		queryEmbedding, err := embeddingModel.Embed(ctx, params.QueryText)
		if err != nil {
			logger.Errorf(ctx, "Failed to embed query text, query text: %s, error: %v", params.QueryText, err)
			return nil, err
		}
		logger.Infof(ctx, "Query embedding generated successfully, embedding vector length: %d", len(queryEmbedding))

		vectorParams := types.RetrieveParams{
			Query:            params.QueryText,
			Embedding:        queryEmbedding,
			KnowledgeBaseIDs: []string{id},
			TopK:             matchCount,
			Threshold:        params.VectorThreshold,
			RetrieverType:    types.VectorRetrieverType,
			KnowledgeIDs:     params.KnowledgeIDs,
			TagIDs:           params.TagIDs,
		}

		// FAQ 지식베이스의 경우 FAQ 인덱스 사용
		if kb.Type == types.KnowledgeBaseTypeFAQ {
			vectorParams.KnowledgeType = types.KnowledgeTypeFAQ
		}

		retrieveParams = append(retrieveParams, vectorParams)
		logger.Info(ctx, "Vector retrieval parameters setup completed")
	}

	// 키워드 검색이 지원되고 비활성화되지 않았으며 FAQ가 아닌 경우 매개변수 추가
	if retrieveEngine.SupportRetriever(types.KeywordsRetrieverType) && !params.DisableKeywordsMatch &&
		kb.Type != types.KnowledgeBaseTypeFAQ {
		logger.Info(ctx, "Keyword retrieval supported, preparing keyword retrieval parameters")
		retrieveParams = append(retrieveParams, types.RetrieveParams{
			Query:            params.QueryText,
			KnowledgeBaseIDs: []string{id},
			TopK:             matchCount,
			Threshold:        params.KeywordThreshold,
			RetrieverType:    types.KeywordsRetrieverType,
			KnowledgeIDs:     params.KnowledgeIDs,
			TagIDs:           params.TagIDs,
		})
		logger.Info(ctx, "Keyword retrieval parameters setup completed")
	}

	if len(retrieveParams) == 0 {
		logger.Error(ctx, "No retrieval parameters available")
		return nil, errors.New("no retrieve params")
	}

	// 구성된 엔진을 사용하여 검색 실행
	logger.Infof(ctx, "Starting retrieval, parameter count: %d", len(retrieveParams))
	retrieveResults, err := retrieveEngine.Retrieve(ctx, retrieveParams)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id": id,
			"query_text":        params.QueryText,
		})
		return nil, err
	}

	// 다른 리트리버의 모든 결과를 수집하고 청크 ID로 중복 제거
	logger.Infof(ctx, "Processing retrieval results")

	// RRF 퓨전을 위해 리트리버 유형별로 결과 분리
	var vectorResults []*types.IndexWithScore
	var keywordResults []*types.IndexWithScore
	for _, retrieveResult := range retrieveResults {
		logger.Infof(ctx, "Retrieval results, engine: %v, retriever: %v, count: %v",
			retrieveResult.RetrieverEngineType,
			retrieveResult.RetrieverType,
			len(retrieveResult.Results),
		)
		if retrieveResult.RetrieverType == types.VectorRetrieverType {
			vectorResults = append(vectorResults, retrieveResult.Results...)
		} else {
			keywordResults = append(keywordResults, retrieveResult.Results...)
		}
	}

	// 결과가 없으면 조기 반환
	if len(vectorResults) == 0 && len(keywordResults) == 0 {
		logger.Info(ctx, "No search results found")
		return nil, nil
	}
	logger.Infof(ctx, "Result count before fusion: vector=%d, keyword=%d", len(vectorResults), len(keywordResults))

	var deduplicatedChunks []*types.IndexWithScore

	// 벡터 결과만 있는 경우(키워드 결과 없음), 원본 임베딩 점수 유지
	// 이는 벡터 검색만 사용하는 FAQ 검색에 중요합니다
	if len(keywordResults) == 0 {
		logger.Info(ctx, "Only vector results, keeping original embedding scores")
		chunkInfoMap := make(map[string]*types.IndexWithScore)
		for _, r := range vectorResults {
			// 각 청크에 대해 가장 높은 점수 유지 (FAQ에는 여러 유사한 질문이 있을 수 있음)
			if existing, exists := chunkInfoMap[r.ChunkID]; !exists || r.Score > existing.Score {
				chunkInfoMap[r.ChunkID] = r
			}
		}
		deduplicatedChunks = make([]*types.IndexWithScore, 0, len(chunkInfoMap))
		for _, info := range chunkInfoMap {
			deduplicatedChunks = append(deduplicatedChunks, info)
		}
		slices.SortFunc(deduplicatedChunks, func(a, b *types.IndexWithScore) int {
			if a.Score > b.Score {
				return -1
			} else if a.Score < b.Score {
				return 1
			}
			return 0
		})
		logger.Infof(ctx, "Result count after deduplication: %d", len(deduplicatedChunks))
	} else {
		// RRF(Reciprocal Rank Fusion)를 사용하여 여러 리트리버의 결과 병합
		// RRF 점수 = 청크가 나타나는 각 리트리버에 대해 sum(1 / (k + rank))
		// k=60은 실제로 잘 작동하는 일반적인 선택입니다
		const rrfK = 60

		// 각 리트리버에 대한 순위 맵 구축 (이미 리트리버에서 점수순으로 정렬됨)
		vectorRanks := make(map[string]int)
		for i, r := range vectorResults {
			if _, exists := vectorRanks[r.ChunkID]; !exists {
				vectorRanks[r.ChunkID] = i + 1 // 1부터 시작하는 순위
			}
		}
		keywordRanks := make(map[string]int)
		for i, r := range keywordResults {
			if _, exists := keywordRanks[r.ChunkID]; !exists {
				keywordRanks[r.ChunkID] = i + 1 // 1부터 시작하는 순위
			}
		}

		// 모든 고유 청크 수집 및 RRF 점수 계산
		// 각 리트리버에서 각 청크에 대해 가장 높은 점수 유지
		chunkInfoMap := make(map[string]*types.IndexWithScore)
		rrfScores := make(map[string]float64)

		// 벡터 결과 처리 - 청크당 최고 점수 유지
		for _, r := range vectorResults {
			if existing, exists := chunkInfoMap[r.ChunkID]; !exists || r.Score > existing.Score {
				chunkInfoMap[r.ChunkID] = r
			}
		}
		// 키워드 결과 처리 - 벡터에 아직 없는 경우에만 추가
		for _, r := range keywordResults {
			if _, exists := chunkInfoMap[r.ChunkID]; !exists {
				chunkInfoMap[r.ChunkID] = r
			}
		}

		// RRF 점수 계산
		for chunkID := range chunkInfoMap {
			rrfScore := 0.0
			if rank, ok := vectorRanks[chunkID]; ok {
				rrfScore += 1.0 / float64(rrfK+rank)
			}
			if rank, ok := keywordRanks[chunkID]; ok {
				rrfScore += 1.0 / float64(rrfK+rank)
			}
			rrfScores[chunkID] = rrfScore
		}

		// 슬라이스로 변환하고 RRF 점수순으로 정렬
		deduplicatedChunks = make([]*types.IndexWithScore, 0, len(chunkInfoMap))
		for chunkID, info := range chunkInfoMap {
			// 다운스트림 처리를 위해 Score 필드에 RRF 점수 저장
			info.Score = rrfScores[chunkID]
			deduplicatedChunks = append(deduplicatedChunks, info)
		}
		slices.SortFunc(deduplicatedChunks, func(a, b *types.IndexWithScore) int {
			if a.Score > b.Score {
				return -1
			} else if a.Score < b.Score {
				return 1
			}
			return 0
		})

		logger.Infof(ctx, "Result count after RRF fusion: %d", len(deduplicatedChunks))

		// 디버깅을 위해 RRF 퓨전 후 상위 결과 로깅
		for i, chunk := range deduplicatedChunks {
			if i < 15 {
				vRank, vOk := vectorRanks[chunk.ChunkID]
				kRank, kOk := keywordRanks[chunk.ChunkID]
				logger.Debugf(ctx, "RRF rank %d: chunk_id=%s, rrf_score=%.6f, vector_rank=%v(%v), keyword_rank=%v(%v)",
					i, chunk.ChunkID, chunk.Score, vRank, vOk, kRank, kOk)
			}
		}
	}

	kb.EnsureDefaults()

	// 개별 인덱싱이 있는 FAQ에 대해 반복 검색이 필요한지 확인
	// 첫 번째 중복 제거 후 고유 청크가 충분하지 않은 경우에만 반복 검색 사용
	needsIterativeRetrieval := len(deduplicatedChunks) < params.MatchCount &&
		kb.Type == types.KnowledgeBaseTypeFAQ && len(vectorResults) == matchCount
	if needsIterativeRetrieval {
		logger.Info(ctx, "Not enough unique chunks, using iterative retrieval for FAQ")
		// 반복 검색을 사용하여 더 많은 고유 청크 확보 (내부에서 부정 질문 필터링 수행)
		deduplicatedChunks = s.iterativeRetrieveWithDeduplication(
			ctx,
			retrieveEngine,
			retrieveParams,
			params.MatchCount,
			params.QueryText,
		)
	} else if kb.Type == types.KnowledgeBaseTypeFAQ {
		// 반복 검색을 사용하지 않는 경우 부정 질문으로 필터링
		deduplicatedChunks = s.filterByNegativeQuestions(ctx, deduplicatedChunks, params.QueryText)
		logger.Infof(ctx, "Result count after negative question filtering: %d", len(deduplicatedChunks))
	}

	// MatchCount로 제한
	if len(deduplicatedChunks) > params.MatchCount {
		deduplicatedChunks = deduplicatedChunks[:params.MatchCount]
	}

	return s.processSearchResults(ctx, deduplicatedChunks)
}

// iterativeRetrieveWithDeduplication 충분한 고유 청크가 발견될 때까지 반복 검색 수행
// 개별 인덱싱 모드가 있는 FAQ 지식베이스에 사용됨
// 각 반복 후 청크 데이터 캐싱과 함께 부정 질문 필터링 적용
func (s *knowledgeBaseService) iterativeRetrieveWithDeduplication(ctx context.Context,
	retrieveEngine *retriever.CompositeRetrieveEngine,
	retrieveParams []types.RetrieveParams,
	matchCount int,
	queryText string,
) []*types.IndexWithScore {
	maxIterations := 5
	// 첫 번째 검색이 충분하지 않았을 때 호출되므로 더 큰 TopK로 시작
	// 첫 번째 검색은 이미 matchCount*3을 사용했으므로 거기서부터 시작
	currentTopK := matchCount * 3
	uniqueChunks := make(map[string]*types.IndexWithScore)
	// 반복 간에 반복적인 DB 쿼리를 피하기 위해 청크 데이터 캐시
	chunkDataCache := make(map[string]*types.Chunk)
	// 부정 질문에 의해 필터링된 청크 추적
	filteredOutChunks := make(map[string]struct{})

	queryTextLower := strings.ToLower(strings.TrimSpace(queryText))
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	for i := 0; i < maxIterations; i++ {
		// 검색 매개변수에서 TopK 업데이트
		updatedParams := make([]types.RetrieveParams, len(retrieveParams))
		for j := range retrieveParams {
			updatedParams[j] = retrieveParams[j]
			updatedParams[j].TopK = currentTopK
		}

		// 검색 실행
		retrieveResults, err := retrieveEngine.Retrieve(ctx, updatedParams)
		if err != nil {
			logger.Warnf(ctx, "Iterative retrieval failed at iteration %d: %v", i+1, err)
			break
		}

		// 결과 수집
		iterationResults := []*types.IndexWithScore{}
		for _, retrieveResult := range retrieveResults {
			iterationResults = append(iterationResults, retrieveResult.Results...)
		}

		if len(iterationResults) == 0 {
			logger.Infof(ctx, "No results found at iteration %d", i+1)
			break
		}

		totalRetrieved := len(iterationResults)

		// DB에서 가져와야 할 새 청크 ID 수집
		newChunkIDs := make([]string, 0)
		for _, result := range iterationResults {
			if _, cached := chunkDataCache[result.ChunkID]; !cached {
				if _, filtered := filteredOutChunks[result.ChunkID]; !filtered {
					newChunkIDs = append(newChunkIDs, result.ChunkID)
				}
			}
		}

		// 새 청크만 일괄 가져오기
		if len(newChunkIDs) > 0 {
			newChunks, err := s.chunkRepo.ListChunksByID(ctx, tenantID, newChunkIDs)
			if err != nil {
				logger.Warnf(ctx, "Failed to fetch chunks at iteration %d: %v", i+1, err)
			} else {
				for _, chunk := range newChunks {
					chunkDataCache[chunk.ID] = chunk
				}
			}
		}

		// 한 번의 패스로 중복 제거, 병합 및 필터링
		for _, result := range iterationResults {
			// 이미 필터링된 경우 건너뜀
			if _, filtered := filteredOutChunks[result.ChunkID]; filtered {
				continue
			}

			// 캐시된 데이터를 사용하여 부정 질문 확인
			if chunkData, ok := chunkDataCache[result.ChunkID]; ok {
				if chunkData.ChunkType == types.ChunkTypeFAQ {
					if meta, err := chunkData.FAQMetadata(); err == nil && meta != nil {
						if s.matchesNegativeQuestions(queryTextLower, meta.NegativeQuestions) {
							filteredOutChunks[result.ChunkID] = struct{}{}
							delete(uniqueChunks, result.ChunkID)
							continue
						}
					}
				}
			}

			// 각 청크에 대해 가장 높은 점수 유지
			if existing, ok := uniqueChunks[result.ChunkID]; !ok || result.Score > existing.Score {
				uniqueChunks[result.ChunkID] = result
			}
		}

		logger.Infof(
			ctx,
			"After iteration %d: retrieved %d results, found %d valid unique chunks (target: %d)",
			i+1,
			totalRetrieved,
			len(uniqueChunks),
			matchCount,
		)

		// 조기 종료: 중복 제거 및 필터링 후 충분한 고유 청크가 있는지 확인
		if len(uniqueChunks) >= matchCount {
			logger.Infof(ctx, "Found enough unique chunks after %d iterations", i+1)
			break
		}

		// 조기 종료: TopK보다 적은 결과를 얻었다면 더 이상 검색할 결과가 없음
		if totalRetrieved < currentTopK {
			logger.Infof(ctx, "No more results available (got %d < %d), stopping iteration", totalRetrieved, currentTopK)
			break
		}

		// 다음 반복을 위해 TopK 증가
		currentTopK *= 2
	}

	// 맵을 슬라이스로 변환하고 점수순으로 정렬
	result := make([]*types.IndexWithScore, 0, len(uniqueChunks))
	for _, chunk := range uniqueChunks {
		result = append(result, chunk)
	}

	// 점수 내림차순 정렬
	slices.SortFunc(result, func(a, b *types.IndexWithScore) int {
		if a.Score > b.Score {
			return -1
		} else if a.Score < b.Score {
			return 1
		}
		return 0
	})

	logger.Infof(ctx, "Iterative retrieval completed: %d unique chunks found after filtering", len(result))
	return result
}

// filterByNegativeQuestions FAQ 지식베이스에 대해 부정 질문과 일치하는 청크 필터링
func (s *knowledgeBaseService) filterByNegativeQuestions(ctx context.Context,
	chunks []*types.IndexWithScore,
	queryText string,
) []*types.IndexWithScore {
	if len(chunks) == 0 {
		return chunks
	}

	queryTextLower := strings.ToLower(strings.TrimSpace(queryText))
	if queryTextLower == "" {
		return chunks
	}

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// 청크 ID 수집
	chunkIDs := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		chunkIDs = append(chunkIDs, chunk.ChunkID)
	}

	// 부정 질문을 가져오기 위해 청크 일괄 가져오기
	allChunks, err := s.chunkRepo.ListChunksByID(ctx, tenantID, chunkIDs)
	if err != nil {
		logger.Warnf(ctx, "Failed to fetch chunks for negative question filtering: %v", err)
		// 청크를 가져올 수 없는 경우 원본 결과 반환
		return chunks
	}

	// 빠른 조회를 위한 청크 맵 구축
	chunkMap := make(map[string]*types.Chunk, len(allChunks))
	for _, chunk := range allChunks {
		chunkMap[chunk.ID] = chunk
	}

	// 부정 질문과 일치하는 청크 필터링
	filteredChunks := make([]*types.IndexWithScore, 0, len(chunks))
	for _, chunk := range chunks {
		chunkData, ok := chunkMap[chunk.ChunkID]
		if !ok {
			// 청크를 찾을 수 없는 경우 유지 (발생하면 안 되지만 안전하게 처리)
			filteredChunks = append(filteredChunks, chunk)
			continue
		}

		// FAQ 유형 청크만 필터링
		if chunkData.ChunkType != types.ChunkTypeFAQ {
			filteredChunks = append(filteredChunks, chunk)
			continue
		}

		// FAQ 메타데이터 가져오기 및 부정 질문 확인
		meta, err := chunkData.FAQMetadata()
		if err != nil || meta == nil {
			// 메타데이터를 파싱할 수 없는 경우 청크 유지
			filteredChunks = append(filteredChunks, chunk)
			continue
		}

		// 쿼리가 부정 질문과 일치하는지 확인
		if s.matchesNegativeQuestions(queryTextLower, meta.NegativeQuestions) {
			logger.Debugf(ctx, "Filtered FAQ chunk %s due to negative question match", chunk.ChunkID)
			continue
		}

		// 청크 유지
		filteredChunks = append(filteredChunks, chunk)
	}

	return filteredChunks
}

// matchesNegativeQuestions 쿼리 텍스트가 부정 질문과 일치하는지 확인
// 쿼리가 부정 질문과 일치하면 true 반환, 그렇지 않으면 false 반환
func (s *knowledgeBaseService) matchesNegativeQuestions(queryTextLower string, negativeQuestions []string) bool {
	if len(negativeQuestions) == 0 {
		return false
	}

	for _, negativeQ := range negativeQuestions {
		negativeQLower := strings.ToLower(strings.TrimSpace(negativeQ))
		if negativeQLower == "" {
			continue
		}
		// 쿼리 텍스트가 부정 질문과 정확히 일치하는지 확인
		if queryTextLower == negativeQLower {
			return true
		}
	}
	return false
}

// processSearchResults 검색 결과 처리, 데이터베이스 쿼리 최적화
func (s *knowledgeBaseService) processSearchResults(ctx context.Context,
	chunks []*types.IndexWithScore,
) ([]*types.SearchResult, error) {
	if len(chunks) == 0 {
		return nil, nil
	}

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// 효율적인 처리를 위한 데이터 구조 준비
	var knowledgeIDs []string
	var chunkIDs []string
	chunkScores := make(map[string]float64)
	chunkMatchTypes := make(map[string]types.MatchType)
	processedKnowledgeIDs := make(map[string]bool)

	// 모든 지식 및 청크 ID 수집
	for _, chunk := range chunks {
		if !processedKnowledgeIDs[chunk.KnowledgeID] {
			knowledgeIDs = append(knowledgeIDs, chunk.KnowledgeID)
			processedKnowledgeIDs[chunk.KnowledgeID] = true
		}

		chunkIDs = append(chunkIDs, chunk.ChunkID)
		chunkScores[chunk.ChunkID] = chunk.Score
		chunkMatchTypes[chunk.ChunkID] = chunk.MatchType
	}

	// 지식 데이터 일괄 가져오기
	logger.Infof(ctx, "Fetching knowledge data for %d IDs", len(knowledgeIDs))
	knowledgeMap, err := s.fetchKnowledgeData(ctx, tenantID, knowledgeIDs)
	if err != nil {
		return nil, err
	}

	// 모든 청크 일괄 가져오기
	logger.Infof(ctx, "Fetching chunk data for %d IDs", len(chunkIDs))
	allChunks, err := s.chunkRepo.ListChunksByID(ctx, tenantID, chunkIDs)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": tenantID,
			"chunk_ids": chunkIDs,
		})
		return nil, err
	}
	logger.Infof(ctx, "Chunk data fetched successfully, count: %d", len(allChunks))

	// 청크 맵 구축 및 가져올 추가 ID 수집
	chunkMap := make(map[string]*types.Chunk, len(allChunks))
	var additionalChunkIDs []string
	processedChunkIDs := make(map[string]bool)

	// 첫 번째 패스: 청크 맵 구축 및 부모 ID 수집
	for _, chunk := range allChunks {
		chunkMap[chunk.ID] = chunk
		processedChunkIDs[chunk.ID] = true

		// 부모 청크 수집
		if chunk.ParentChunkID != "" && !processedChunkIDs[chunk.ParentChunkID] {
			additionalChunkIDs = append(additionalChunkIDs, chunk.ParentChunkID)
			processedChunkIDs[chunk.ParentChunkID] = true

			// 부모에게 점수 전달
			chunkScores[chunk.ParentChunkID] = chunkScores[chunk.ID]
			chunkMatchTypes[chunk.ParentChunkID] = types.MatchTypeParentChunk
		}

		// 관련 청크 수집
		relationChunkIDs := s.collectRelatedChunkIDs(chunk, processedChunkIDs)
		for _, chunkID := range relationChunkIDs {
			additionalChunkIDs = append(additionalChunkIDs, chunkID)
			chunkMatchTypes[chunkID] = types.MatchTypeRelationChunk
		}

		// 인접 청크 추가 (이전 및 다음)
		if slices.Contains([]string{types.ChunkTypeText}, chunk.ChunkType) {
			if chunk.NextChunkID != "" && !processedChunkIDs[chunk.NextChunkID] {
				additionalChunkIDs = append(additionalChunkIDs, chunk.NextChunkID)
				processedChunkIDs[chunk.NextChunkID] = true
				chunkMatchTypes[chunk.NextChunkID] = types.MatchTypeNearByChunk
			}
			if chunk.PreChunkID != "" && !processedChunkIDs[chunk.PreChunkID] {
				additionalChunkIDs = append(additionalChunkIDs, chunk.PreChunkID)
				processedChunkIDs[chunk.PreChunkID] = true
				chunkMatchTypes[chunk.PreChunkID] = types.MatchTypeNearByChunk
			}
		}
	}

	// 필요한 경우 모든 추가 청크 일괄 가져오기
	if len(additionalChunkIDs) > 0 {
		logger.Infof(ctx, "Fetching %d additional chunks", len(additionalChunkIDs))
		additionalChunks, err := s.chunkRepo.ListChunksByID(ctx, tenantID, additionalChunkIDs)
		if err != nil {
			logger.Warnf(ctx, "Failed to fetch some additional chunks: %v", err)
			// 있는 것으로 계속 진행
		} else {
			// 청크 맵에 추가
			for _, chunk := range additionalChunks {
				chunkMap[chunk.ID] = chunk
			}
		}
	}

	// 최종 검색 결과 구축 - 입력 청크의 원래 순서 유지
	var searchResults []*types.SearchResult
	addedChunkIDs := make(map[string]bool)

	// 첫 번째 패스: 입력 청크의 원래 순서대로 결과 추가
	for _, inputChunk := range chunks {
		chunk, exists := chunkMap[inputChunk.ChunkID]
		if !exists {
			logger.Debugf(ctx, "Chunk not found in chunkMap: %s", inputChunk.ChunkID)
			continue
		}
		if !s.isValidTextChunk(chunk) {
			logger.Debugf(ctx, "Chunk is not valid text chunk: %s, type: %s", chunk.ID, chunk.ChunkType)
			continue
		}
		if addedChunkIDs[chunk.ID] {
			continue
		}

		score := chunkScores[chunk.ID]
		if knowledge, ok := knowledgeMap[chunk.KnowledgeID]; ok {
			matchType := chunkMatchTypes[chunk.ID]
			searchResults = append(searchResults, s.buildSearchResult(chunk, knowledge, score, matchType))
			addedChunkIDs[chunk.ID] = true
		} else {
			logger.Warnf(ctx, "Knowledge not found for chunk: %s, knowledge_id: %s", chunk.ID, chunk.KnowledgeID)
		}
	}

	// 두 번째 패스: 원래 입력에 없었던 추가 청크(부모, 인접, 관계) 추가
	for chunkID, chunk := range chunkMap {
		if addedChunkIDs[chunkID] || !s.isValidTextChunk(chunk) {
			continue
		}

		score, hasScore := chunkScores[chunkID]
		if !hasScore || score <= 0 {
			score = 0.0
		}

		if knowledge, ok := knowledgeMap[chunk.KnowledgeID]; ok {
			matchType := types.MatchTypeParentChunk
			if specificType, exists := chunkMatchTypes[chunkID]; exists {
				matchType = specificType
			} else {
				logger.Warnf(ctx, "Unkonwn match type for chunk: %s", chunkID)
				continue
			}
			searchResults = append(searchResults, s.buildSearchResult(chunk, knowledge, score, matchType))
		}
	}
	logger.Infof(ctx, "Search results processed, total: %d", len(searchResults))
	return searchResults, nil
}

// collectRelatedChunkIDs 청크에서 관련 청크 ID 추출
func (s *knowledgeBaseService) collectRelatedChunkIDs(chunk *types.Chunk, processedIDs map[string]bool) []string {
	var relatedIDs []string
	// 직접적인 관계 처리
	if len(chunk.RelationChunks) > 0 {
		var relations []string
		if err := json.Unmarshal(chunk.RelationChunks, &relations); err == nil {
			for _, id := range relations {
				if !processedIDs[id] {
					relatedIDs = append(relatedIDs, id)
					processedIDs[id] = true
				}
			}
		}
	}
	return relatedIDs
}

// buildSearchResult 청크와 지식에서 검색 결과 생성
func (s *knowledgeBaseService) buildSearchResult(chunk *types.Chunk,
	knowledge *types.Knowledge,
	score float64,
	matchType types.MatchType,
) *types.SearchResult {
	return &types.SearchResult{
		ID:                chunk.ID,
		Content:           chunk.Content,
		KnowledgeID:       chunk.KnowledgeID,
		ChunkIndex:        chunk.ChunkIndex,
		KnowledgeTitle:    knowledge.Title,
		StartAt:           chunk.StartAt,
		EndAt:             chunk.EndAt,
		Seq:               chunk.ChunkIndex,
		Score:             score,
		MatchType:         matchType,
		Metadata:          knowledge.GetMetadata(),
		ChunkType:         string(chunk.ChunkType),
		ParentChunkID:     chunk.ParentChunkID,
		ImageInfo:         chunk.ImageInfo,
		KnowledgeFilename: knowledge.FileName,
		KnowledgeSource:   knowledge.Source,
		ChunkMetadata:     chunk.Metadata,
	}
}

// isValidTextChunk 청크가 유효한 텍스트 청크인지 확인
func (s *knowledgeBaseService) isValidTextChunk(chunk *types.Chunk) bool {
	return slices.Contains([]types.ChunkType{
		types.ChunkTypeText, types.ChunkTypeSummary,
		types.ChunkTypeTableColumn, types.ChunkTypeTableSummary,
		types.ChunkTypeFAQ,
	}, chunk.ChunkType)
}

// fetchKnowledgeData 지식 데이터를 일괄 가져오기
func (s *knowledgeBaseService) fetchKnowledgeData(ctx context.Context,
	tenantID uint64,
	knowledgeIDs []string,
) (map[string]*types.Knowledge, error) {
	knowledges, err := s.kgRepo.GetKnowledgeBatch(ctx, tenantID, knowledgeIDs)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id":     tenantID,
			"knowledge_ids": knowledgeIDs,
		})
		return nil, err
	}

	knowledgeMap := make(map[string]*types.Knowledge, len(knowledges))
	for _, knowledge := range knowledges {
		knowledgeMap[knowledge.ID] = knowledge
	}

	return knowledgeMap, nil
}
