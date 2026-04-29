package weaviate

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/filters"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/graphql"
	"github.com/weaviate/weaviate/entities/models"
)

const (
	envWeaviateCollection = "WEAVIATE_COLLECTION"
	defaultCollectionName = "Weknora_embeddings"
	fieldContent          = "content"
	fieldSourceID         = "source_id"
	fieldSourceType       = "source_type"
	fieldChunkID          = "chunk_id"
	fieldKnowledgeID      = "knowledge_id"
	fieldKnowledgeBaseID  = "knowledge_base_id"
	fieldTagID            = "tag_id"
	fieldEmbedding        = "embedding"
	fieldIsEnabled        = "is_enabled"
	fieldID               = "id"
)

// NewWeaviateRetrieveEngineRepository creates and initializes a new Weaviate repository.
// indexCfg is optional — pass nil to use env var / default values (env path).
func NewWeaviateRetrieveEngineRepository(client *weaviate.Client, indexCfg *types.IndexConfig) interfaces.RetrieveEngineRepository {
	log := logger.GetLogger(context.Background())
	log.Info("[Weaviate] Initializing Weaviate retriever engine repository")

	collectionBaseName := types.ResolveCollectionName(indexCfg, envWeaviateCollection, defaultCollectionName)

	res := &weaviateRepository{
		client:             client,
		collectionBaseName: collectionBaseName,
		replicationFactor:  indexCfg.GetReplicationFactor(0),
		desiredShardCount:  indexCfg.GetDesiredShardCount(0),
	}

	log.Info("[Weaviate] Successfully initialized repository")
	return res
}

func (w *weaviateRepository) getCollectionName(dimension int) string {
	return fmt.Sprintf("%s_%d", w.collectionBaseName, dimension)
}

func (w *weaviateRepository) ensureCollection(ctx context.Context, dimension int) error {
	collectionName := w.getCollectionName(dimension)

	//Check cache first
	if _, ok := w.initializedCollections.Load(dimension); ok {
		return nil
	}

	log := logger.GetLogger(ctx)

	//Check if collection exists
	exists, err := w.client.Schema().ClassExistenceChecker().WithClassName(collectionName).Do(ctx)
	if err != nil {
		log.Errorf("[Weaviate] Failed to check collection existence: %v", err)
		return fmt.Errorf("failed to check collection existence: %w", err)
	}
	enabled := true
	if !exists {
		log.Infof("[Weaviate] Creating collection %s with dimension %d", collectionName, dimension)

		//定义class结构
		classObj := models.Class{
			Class:       collectionName,
			Description: fmt.Sprintf("WeKnora embeddings collection with dimension %d", dimension),
			VectorConfig: map[string]models.VectorConfig{
				fieldEmbedding: {
					VectorIndexType: "hnsw",
					VectorIndexConfig: map[string]interface{}{
						"distance":       "cosine",
						"efConstruction": 128,
						"maxConnections": 32,
						"ef":             64,
					},
					Vectorizer: map[string]interface{}{
						"none": map[string]interface{}{},
					},
				},
			},
			Properties: []*models.Property{
				{
					Name:         fieldContent,
					DataType:     []string{"text"},
					Tokenization: "gse",
				},
				{
					Name:     fieldSourceID,
					DataType: []string{"text"},
				},
				{
					Name:     fieldSourceType,
					DataType: []string{"int"},
				},
				{
					Name:            fieldChunkID,
					DataType:        []string{"text"},
					IndexFilterable: &enabled,
				},
				{
					Name:            fieldKnowledgeID,
					DataType:        []string{"text"},
					IndexFilterable: &enabled,
				},
				{
					Name:            fieldKnowledgeBaseID,
					DataType:        []string{"text"},
					IndexFilterable: &enabled,
				},
				{
					Name:            fieldTagID,
					DataType:        []string{"text"},
					IndexFilterable: &enabled,
				},
				{
					Name:            fieldIsEnabled,
					DataType:        []string{"boolean"},
					IndexFilterable: &enabled,
				},
			},
		}
		// Set replication factor if explicitly configured (> 0)
		if w.replicationFactor > 0 {
			classObj.ReplicationConfig = &models.ReplicationConfig{
				Factor: int64(w.replicationFactor),
			}
		}
		// Set shard count if explicitly configured (> 0)
		if w.desiredShardCount > 0 {
			classObj.ShardingConfig = map[string]interface{}{
				"desiredCount": w.desiredShardCount,
			}
		}
		//创建collection
		if err = w.client.Schema().ClassCreator().WithClass(&classObj).Do(ctx); err != nil {
			log.Errorf("[Weaviate] Failed to create collection: %v", err)
			return fmt.Errorf("failed to create collection: %w", err)
		}
		log.Infof("[Weaviate] Successfully created collection %s", collectionName)
	}
	w.initializedCollections.Store(dimension, true)
	return nil
}

