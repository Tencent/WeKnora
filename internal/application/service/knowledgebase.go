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

// ErrInvalidTenantID represents an error for invalid tenant ID
var ErrInvalidTenantID = errors.New("invalid tenant ID")

// knowledgeBaseService implements the knowledge base service interface
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

// NewKnowledgeBaseService creates a new knowledge base service
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

// GetRepository gets the knowledge base repository
// Parameters:
//   - ctx: Context with authentication and request information
//
// Returns:
//   - interfaces.KnowledgeBaseRepository: Knowledge base repository
func (s *knowledgeBaseService) GetRepository() interfaces.KnowledgeBaseRepository {
	return s.repo
}

// CreateKnowledgeBase creates a new knowledge base
func (s *knowledgeBaseService) CreateKnowledgeBase(ctx context.Context,
	kb *types.KnowledgeBase,
) (*types.KnowledgeBase, error) {
	// Generate UUID and set creation timestamps
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

// GetKnowledgeBaseByID retrieves a knowledge base by its ID
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

// ListKnowledgeBases returns all knowledge bases for a tenant
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

	// Query knowledge count and chunk count for each knowledge base
	for _, kb := range kbs {
		kb.EnsureDefaults()

		// Get knowledge count
		switch kb.Type {
		case types.KnowledgeBaseTypeDocument:
			knowledgeCount, err := s.kgRepo.CountKnowledgeByKnowledgeBaseID(ctx, tenantID, kb.ID)
			if err != nil {
				logger.Warnf(ctx, "Failed to get knowledge count for knowledge base %s: %v", kb.ID, err)
			} else {
				kb.KnowledgeCount = knowledgeCount
			}
		case types.KnowledgeBaseTypeFAQ:
			// Get chunk count
			chunkCount, err := s.chunkRepo.CountChunksByKnowledgeBaseID(ctx, tenantID, kb.ID)
			if err != nil {
				logger.Warnf(ctx, "Failed to get chunk count for knowledge base %s: %v", kb.ID, err)
			} else {
				kb.ChunkCount = chunkCount
			}
		}

		// Check if there is a processing import task
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

// UpdateKnowledgeBase updates a knowledge base's properties
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

	// Get existing knowledge base
	kb, err := s.repo.GetKnowledgeBaseByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id": id,
		})
		return nil, err
	}

	// Update the knowledge base properties
	kb.Name = name
	kb.Description = description
	kb.ChunkingConfig = config.ChunkingConfig
	kb.ImageProcessingConfig = config.ImageProcessingConfig
	// Update FAQ config if provided
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

// DeleteKnowledgeBase deletes a knowledge base by its ID
// This method marks the knowledge base as deleted and enqueues an async task
// to handle the heavy cleanup operations (embeddings, chunks, files, graph data)
func (s *knowledgeBaseService) DeleteKnowledgeBase(ctx context.Context, id string) error {
	if id == "" {
		logger.Error(ctx, "Knowledge base ID is empty")
		return errors.New("knowledge base ID cannot be empty")
	}

	logger.Infof(ctx, "Deleting knowledge base, ID: %s", id)

	// Get tenant ID from context
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)

	// Step 1: Delete the knowledge base record first (mark as deleted)
	logger.Infof(ctx, "Deleting knowledge base from database")
	err := s.repo.DeleteKnowledgeBase(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id": id,
		})
		return err
	}

	// Step 2: Enqueue async task for heavy cleanup operations
	payload := types.KBDeletePayload{
		TenantID:         tenantID,
		KnowledgeBaseID:  id,
		EffectiveEngines: tenantInfo.GetEffectiveEngines(),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		logger.Warnf(ctx, "Failed to marshal KB delete payload: %v", err)
		// Don't fail the request, the KB record is already deleted
		return nil
	}

	task := asynq.NewTask(types.TypeKBDelete, payloadBytes, asynq.Queue("low"), asynq.MaxRetry(3))
	info, err := s.asynqClient.Enqueue(task)
	if err != nil {
		logger.Warnf(ctx, "Failed to enqueue KB delete task: %v", err)
		// Don't fail the request, the KB record is already deleted
		return nil
	}

	logger.Infof(ctx, "KB delete task enqueued: %s, knowledge base ID: %s", info.ID, id)
	logger.Infof(ctx, "Knowledge base deleted successfully, ID: %s", id)
	return nil
}

