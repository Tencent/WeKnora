package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// ErrInvalidTenantID represents an error for invalid tenant ID
var ErrInvalidTenantID = errors.New("invalid tenant ID")

// knowledgeBaseService implements the knowledge base service interface
type knowledgeBaseService struct {
	repo         interfaces.KnowledgeBaseRepository
	kgRepo       interfaces.KnowledgeRepository
	chunkRepo    interfaces.ChunkRepository
	modelService interfaces.ModelService
}

// NewKnowledgeBaseService creates a new knowledge base service
func NewKnowledgeBaseService(repo interfaces.KnowledgeBaseRepository,
	kgRepo interfaces.KnowledgeRepository,
	chunkRepo interfaces.ChunkRepository,
	modelService interfaces.ModelService,
) interfaces.KnowledgeBaseService {
	return &knowledgeBaseService{
		repo:         repo,
		kgRepo:       kgRepo,
		chunkRepo:    chunkRepo,
		modelService: modelService,
	}
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
	kb.TenantID = ctx.Value(types.TenantIDContextKey).(uint)
	kb.UpdatedAt = time.Now()

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

	logger.Infof(ctx, "Retrieving knowledge base, ID: %s", id)

	kb, err := s.repo.GetKnowledgeBaseByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id": id,
		})
		return nil, err
	}

	logger.Infof(ctx, "Knowledge base retrieved successfully, ID: %s, name: %s", kb.ID, kb.Name)
	return kb, nil
}