func (w *weaviateRepository) EngineType() types.RetrieverEngineType {
	return types.WeaviateRetrieverEngineType
}

func (w *weaviateRepository) Support() []types.RetrieverType {
	return []types.RetrieverType{types.KeywordsRetrieverType, types.VectorRetrieverType}
}

// EstimateStorageSize calculates the estimated storage size for a list of indices
func (w *weaviateRepository) EstimateStorageSize(ctx context.Context,
	indexInfoList []*types.IndexInfo, params map[string]any,
) int64 {
	var totalStorageSize int64
	for _, embedding := range indexInfoList {
		embeddingDB := toWeaviateVectorEmbedding(embedding, params)
		totalStorageSize += w.calculateStorageSize(embeddingDB)
	}
	logger.GetLogger(ctx).Infof(
		"[Weaviate] Storage size for %d indices: %d bytes", len(indexInfoList), totalStorageSize,
	)
	return totalStorageSize
}

// Save stores a single point in Weaviate
func (w *weaviateRepository) Save(ctx context.Context,
	embedding *types.IndexInfo, additionalParams map[string]any,
) error {
	log := logger.GetLogger(ctx)
	log.Debugf("[Weaviate] Saving index for chunk ID: %s", embedding.ChunkID)

	embeddingDB := toWeaviateVectorEmbedding(embedding, additionalParams)
	if len(embeddingDB.Embedding) == 0 {
		err := fmt.Errorf("empty embedding vector for chunk ID: %s", embedding.ChunkID)
		log.Errorf("[Weaviate] %v", err)
		return err
	}

	dimension := len(embeddingDB.Embedding)
	if err := w.ensureCollection(ctx, dimension); err != nil {
		return err
	}
	collectionName := w.getCollectionName(dimension)
	dataSchema := createPayload(embeddingDB)

	id := embedding.ChunkID
	// Create point in Weaviate
	_, err := w.client.Data().Creator().
		WithClassName(collectionName).
		WithID(id).
		WithProperties(dataSchema).
		WithVector(embeddingDB.Embedding).
		Do(ctx)

	if err != nil {
		log.Errorf("[Weaviate] Failed to save index: %v", err)
		return err
	}
	log.Infof("[Weaviate] Successfully saved index for chunk ID: %s", embedding.ChunkID)
	return nil
}

// BatchSave stores multiple points in Milvus using batch insert
func (w *weaviateRepository) BatchSave(ctx context.Context,
	embeddingList []*types.IndexInfo, additionalParams map[string]any,
) error {
	log := logger.GetLogger(ctx)
	if len(embeddingList) == 0 {
		log.Warn("[Weaviate] Empty list provided to BatchSave, skipping")
		return nil
	}

	log.Infof("[Weaviate] Batch saving %d indices", len(embeddingList))

	// Group points by dimension
	embeddingsByDimension := make(map[int][]*types.IndexInfo)

	for _, embedding := range embeddingList {
		embeddingDB := toWeaviateVectorEmbedding(embedding, additionalParams)
		if len(embeddingDB.Embedding) == 0 {
			log.Warnf("[Weaviate] Skipping empty embedding for chunk ID: %s", embedding.ChunkID)
			continue
		}

		dimension := len(embeddingDB.Embedding)
		embeddingsByDimension[dimension] = append(embeddingsByDimension[dimension], embedding)
		log.Debugf("[Weaviate] Added chunk ID %s to batch request (dimension: %d)", embedding.ChunkID, dimension)
	}

	if len(embeddingsByDimension) == 0 {
		log.Warn("[Weaviate] No valid points to save after filtering")
		return nil
	}

	// Save points to each dimension-specific collection
	totalSaved := 0
	for dimension, embeddings := range embeddingsByDimension {
		batcher := w.client.Batch().ObjectsBatcher()
		if err := w.ensureCollection(ctx, dimension); err != nil {
			return err
		}
		collectionName := w.getCollectionName(dimension)
		for _, embedding := range embeddings {
			embeddingDB := toWeaviateVectorEmbedding(embedding, additionalParams)
			dataSchema := createPayload(embeddingDB)

			obj := &models.Object{
				Class:      collectionName,
				ID:         strfmt.UUID(embeddingDB.ChunkID),
				Properties: dataSchema,
				Vector:     embeddingDB.Embedding,
			}
			batcher.WithObjects(obj)
		}
		// Flush batch
		if _, err := batcher.Do(ctx); err != nil {
			log.Errorf("[Weaviate] Failed to execute batch operation for dimension %d: %v", dimension, err)
			return fmt.Errorf("failed to batch save (dimension %d): %w", dimension, err)
		}
		totalSaved += len(embeddings)
		log.Infof("[Weaviate] Saved %d points to collection %s", len(embeddings), collectionName)
	}

	log.Infof("[Weaviate] Successfully batch saved %d indices", totalSaved)
	return nil
}