// ProcessKBDelete handles async knowledge base deletion task
// This method performs heavy cleanup operations: deleting embeddings, chunks, files, and graph data
func (s *knowledgeBaseService) ProcessKBDelete(ctx context.Context, t *asynq.Task) error {
	var payload types.KBDeletePayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "Failed to unmarshal KB delete payload: %v", err)
		return err
	}

	tenantID := payload.TenantID
	kbID := payload.KnowledgeBaseID

	// Set tenant context for downstream services
	ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)

	logger.Infof(ctx, "Processing KB delete task for knowledge base: %s", kbID)

	// Step 1: Get all knowledge entries in this knowledge base
	logger.Infof(ctx, "Fetching all knowledge entries in knowledge base, ID: %s", kbID)
	knowledgeList, err := s.kgRepo.ListKnowledgeByKnowledgeBaseID(ctx, tenantID, kbID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id": kbID,
		})
		return err
	}
	logger.Infof(ctx, "Found %d knowledge entries to delete", len(knowledgeList))

	// Step 2: Delete all knowledge entries and their resources
	if len(knowledgeList) > 0 {
		knowledgeIDs := make([]string, 0, len(knowledgeList))
		for _, knowledge := range knowledgeList {
			knowledgeIDs = append(knowledgeIDs, knowledge.ID)
		}

		logger.Infof(ctx, "Deleting all knowledge entries and their resources")

		// Delete embeddings from vector store
		logger.Infof(ctx, "Deleting embeddings from vector store")
		retrieveEngine, err := retriever.NewCompositeRetrieveEngine(
			s.retrieveEngine,
			payload.EffectiveEngines,
		)
		if err != nil {
			logger.Warnf(ctx, "Failed to create retrieve engine: %v", err)
		} else {
			// Group knowledge by embedding model and type
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

		// Delete all chunks
		logger.Infof(ctx, "Deleting all chunks in knowledge base")
		for _, knowledgeID := range knowledgeIDs {
			if err := s.chunkRepo.DeleteChunksByKnowledgeID(ctx, tenantID, knowledgeID); err != nil {
				logger.Warnf(ctx, "Failed to delete chunks for knowledge %s: %v", knowledgeID, err)
			}
		}

		// Delete physical files and adjust storage
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

		// Delete knowledge graph data
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

		// Delete all knowledge entries from database
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

// SetEmbeddingModel sets the embedding model for a knowledge base
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

	// Get the knowledge base
	kb, err := s.repo.GetKnowledgeBaseByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id": id,
		})
		return err
	}

	// Update the knowledge base's embedding model
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

// CopyKnowledgeBase copies a knowledge base to a new knowledge base
// 浅拷贝
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