// ListKnowledgeBases returns all knowledge bases for a tenant
func (s *knowledgeBaseService) ListKnowledgeBases(ctx context.Context) ([]*types.KnowledgeBase, error) {
	tenantID := ctx.Value(types.TenantIDContextKey).(uint)
	logger.Infof(ctx, "Retrieving knowledge base list for tenant, tenant ID: %d", tenantID)

	kbs, err := s.repo.ListKnowledgeBasesByTenantID(ctx, tenantID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": tenantID,
		})
		return nil, err
	}

	logger.Infof(
		ctx,
		"Knowledge base list retrieved successfully, tenant ID: %d, knowledge base count: %d",
		tenantID,
		len(kbs),
	)
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
	kb.UpdatedAt = time.Now()

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
func (s *knowledgeBaseService) DeleteKnowledgeBase(ctx context.Context, id string) error {
	if id == "" {
		logger.Error(ctx, "Knowledge base ID is empty")
		return errors.New("knowledge base ID cannot be empty")
	}

	logger.Infof(ctx, "Deleting knowledge base, ID: %s", id)

	err := s.repo.DeleteKnowledgeBase(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id": id,
		})
		return err
	}

	logger.Infof(ctx, "Knowledge base deleted successfully, ID: %s", id)
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
	tenantID := ctx.Value(types.TenantIDContextKey).(uint)
	var targetKB *types.KnowledgeBase
	if dstKB != "" {
		targetKB, err = s.repo.GetKnowledgeBaseByID(ctx, dstKB)
		if err != nil {
			return nil, nil, err
		}
	} else {
		targetKB = &types.KnowledgeBase{
			ID:                    uuid.New().String(),
			Name:                  sourceKB.Name,
			Description:           sourceKB.Description,
			TenantID:              tenantID,
			ChunkingConfig:        sourceKB.ChunkingConfig,
			ImageProcessingConfig: sourceKB.ImageProcessingConfig,
			EmbeddingModelID:      sourceKB.EmbeddingModelID,
			SummaryModelID:        sourceKB.SummaryModelID,
			RerankModelID:         sourceKB.RerankModelID,
			VLMModelID:            sourceKB.VLMModelID,
			StorageConfig:         sourceKB.StorageConfig,
		}
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
	logger.Infof(ctx, "Creating composite retrieval engine, tenant ID: %d", tenantInfo.ID)

	// Create a composite retrieval engine with tenant's configured retrievers
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(tenantInfo.RetrieverEngines.Engines)
	if err != nil {
		logger.Errorf(ctx, "Failed to create retrieval engine: %v", err)
		return nil, err
	}

	var retrieveParams []types.RetrieveParams
	var embeddingModel embedding.Embedder
	var kb *types.KnowledgeBase

	// Add vector retrieval params if supported
	if retrieveEngine.SupportRetriever(types.VectorRetrieverType) {
		logger.Info(ctx, "Vector retrieval supported, preparing vector retrieval parameters")

		kb, err = s.repo.GetKnowledgeBaseByID(ctx, id)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"knowledge_base_id": id,
			})
			return nil, err
		}

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

		retrieveParams = append(retrieveParams, types.RetrieveParams{
			Query:            params.QueryText,
			Embedding:        queryEmbedding,
			KnowledgeBaseIDs: []string{id},
			TopK:             params.MatchCount,
			Threshold:        params.VectorThreshold,
			RetrieverType:    types.VectorRetrieverType,
		})
		logger.Info(ctx, "Vector retrieval parameters setup completed")
	}

	// Add keyword retrieval params if supported
	if retrieveEngine.SupportRetriever(types.KeywordsRetrieverType) {
		logger.Info(ctx, "Keyword retrieval supported, preparing keyword retrieval parameters")
		retrieveParams = append(retrieveParams, types.RetrieveParams{
			Query:            params.QueryText,
			KnowledgeBaseIDs: []string{id},
			TopK:             params.MatchCount,
			Threshold:        params.KeywordThreshold,
			RetrieverType:    types.KeywordsRetrieverType,
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
	matchResults := []*types.IndexWithScore{}
	for _, retrieveResult := range retrieveResults {
		logger.Infof(ctx, "Retrieval results, engine: %v, retriever: %v, count: %v",
			retrieveResult.RetrieverEngineType,
			retrieveResult.RetrieverType,
			len(retrieveResult.Results),
		)
		matchResults = append(matchResults, retrieveResult.Results...)
	}

	// Early return if no results
	if len(matchResults) == 0 {
		logger.Info(ctx, "No search results found")
		return nil, nil
	}

	// Deduplicate results by chunk ID
	logger.Infof(ctx, "Result count before deduplication: %d", len(matchResults))
	deduplicatedChunks := common.Deduplicate(func(r *types.IndexWithScore) string { return r.ChunkID }, matchResults...)
	logger.Infof(ctx, "Result count after deduplication: %d", len(deduplicatedChunks))

	return s.processSearchResults(ctx, deduplicatedChunks)
}

// processSearchResults handles the processing of search results, optimizing database queries
func (s *knowledgeBaseService) processSearchResults(ctx context.Context,
	chunks []*types.IndexWithScore,
) ([]*types.SearchResult, error) {
	if len(chunks) == 0 {
		return nil, nil
	}

	tenantID := ctx.Value(types.TenantIDContextKey).(uint)

	// Prepare data structures for efficient processing
	var knowledgeIDs []string
	var chunkIDs []string
	chunkScores := make(map[string]float64)
	chunkMatchTypes := make(map[string]types.MatchType)
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
		if chunk.ChunkType == types.ChunkTypeText {
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

	// Build final search results
	var searchResults []*types.SearchResult
	for chunkID, chunk := range chunkMap {
		if !s.isValidTextChunk(chunk) {
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
	}
}

// isValidTextChunk checks if a chunk is a valid text chunk
func (s *knowledgeBaseService) isValidTextChunk(chunk *types.Chunk) bool {
	return slices.Contains([]types.ChunkType{
		types.ChunkTypeText, types.ChunkTypeSummary,
	}, chunk.ChunkType)
}

// fetchKnowledgeData gets knowledge data in batch
func (s *knowledgeBaseService) fetchKnowledgeData(ctx context.Context,
	tenantID uint,
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