// DeleteByChunkIDList removes points from the collection based on chunk IDs
func (w *weaviateRepository) DeleteByChunkIDList(ctx context.Context, chunkIDList []string, dimension int, knowledgeType string) error {
	log := logger.GetLogger(ctx)
	if len(chunkIDList) == 0 {
		log.Warn("[Weaviate] Empty chunk ID list provided for deletion, skipping")
		return nil
	}

	collectionName := w.getCollectionName(dimension)
	log.Infof("[Weaviate] Deleting indices by chunk IDs from %s, count: %d", collectionName, len(chunkIDList))

	//define filter
	filter := w.client.Batch().ObjectsBatchDeleter().
		WithClassName(collectionName).
		WithWhere(filters.Where().
			WithPath([]string{fieldChunkID}).
			WithOperator(filters.ContainsAny).
			WithValueText(chunkIDList...)).
		WithOutput("minimal")

	// Execute deletion
	if _, err := filter.Do(ctx); err != nil {
		log.Errorf("[Weaviate] Failed to delete by chunk IDs: %v", err)
		return fmt.Errorf("failed to delete by chunk IDs: %w", err)
	}
	log.Infof("[Weaviate] Successfully deleted documents by chunk IDs")
	return nil
}

// DeleteByKnowledgeIDList removes points from the collection based on knowledge IDs
func (w *weaviateRepository) DeleteByKnowledgeIDList(ctx context.Context,
	knowledgeIDList []string, dimension int, knowledgeType string,
) error {
	log := logger.GetLogger(ctx)
	if len(knowledgeIDList) == 0 {
		log.Warn("[Weaviate] Empty knowledge ID list provided for deletion, skipping")
		return nil
	}

	collectionName := w.getCollectionName(dimension)
	log.Infof("[Weaviate] Deleting indices by knowledge IDs from %s, count: %d", collectionName, len(knowledgeIDList))

	//define filter
	filter := w.client.Batch().ObjectsBatchDeleter().
		WithClassName(collectionName).
		WithWhere(filters.Where().
			WithPath([]string{fieldKnowledgeID}).
			WithOperator(filters.ContainsAny).
			WithValueText(knowledgeIDList...)).
		WithOutput("minimal")

	// Execute deletion
	if _, err := filter.Do(ctx); err != nil {
		log.Errorf("[Weaviate] Failed to delete by knowledge IDs: %v", err)
		return fmt.Errorf("failed to delete by knowledge IDs: %w", err)
	}
	log.Infof("[Weaviate] Successfully deleted documents by knowledge IDs")
	return nil
}

// DeleteBySourceIDList removes points from the collection based on source IDs
func (w *weaviateRepository) DeleteBySourceIDList(ctx context.Context,
	sourceIDList []string, dimension int, knowledgeType string,
) error {
	log := logger.GetLogger(ctx)
	if len(sourceIDList) == 0 {
		log.Warn("[Weaviate] Empty Source ID list provided for deletion, skipping")
		return nil
	}
	collectionName := w.getCollectionName(dimension)
	log.Infof("[Weaviate] Deleting indices by source IDs from %s, count: %d", collectionName, len(sourceIDList))

	//define filter
	filter := w.client.Batch().ObjectsBatchDeleter().
		WithClassName(collectionName).
		WithWhere(filters.Where().
			WithPath([]string{fieldSourceID}).
			WithOperator(filters.ContainsAny).
			WithValueText(sourceIDList...)).
		WithOutput("minimal")

	// Execute deletion
	if _, err := filter.Do(ctx); err != nil {
		log.Errorf("[Weaviate] Failed to delete by source IDs: %v", err)
		return fmt.Errorf("failed to delete by source IDs: %w", err)
	}
	log.Infof("[Weaviate] Successfully deleted documents by source IDs")
	return nil
}