// HybridSearch performs hybrid search, including vector retrieval and keyword retrieval
func (s *knowledgeBaseService) HybridSearch(ctx context.Context,
	id string,
	params types.SearchParams,
) ([]*types.SearchResult, error) {
	logger.Infof(ctx, "Hybrid search parameters, knowledge base ID: %s, query text: %s", id, params.QueryText)

	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)

	// Create a composite retrieval engine with tenant's configured retrievers
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

	// Add vector retrieval params if supported
	if retrieveEngine.SupportRetriever(types.VectorRetrieverType) && !params.DisableVectorMatch {
		logger.Info(ctx, "Vector retrieval supported, preparing vector retrieval parameters")

		logger.Infof(ctx, "Getting embedding model, model ID: %s", kb.EmbeddingModelID)
		embeddingModel, err = s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
		if err != nil {
			logger.Errorf(ctx, "Failed to get embedding model, model ID: %s, error: %v", kb.EmbeddingModelID, err)
			return nil, err
		}
		logger.Infof(ctx, "Embedding model retrieved: %v", embeddingModel)

		// Generate embedding vector for the query text
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

		// For FAQ knowledge base, use FAQ index
		if kb.Type == types.KnowledgeBaseTypeFAQ {
			vectorParams.KnowledgeType = types.KnowledgeTypeFAQ
		}

		retrieveParams = append(retrieveParams, vectorParams)
		logger.Info(ctx, "Vector retrieval parameters setup completed")
	}

	// Add keyword retrieval params if supported and not FAQ
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

	// Execute retrieval using the configured engines
	logger.Infof(ctx, "Starting retrieval, parameter count: %d", len(retrieveParams))
	retrieveResults, err := retrieveEngine.Retrieve(ctx, retrieveParams)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id": id,
			"query_text":        params.QueryText,
		})
		return nil, err
	}

	// Collect all results from different retrievers and deduplicate by chunk ID
	logger.Infof(ctx, "Processing retrieval results")

	// Separate results by retriever type for RRF fusion
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

	// Early return if no results
	if len(vectorResults) == 0 && len(keywordResults) == 0 {
		logger.Info(ctx, "No search results found")
		return nil, nil
	}
	logger.Infof(ctx, "Result count before fusion: vector=%d, keyword=%d", len(vectorResults), len(keywordResults))

	var deduplicatedChunks []*types.IndexWithScore

	// If only vector results (no keyword results), keep original embedding scores
	// This is important for FAQ search which only uses vector retrieval
	if len(keywordResults) == 0 {
		logger.Info(ctx, "Only vector results, keeping original embedding scores")
		chunkInfoMap := make(map[string]*types.IndexWithScore)
		for _, r := range vectorResults {
			// Keep the highest score for each chunk (FAQ may have multiple similar questions)
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
		// Use RRF (Reciprocal Rank Fusion) to merge results from multiple retrievers
		// RRF score = sum(1 / (k + rank)) for each retriever where the chunk appears
		// k=60 is a common choice that works well in practice
		const rrfK = 60

		// Build rank maps for each retriever (already sorted by score from retriever)
		vectorRanks := make(map[string]int)
		for i, r := range vectorResults {
			if _, exists := vectorRanks[r.ChunkID]; !exists {
				vectorRanks[r.ChunkID] = i + 1 // 1-indexed rank
			}
		}
		keywordRanks := make(map[string]int)
		for i, r := range keywordResults {
			if _, exists := keywordRanks[r.ChunkID]; !exists {
				keywordRanks[r.ChunkID] = i + 1 // 1-indexed rank
			}
		}

		// Collect all unique chunks and compute RRF scores
		// Keep the highest score for each chunk from each retriever
		chunkInfoMap := make(map[string]*types.IndexWithScore)
		rrfScores := make(map[string]float64)

		// Process vector results - keep highest score per chunk
		for _, r := range vectorResults {
			if existing, exists := chunkInfoMap[r.ChunkID]; !exists || r.Score > existing.Score {
				chunkInfoMap[r.ChunkID] = r
			}
		}
		// Process keyword results - only add if not already from vector
		for _, r := range keywordResults {
			if _, exists := chunkInfoMap[r.ChunkID]; !exists {
				chunkInfoMap[r.ChunkID] = r
			}
		}

		// Compute RRF scores
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

		// Convert to slice and sort by RRF score
		deduplicatedChunks = make([]*types.IndexWithScore, 0, len(chunkInfoMap))
		for chunkID, info := range chunkInfoMap {
			// Store RRF score in the Score field for downstream processing
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

		// Log top results after RRF fusion for debugging
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

	// Check if we need iterative retrieval for FAQ with separate indexing
	// Only use iterative retrieval if we don't have enough unique chunks after first deduplication
	needsIterativeRetrieval := len(deduplicatedChunks) < params.MatchCount &&
		kb.Type == types.KnowledgeBaseTypeFAQ && len(vectorResults) == matchCount
	if needsIterativeRetrieval {
		logger.Info(ctx, "Not enough unique chunks, using iterative retrieval for FAQ")
		// Use iterative retrieval to get more unique chunks (with negative question filtering inside)
		deduplicatedChunks = s.iterativeRetrieveWithDeduplication(
			ctx,
			retrieveEngine,
			retrieveParams,
			params.MatchCount,
			params.QueryText,
		)
	} else if kb.Type == types.KnowledgeBaseTypeFAQ {
		// Filter by negative questions if not using iterative retrieval
		deduplicatedChunks = s.filterByNegativeQuestions(ctx, deduplicatedChunks, params.QueryText)
		logger.Infof(ctx, "Result count after negative question filtering: %d", len(deduplicatedChunks))
	}

	// Limit to MatchCount
	if len(deduplicatedChunks) > params.MatchCount {
		deduplicatedChunks = deduplicatedChunks[:params.MatchCount]
	}

	return s.processSearchResults(ctx, deduplicatedChunks)
}

// iterativeRetrieveWithDeduplication performs iterative retrieval until enough unique chunks are found
// This is used for FAQ knowledge bases with separate indexing mode
// Negative question filtering is applied after each iteration with chunk data caching
func (s *knowledgeBaseService) iterativeRetrieveWithDeduplication(ctx context.Context,
	retrieveEngine *retriever.CompositeRetrieveEngine,
	retrieveParams []types.RetrieveParams,
	matchCount int,
	queryText string,
) []*types.IndexWithScore {
	maxIterations := 5
	// Start with a larger TopK since we're called when first retrieval wasn't enough
	// The first retrieval already used matchCount*3, so start from there
	currentTopK := matchCount * 3
	uniqueChunks := make(map[string]*types.IndexWithScore)
	// Cache chunk data to avoid repeated DB queries across iterations
	chunkDataCache := make(map[string]*types.Chunk)
	// Track chunks that have been filtered out by negative questions
	filteredOutChunks := make(map[string]struct{})

	queryTextLower := strings.ToLower(strings.TrimSpace(queryText))
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	for i := 0; i < maxIterations; i++ {
		// Update TopK in retrieve params
		updatedParams := make([]types.RetrieveParams, len(retrieveParams))
		for j := range retrieveParams {
			updatedParams[j] = retrieveParams[j]
			updatedParams[j].TopK = currentTopK
		}

		// Execute retrieval
		retrieveResults, err := retrieveEngine.Retrieve(ctx, updatedParams)
		if err != nil {
			logger.Warnf(ctx, "Iterative retrieval failed at iteration %d: %v", i+1, err)
			break
		}

		// Collect results
		iterationResults := []*types.IndexWithScore{}
		for _, retrieveResult := range retrieveResults {
			iterationResults = append(iterationResults, retrieveResult.Results...)
		}

		if len(iterationResults) == 0 {
			logger.Infof(ctx, "No results found at iteration %d", i+1)
			break
		}

		totalRetrieved := len(iterationResults)

		// Collect new chunk IDs that need to be fetched from DB
		newChunkIDs := make([]string, 0)
		for _, result := range iterationResults {
			if _, cached := chunkDataCache[result.ChunkID]; !cached {
				if _, filtered := filteredOutChunks[result.ChunkID]; !filtered {
					newChunkIDs = append(newChunkIDs, result.ChunkID)
				}
			}
		}

		// Batch fetch only new chunks
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

		// Deduplicate, merge, and filter in one pass
		for _, result := range iterationResults {
			// Skip if already filtered out
			if _, filtered := filteredOutChunks[result.ChunkID]; filtered {
				continue
			}

			// Check negative questions using cached data
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

			// Keep highest score for each chunk
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

		// Early stop: Check if we have enough unique chunks after deduplication and filtering
		if len(uniqueChunks) >= matchCount {
			logger.Infof(ctx, "Found enough unique chunks after %d iterations", i+1)
			break
		}

		// Early stop: If we got fewer results than TopK, there are no more results to retrieve
		if totalRetrieved < currentTopK {
			logger.Infof(ctx, "No more results available (got %d < %d), stopping iteration", totalRetrieved, currentTopK)
			break
		}

		// Increase TopK for next iteration
		currentTopK *= 2
	}

	// Convert map to slice and sort by score
	result := make([]*types.IndexWithScore, 0, len(uniqueChunks))
	for _, chunk := range uniqueChunks {
		result = append(result, chunk)
	}

	// Sort by score descending
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

// filterByNegativeQuestions filters out chunks that match negative questions for FAQ knowledge bases.
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

	// Collect chunk IDs
	chunkIDs := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		chunkIDs = append(chunkIDs, chunk.ChunkID)
	}

	// Batch fetch chunks to get negative questions
	allChunks, err := s.chunkRepo.ListChunksByID(ctx, tenantID, chunkIDs)
	if err != nil {
		logger.Warnf(ctx, "Failed to fetch chunks for negative question filtering: %v", err)
		// If we can't fetch chunks, return original results
		return chunks
	}

	// Build chunk map for quick lookup
	chunkMap := make(map[string]*types.Chunk, len(allChunks))
	for _, chunk := range allChunks {
		chunkMap[chunk.ID] = chunk
	}

	// Filter out chunks that match negative questions
	filteredChunks := make([]*types.IndexWithScore, 0, len(chunks))
	for _, chunk := range chunks {
		chunkData, ok := chunkMap[chunk.ChunkID]
		if !ok {
			// If chunk not found, keep it (shouldn't happen, but be safe)
			filteredChunks = append(filteredChunks, chunk)
			continue
		}

		// Only filter FAQ type chunks
		if chunkData.ChunkType != types.ChunkTypeFAQ {
			filteredChunks = append(filteredChunks, chunk)
			continue
		}

		// Get FAQ metadata and check negative questions
		meta, err := chunkData.FAQMetadata()
		if err != nil || meta == nil {
			// If we can't parse metadata, keep the chunk
			filteredChunks = append(filteredChunks, chunk)
			continue
		}

		// Check if query matches any negative question
		if s.matchesNegativeQuestions(queryTextLower, meta.NegativeQuestions) {
			logger.Debugf(ctx, "Filtered FAQ chunk %s due to negative question match", chunk.ChunkID)
			continue
		}

		// Keep the chunk
		filteredChunks = append(filteredChunks, chunk)
	}

	return filteredChunks
}

// matchesNegativeQuestions checks if the query text matches any negative questions.
// Returns true if the query matches any negative question, false otherwise.
func (s *knowledgeBaseService) matchesNegativeQuestions(queryTextLower string, negativeQuestions []string) bool {
	if len(negativeQuestions) == 0 {
		return false
	}

	for _, negativeQ := range negativeQuestions {
		negativeQLower := strings.ToLower(strings.TrimSpace(negativeQ))
		if negativeQLower == "" {
			continue
		}
		// Check if query text is exactly the same as the negative question
		if queryTextLower == negativeQLower {
			return true
		}
	}
	return false
}

// processSearchResults handles the processing of search results, optimizing database queries
func (s *knowledgeBaseService) processSearchResults(ctx context.Context,
	chunks []*types.IndexWithScore,
) ([]*types.SearchResult, error) {
	if len(chunks) == 0 {
		return nil, nil
	}

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// Prepare data structures for efficient processing
	var knowledgeIDs []string
	var chunkIDs []string
	chunkScores := make(map[string]float64)
	chunkMatchTypes := make(map[string]types.MatchType)
	chunkMatchedContents := make(map[string]string)
	processedKnowledgeIDs := make(map[string]bool)

	// Collect all knowledge and chunk IDs
	for _, chunk := range chunks {
		if !processedKnowledgeIDs[chunk.KnowledgeID] {
			knowledgeIDs = append(knowledgeIDs, chunk.KnowledgeID)
			processedKnowledgeIDs[chunk.KnowledgeID] = true
		}

		chunkIDs = append(chunkIDs, chunk.ChunkID)
		chunkScores[chunk.ChunkID] = chunk.Score
		chunkMatchTypes[chunk.ChunkID] = chunk.MatchType
		chunkMatchedContents[chunk.ChunkID] = chunk.Content
	}

	// Batch fetch knowledge data
	logger.Infof(ctx, "Fetching knowledge data for %d IDs", len(knowledgeIDs))
	knowledgeMap, err := s.fetchKnowledgeData(ctx, tenantID, knowledgeIDs)
	if err != nil {
		return nil, err
	}

	// Batch fetch all chunks in one go
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

	// Build chunk map and collect additional IDs to fetch
	chunkMap := make(map[string]*types.Chunk, len(allChunks))
	var additionalChunkIDs []string
	processedChunkIDs := make(map[string]bool)

	// First pass: Build chunk map and collect parent IDs
	for _, chunk := range allChunks {
		chunkMap[chunk.ID] = chunk
		processedChunkIDs[chunk.ID] = true

		// Collect parent chunks
		if chunk.ParentChunkID != "" && !processedChunkIDs[chunk.ParentChunkID] {
			additionalChunkIDs = append(additionalChunkIDs, chunk.ParentChunkID)
			processedChunkIDs[chunk.ParentChunkID] = true

			// Pass score to parent
			chunkScores[chunk.ParentChunkID] = chunkScores[chunk.ID]
			chunkMatchTypes[chunk.ParentChunkID] = types.MatchTypeParentChunk
		}

		// Collect related chunks
		relationChunkIDs := s.collectRelatedChunkIDs(chunk, processedChunkIDs)
		for _, chunkID := range relationChunkIDs {
			additionalChunkIDs = append(additionalChunkIDs, chunkID)
			chunkMatchTypes[chunkID] = types.MatchTypeRelationChunk
		}

		// Add nearby chunks (prev and next)
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

	// Fetch all additional chunks in one go if needed
	if len(additionalChunkIDs) > 0 {
		logger.Infof(ctx, "Fetching %d additional chunks", len(additionalChunkIDs))
		additionalChunks, err := s.chunkRepo.ListChunksByID(ctx, tenantID, additionalChunkIDs)
		if err != nil {
			logger.Warnf(ctx, "Failed to fetch some additional chunks: %v", err)
			// Continue with what we have
		} else {
			// Add to chunk map
			for _, chunk := range additionalChunks {
				chunkMap[chunk.ID] = chunk
			}
		}
	}

	// Build final search results - preserve original order from input chunks
	var searchResults []*types.SearchResult
	addedChunkIDs := make(map[string]bool)

	// First pass: Add results in the original order from input chunks
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
			matchedContent := chunkMatchedContents[chunk.ID]
			searchResults = append(searchResults, s.buildSearchResult(chunk, knowledge, score, matchType, matchedContent))
			addedChunkIDs[chunk.ID] = true
		} else {
			logger.Warnf(ctx, "Knowledge not found for chunk: %s, knowledge_id: %s", chunk.ID, chunk.KnowledgeID)
		}
	}

	// Second pass: Add additional chunks (parent, nearby, relation) that weren't in original input
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
			matchedContent := chunkMatchedContents[chunkID]
			searchResults = append(searchResults, s.buildSearchResult(chunk, knowledge, score, matchType, matchedContent))
		}
	}
	logger.Infof(ctx, "Search results processed, total: %d", len(searchResults))
	return searchResults, nil
}

// collectRelatedChunkIDs extracts related chunk IDs from a chunk
func (s *knowledgeBaseService) collectRelatedChunkIDs(chunk *types.Chunk, processedIDs map[string]bool) []string {
	var relatedIDs []string
	// Process direct relations
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

// buildSearchResult creates a search result from chunk and knowledge
func (s *knowledgeBaseService) buildSearchResult(chunk *types.Chunk,
	knowledge *types.Knowledge,
	score float64,
	matchType types.MatchType,
	matchedContent string,
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
		MatchedContent:    matchedContent,
	}
}

// isValidTextChunk checks if a chunk is a valid text chunk
func (s *knowledgeBaseService) isValidTextChunk(chunk *types.Chunk) bool {
	return slices.Contains([]types.ChunkType{
		types.ChunkTypeText, types.ChunkTypeSummary,
		types.ChunkTypeTableColumn, types.ChunkTypeTableSummary,
		types.ChunkTypeFAQ,
	}, chunk.ChunkType)
}

// fetchKnowledgeData gets knowledge data in batch
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