// BatchUpdateChunkEnabledStatus updates the enabled status of chunks in batch
func (w *weaviateRepository) BatchUpdateChunkEnabledStatus(ctx context.Context, chunkStatusMap map[string]bool) error {
	log := logger.GetLogger(ctx)
	if len(chunkStatusMap) == 0 {
		log.Warn("[Weaviate] Empty chunk status map provided, skipping")
		return nil
	}

	log.Infof("[Weaviate] Batch updating chunk enabled status, count: %d", len(chunkStatusMap))

	// Get all collections
	collections, err := w.ListCollections(ctx)
	if err != nil {
		log.Errorf("[Weaviate] Failed to list collections: %v", err)
		return fmt.Errorf("failed to list collections: %w", err)
	}

	// Update in all matching collections
	for _, collectionName := range collections {
		// Only process collections that start with our base name
		if len(collectionName) <= len(w.collectionBaseName) ||
			collectionName[:len(w.collectionBaseName)] != w.collectionBaseName {
			continue
		}
		for chunkID, enabled := range chunkStatusMap {
			if err != nil {
				log.Errorf("[Weaviate] Failed to search ID by chunk ID %s in %s: %v", chunkID, collectionName, err)
				continue
			}
			err = w.client.Data().Updater().
				WithClassName(collectionName).
				WithID(chunkID).
				WithProperties(map[string]interface{}{
					fieldIsEnabled: enabled,
				}).
				Do(ctx)

			isEnabled := "enabled"
			if !enabled {
				isEnabled = "disabled"
			}

			if err != nil {
				log.Errorf("[Weaviate] Failed to update chunk %s status in %s: %v", isEnabled, collectionName, err)
				continue
			}
		}
	}
	log.Infof("[Weaviate] Batch update chunk enabled status completed")
	return nil
}

// BatchUpdateChunkTagID updates the tag ID of chunks in batch
func (w *weaviateRepository) BatchUpdateChunkTagID(ctx context.Context, chunkTagMap map[string]string) error {
	log := logger.GetLogger(ctx)
	if len(chunkTagMap) == 0 {
		log.Warn("[Weaviate] Empty chunk tag map provided, skipping")
		return nil
	}

	log.Infof("[Weaviate] Batch updating chunk tag ID, count: %d", len(chunkTagMap))

	// Get all collections
	collections, err := w.ListCollections(ctx)
	if err != nil {
		log.Errorf("[Weaviate] Failed to list collections: %v", err)
		return fmt.Errorf("failed to list collections: %w", err)
	}

	for _, collectionName := range collections {
		// Only process collections that start with our base name
		if len(collectionName) <= len(w.collectionBaseName) ||
			collectionName[:len(w.collectionBaseName)] != w.collectionBaseName {
			continue
		}

		for chunkID, tagID := range chunkTagMap {
			if err != nil {
				log.Warnf("[Weaviate] Failed to search ID by chunk ID %s in %s: %v", chunkID, collectionName, err)
				continue
			}
			err = w.client.Data().Updater().
				WithClassName(collectionName).
				WithID(chunkID).
				WithProperties(map[string]interface{}{
					fieldTagID: tagID,
				}).
				Do(ctx)
			if err != nil {
				log.Warnf("[Weaviate] Failed to update chunk %s tag ID in %s: %v", chunkID, collectionName, err)
				continue
			}
		}
	}
	log.Infof("[Weaviate] Batch update chunk tag ID completed")
	return nil

}

func (w *weaviateRepository) getBaseFilter(params types.RetrieveParams) *filters.WhereBuilder {
	var operands []*filters.WhereBuilder
	operands = append(operands, filters.Where().
		WithPath([]string{fieldIsEnabled}).
		WithOperator(filters.Equal).
		WithValueBoolean(true))

	if len(params.KnowledgeBaseIDs) > 0 {
		operands = append(operands, filters.Where().
			WithPath([]string{fieldKnowledgeBaseID}).
			WithOperator(filters.ContainsAny).
			WithValueText(params.KnowledgeBaseIDs...))
	}
	if len(params.KnowledgeIDs) > 0 {
		operands = append(operands, filters.Where().
			WithPath([]string{fieldKnowledgeID}).
			WithOperator(filters.ContainsAny).
			WithValueText(params.KnowledgeIDs...))
	}

	if len(params.TagIDs) > 0 {
		operands = append(operands, filters.Where().
			WithPath([]string{fieldTagID}).
			WithOperator(filters.ContainsAny).
			WithValueText(params.TagIDs...))
	}
	if len(params.ExcludeKnowledgeIDs) > 0 {
		operands = append(operands, filters.Where().
			WithPath([]string{fieldKnowledgeID}).
			WithOperator(filters.NotEqual).
			WithValueText(params.ExcludeKnowledgeIDs...))
	}
	if len(params.ExcludeChunkIDs) > 0 {
		operands = append(operands, filters.Where().
			WithPath([]string{fieldChunkID}).
			WithOperator(filters.NotEqual).
			WithValueText(params.ExcludeChunkIDs...))
	}

	return filters.Where().
		WithOperator(filters.And).
		WithOperands(operands)
}

// Retrieve dispatches the retrieval operation to the appropriate method based on retriever type
func (w *weaviateRepository) Retrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	log.Debugf("[Weaviate] Processing retrieval request of type: %s", params.RetrieverType)

	switch params.RetrieverType {
	case types.VectorRetrieverType:
		return w.VectorRetrieve(ctx, params)
	case types.KeywordsRetrieverType:
		return w.KeywordsRetrieve(ctx, params)
	}

	err := fmt.Errorf("invalid retriever type: %v", params.RetrieverType)
	log.Errorf("[Weaviate] %v", err)
	return nil, err
}

// VectorRetrieve performs vector similarity search
func (w *weaviateRepository) VectorRetrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	dimension := len(params.Embedding)
	log.Infof("[Weaviate] Vector retrieval: dim=%d, topK=%d, threshold=%.4f",
		dimension, params.TopK, params.Threshold)

	// Get collection name based on embedding dimension
	collectionName := w.getCollectionName(dimension)

	// Check if collection exists
	hasCollection, err := w.client.Schema().ClassExistenceChecker().WithClassName(collectionName).Do(ctx)
	if err != nil {
		log.Errorf("[Weaviate] Failed to check collection existence: %v", err)
		return nil, fmt.Errorf("failed to check collection: %w", err)
	}
	if !hasCollection {
		log.Warnf("[Weaviate] Collection %s does not exist, returning empty results", collectionName)
		return buildRetrieveResult(nil, types.VectorRetrieverType), nil
	}

	where := w.getBaseFilter(params)
	limit := params.TopK
	scoreThreshold := float32(params.Threshold)
	fields := getEmbeddingFields()
	result, err := w.client.GraphQL().Get().WithClassName(collectionName).
		WithWhere(where).
		WithLimit(limit).
		WithFields(fields...).
		WithNearVector(w.client.GraphQL().NearVectorArgBuilder().
			WithVector(params.Embedding).
			WithCertainty(scoreThreshold)).
		Do(ctx)

	if err != nil {
		log.Errorf("[Weaviate] Vector search failed: %v", err)
		return nil, fmt.Errorf("failed to search: %w", err)
	}
	if len(result.Errors) > 0 {
		log.Errorf("[Weaviate] Vector search failed: %v", result.Errors)
		return nil, fmt.Errorf("graphql search failed: %s", result.Errors[0].Message)
	}

	data, ok := result.Data["Get"].(map[string]interface{})
	if !ok || data[collectionName] == nil {
		log.Warnf("[Weaviate] No vector matches found that meet threshold %.4f", params.Threshold)
		return buildRetrieveResult(nil, types.VectorRetrieverType), nil
	}
	items := data[collectionName].([]interface{})
	results := parseGraphQLResponse(items, collectionName, types.MatchTypeEmbedding)

	if len(results) == 0 {
		log.Warnf("[Weaviate] No vector matches found that meet threshold %.4f", params.Threshold)
	} else {
		log.Infof("[Weaviate] Vector retrieval found %d results", len(results))
		log.Debugf("[Weaviate] Top result score: %.4f", results[0].Score)
	}

	return buildRetrieveResult(results, types.VectorRetrieverType), nil
}

// KeywordsRetrieve performs keyword-based search in document content
// This searches across all collections since keyword search doesn't depend on dimension
func (w *weaviateRepository) KeywordsRetrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	log.Infof("[Weaviate] Performing keywords retrieval with query: %s, topK: %d", params.Query, params.TopK)

	// Get all collections that match our base name pattern
	collections, err := w.ListCollections(ctx)
	if err != nil {
		log.Errorf("[Weaviate] Failed to list collections: %v", err)
		return nil, fmt.Errorf("failed to list collections: %w", err)
	}

	var allResults []*types.IndexWithScore

	for _, collectionName := range collections {
		log.Debugf("[Weaviate] Checking collection: %s", collectionName)
		// Only process collections that start with our base name
		if len(collectionName) <= len(w.collectionBaseName) ||
			collectionName[:len(w.collectionBaseName)] != w.collectionBaseName {
			log.Debugf("[Weaviate] Skipping collection %s (doesn't match base name %s)", collectionName, w.collectionBaseName)
			continue
		}

		filter := w.getBaseFilter(params)

		//bm25 search
		bm25 := w.client.GraphQL().Bm25ArgBuilder().
			WithQuery(params.Query).
			WithProperties([]string{fieldContent}...)

		fields := getKeywordsFields()

		result, err := w.client.GraphQL().Get().WithClassName(collectionName).
			WithWhere(filter).
			WithLimit(params.TopK).
			WithFields(fields...).
			WithBM25(bm25).
			Do(ctx)

		if err != nil {
			log.Errorf("[Weaviate] keywords search failed: %v", err)
			return nil, fmt.Errorf("failed to search: %w", err)
		}
		if len(result.Errors) > 0 {
			log.Errorf("[Weaviate] keywords search failed: %v", result.Errors)
			return nil, fmt.Errorf("graphql search failed: %s", result.Errors[0].Message)
		}
		data, ok := result.Data["Get"].(map[string]interface{})
		if !ok || data[collectionName] == nil {
			log.Warnf("[Weaviate] No keywords matches found that meet threshold %.4f", params.Threshold)
			continue
		}
		items := data[collectionName].([]interface{})
		results := parseGraphQLResponse(items, collectionName, types.MatchTypeKeywords)
		allResults = append(allResults, results...)
	}

	// Limit results to topK
	if len(allResults) > params.TopK {
		allResults = allResults[:params.TopK]
	}

	if len(allResults) == 0 {
		log.Warnf("[Weaviate] No keyword matches found for query: %s", params.Query)
	} else {
		log.Infof("[Weaviate] Keywords retrieval found %d results", len(allResults))
	}
	return buildRetrieveResult(allResults, types.KeywordsRetrieverType), nil
}

// CopyIndices copies index data from source knowledge base to target knowledge base
func (w *weaviateRepository) CopyIndices(ctx context.Context,
	sourceKnowledgeBaseID string,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
	dimension int,
	knowledgeType string,
) error {
	log := logger.GetLogger(ctx)
	log.Infof("[Weaviate] Copying indices from %s to %s, count: %d",
		sourceKnowledgeBaseID, targetKnowledgeBaseID, len(sourceToTargetChunkIDMap))

	if len(sourceToTargetChunkIDMap) == 0 {
		return nil
	}

	collectionName := w.getCollectionName(dimension)
	batchSize := 64
	var lastID string
	totalCopied := 0
	fields := getVectorFields()

	for {
		result, err := w.client.GraphQL().Get().
			WithClassName(collectionName).
			WithWhere(filters.Where().
				WithPath([]string{fieldKnowledgeBaseID}).
				WithOperator(filters.Equal).
				WithValueString(sourceKnowledgeBaseID)).
			WithLimit(batchSize).
			WithFields(fields...).
			WithAfter(lastID).
			Do(ctx)
		if err != nil {
			log.Errorf("[Weaviate] Failed to query source points: %v", err)
			return err
		}

		objects, ok := result.Data["Get"].(map[string]interface{})[collectionName].([]interface{})
		if !ok || len(objects) == 0 {
			break
		}
		log.Infof("[Weaviate] Found %d source points in batch", len(objects))

		batcher := w.client.Batch().ObjectsBatcher()
		currentBatchCount := 0

		targetObjects := make([]*models.Object, 0, len(objects))
		for _, obj := range objects {
			data, ok := obj.(map[string]interface{})
			if !ok {
				continue
			}
			additional, ok := data["_additional"].(map[string]interface{})
			if !ok {
				continue
			}

			lastID = additional["id"].(string)

			sourceChunkID, ok := data[fieldChunkID].(string)
			if !ok {
				continue
			}
			sourceKnowledgeID, ok := data[fieldKnowledgeID].(string)
			if !ok {
				continue
			}
			originalSourceID, ok := data[fieldSourceID].(string)
			if !ok {
				continue
			}

			targetChunkID, ok1 := sourceToTargetChunkIDMap[sourceChunkID]
			targetKnowledgeID, ok2 := sourceToTargetKBIDMap[sourceKnowledgeID]
			if !ok1 || !ok2 {
				continue
			}

			// Handle SourceID transformation for generated questions
			// Generated questions have SourceID format: {chunkID}-{questionID}
			// Regular chunks have SourceID == ChunkID
			var targetSourceID string
			if originalSourceID == sourceChunkID {
				// Regular chunk, use targetChunkID as SourceID
				targetSourceID = targetChunkID
			} else if strings.HasPrefix(originalSourceID, sourceChunkID+"-") {
				// This is a generated question, preserve the questionID part
				questionID := strings.TrimPrefix(originalSourceID, sourceChunkID+"-")
				targetSourceID = fmt.Sprintf("%s-%s", targetChunkID, questionID)
			} else {
				// For other complex scenarios, generate new unique SourceID
				targetSourceID = uuid.New().String()
			}

			vectorRaw, ok := additional["vector"].([]interface{})
			if !ok {
				continue
			}
			vector := make([]float32, len(vectorRaw))
			for i, v := range vectorRaw {
				vector[i] = float32(v.(float64))
			}

			isEnabled := true
			newObj := &models.Object{
				Class: collectionName,
				ID:    strfmt.UUID(uuid.New().String()),
				Properties: map[string]interface{}{
					fieldContent:         data[fieldContent],
					fieldSourceID:        targetSourceID,
					fieldSourceType:      data[fieldSourceType],
					fieldChunkID:         targetChunkID,
					fieldKnowledgeID:     targetKnowledgeID,
					fieldKnowledgeBaseID: targetKnowledgeBaseID,
					fieldTagID:           data[fieldTagID],
					fieldIsEnabled:       isEnabled,
				},
				Vector: vector,
			}
			targetObjects = append(targetObjects, newObj)
			currentBatchCount++
		}
		if len(targetObjects) > 0 {
			resp, err := batcher.WithObjects(targetObjects...).Do(ctx)
			if err != nil {
				return fmt.Errorf("batch upsert failed: %w", err)
			}
			for _, r := range resp {
				if r.Result.Errors != nil {
					log.Errorf("[Weaviate] Object error: %v", r.Result.Errors.Error[0].Message)
				}
			}
			totalCopied += len(targetObjects)
			log.Infof("[Weaviate] Successfully copied batch, total: %d", totalCopied)
		}
	}
	log.Infof("[Weaviate] Index copy completed, total copied: %d", totalCopied)
	return nil
}

func (w *weaviateRepository) ListCollections(ctx context.Context) ([]string, error) {
	schema, err := w.client.Schema().Getter().Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("weaviate 获取 schema 失败: %w", err)
	}
	var collectionNames []string
	for _, class := range schema.Classes {
		collectionNames = append(collectionNames, class.Class)
	}

	return collectionNames, nil
}

func createPayload(embedding *WeaviateVectorEmbedding) map[string]interface{} {
	payload := map[string]any{
		fieldContent:         embedding.Content,
		fieldSourceID:        embedding.SourceID,
		fieldSourceType:      int64(embedding.SourceType),
		fieldChunkID:         embedding.ChunkID,
		fieldKnowledgeID:     embedding.KnowledgeID,
		fieldKnowledgeBaseID: embedding.KnowledgeBaseID,
		fieldTagID:           embedding.TagID,
		fieldIsEnabled:       embedding.IsEnabled,
	}
	return payload
}

func buildRetrieveResult(results []*types.IndexWithScore, retrieverType types.RetrieverType) []*types.RetrieveResult {
	return []*types.RetrieveResult{
		{
			Results:             results,
			RetrieverEngineType: types.WeaviateRetrieverEngineType,
			RetrieverType:       retrieverType,
			Error:               nil,
		},
	}
}

func getKeywordsFields() []graphql.Field {
	return []graphql.Field{
		{Name: fieldContent},
		{Name: fieldSourceID},
		{Name: fieldSourceType},
		{Name: fieldChunkID},
		{Name: fieldKnowledgeID},
		{Name: fieldKnowledgeBaseID},
		{Name: fieldTagID},
		{
			Name: "_additional",
			Fields: []graphql.Field{
				{Name: "id"},
				{Name: "score"},
			},
		},
	}
}

func getEmbeddingFields() []graphql.Field {
	return []graphql.Field{
		{Name: fieldContent},
		{Name: fieldSourceID},
		{Name: fieldSourceType},
		{Name: fieldChunkID},
		{Name: fieldKnowledgeID},
		{Name: fieldKnowledgeBaseID},
		{Name: fieldTagID},
		{
			Name: "_additional",
			Fields: []graphql.Field{
				{Name: "id"},
				{Name: "certainty"},
			},
		},
	}
}

func getVectorFields() []graphql.Field {
	return []graphql.Field{
		{Name: fieldContent},
		{Name: fieldSourceID},
		{Name: fieldSourceType},
		{Name: fieldChunkID},
		{Name: fieldKnowledgeID},
		{Name: fieldKnowledgeBaseID},
		{Name: fieldTagID},
		{
			Name: "_additional",
			Fields: []graphql.Field{
				{Name: "id"},
				{Name: "vector"},
			},
		},
	}
}

// parseGraphQLResponse parses the GraphQL response for vector embeddings or keyword search results.
func parseGraphQLResponse(items []interface{}, collectionName string, matchType types.MatchType) []*types.IndexWithScore {
	var results []*types.IndexWithScore
	var additionalName string
	if matchType == types.MatchTypeEmbedding {
		additionalName = "certainty"
	} else {
		additionalName = "score"
	}
	for _, item := range items {
		obj := item.(map[string]interface{})
		additional := obj["_additional"].(map[string]interface{})

		pointID := additional["id"].(string)
		score := 0.0
		if s, ok := additional[additionalName].(float64); ok {
			if matchType == types.MatchTypeKeywords {
				score = 1.0
			} else {
				score = s
			}
		}

		getString := func(key string) string {
			if v, ok := obj[key].(string); ok {
				return v
			}
			return ""
		}
		getInt := func(key string) int {
			if v, ok := obj[key].(float64); ok {
				return int(v)
			}
			return 0
		}

		embedding := &WeaviateVectorEmbeddingWithScore{
			WeaviateVectorEmbedding{
				Content:         getString(fieldContent),
				SourceID:        getString(fieldSourceID),
				SourceType:      getInt(fieldSourceType),
				ChunkID:         getString(fieldChunkID),
				KnowledgeID:     getString(fieldKnowledgeID),
				KnowledgeBaseID: getString(fieldKnowledgeBaseID),
				TagID:           getString(fieldTagID),
			},
			float64(score),
		}

		results = append(results, fromWeaviateVectorEmbedding(pointID, embedding, matchType))
	}
	return results
}

// Ref: https://github.com/weaviate/weaviate/blob/b4aec91c6fe464df50e9fa1e2d643322fbb85679/entities/vectorindex/hnsw/config.go#L27
func (w *weaviateRepository) calculateStorageSize(embedding *WeaviateVectorEmbedding) int64 {
	// Payload fields
	payloadSizeBytes := int64(0)
	payloadSizeBytes += int64(len(embedding.Content))         // content string
	payloadSizeBytes += int64(len(embedding.SourceID))        // source_id string
	payloadSizeBytes += int64(len(embedding.ChunkID))         // chunk_id string
	payloadSizeBytes += int64(len(embedding.KnowledgeID))     // knowledge_id string
	payloadSizeBytes += int64(len(embedding.KnowledgeBaseID)) // knowledge_base_id string
	payloadSizeBytes += 8                                     // source_type int64

	// Vector storage and index
	var vectorSizeBytes int64 = 0
	var hnswIndexBytes int64 = 0
	if embedding.Embedding != nil {
		dimensions := int64(len(embedding.Embedding))
		vectorSizeBytes = dimensions * 4

		// HNSW graph links per vector: M×2 neighbors in layer 0, ~8 bytes per link
		// (4 bytes for neighbor ID + multi-layer amortization).
		// Graph link count depends on M, NOT on vector dimensions.
		const hnswM = 32
		hnswIndexBytes = hnswM * 2 * 8
	}

	// ID tracker metadata: 24 bytes per vector
	// (forward refs + backward refs + version tracking = 8 + 8 + 8)
	const idTrackerBytes int64 = 24

	totalSizeBytes := payloadSizeBytes + vectorSizeBytes + hnswIndexBytes + idTrackerBytes
	return totalSizeBytes
}

// toWeaviateVectorEmbedding converts IndexInfo to Weaviate payload format
func toWeaviateVectorEmbedding(embedding *types.IndexInfo, additionalParams map[string]interface{}) *WeaviateVectorEmbedding {
	vector := &WeaviateVectorEmbedding{
		Content:         embedding.Content,
		SourceID:        embedding.SourceID,
		SourceType:      int(embedding.SourceType),
		ChunkID:         embedding.ChunkID,
		KnowledgeID:     embedding.KnowledgeID,
		KnowledgeBaseID: embedding.KnowledgeBaseID,
		TagID:           embedding.TagID,
		IsEnabled:       embedding.IsEnabled,
	}
	if additionalParams != nil && slices.Contains(slices.Collect(maps.Keys(additionalParams)), fieldEmbedding) {
		if embeddingMap, ok := additionalParams[fieldEmbedding].(map[string][]float32); ok {
			vector.Embedding = embeddingMap[embedding.SourceID]
		}
	}
	return vector
}

// fromWeaviateVectorEmbedding converts Weaviate point to IndexWithScore domain model
func fromWeaviateVectorEmbedding(id string,
	embedding *WeaviateVectorEmbeddingWithScore,
	matchType types.MatchType,
) *types.IndexWithScore {
	return &types.IndexWithScore{
		ID:              id,
		SourceID:        embedding.SourceID,
		SourceType:      types.SourceType(embedding.SourceType),
		ChunkID:         embedding.ChunkID,
		KnowledgeID:     embedding.KnowledgeID,
		KnowledgeBaseID: embedding.KnowledgeBaseID,
		TagID:           embedding.TagID,
		Content:         embedding.Content,
		Score:           embedding.Score,
		MatchType:       matchType,
	}
}

// tokenizeQuery splits a query string into tokens for OR-based full-text search.
// It uses jieba for professional Chinese word segmentation.
func tokenizeQuery(query string) []string {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}

	// Use jieba for segmentation (search mode for better recall)
	words := types.Jieba.CutForSearch(query, true)

	// Filter and deduplicate
	seen := make(map[string]bool)
	result := make([]string, 0, len(words))
	for _, word := range words {
		word = strings.TrimSpace(strings.ToLower(word))
		// Skip empty, single-char, and already seen words
		if utf8.RuneCountInString(word) < 2 || seen[word] {
			continue
		}
		seen[word] = true
		result = append(result, word)
	}

	return result
}
