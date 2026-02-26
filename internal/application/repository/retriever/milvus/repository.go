package milvus

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	client "github.com/milvus-io/milvus/client/v2/milvusclient"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	envQdrantCollection   = "MILVUS_COLLECTION"
	defaultCollectionName = "weknora_embeddings"
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
	fieldContentSparse    = "content_sparse"
)

var (
	allFields = []string{fieldID, fieldContent, fieldSourceID, fieldSourceType, fieldChunkID,
		fieldKnowledgeID, fieldKnowledgeBaseID, fieldTagID, fieldIsEnabled, fieldEmbedding}
)

// NewMilvusRetrieveEngineRepository creates and initializes a new Milvus repository
func NewMilvusRetrieveEngineRepository(client *client.Client) interfaces.RetrieveEngineRepository {
	log := logger.GetLogger(context.Background())
	log.Info("[Milvus] Initializing Milvus retriever engine repository")

	collectionBaseName := os.Getenv(envQdrantCollection)
	if collectionBaseName == "" {
		log.Warn("[Milvus] MILVUS_COLLECTION environment variable not set, using default collection name")
		collectionBaseName = defaultCollectionName
	}

	res := &milvusRepository{
		filter:             filter{},
		client:             client,
		collectionBaseName: collectionBaseName,
	}

	log.Info("[Milvus] Successfully initialized repository")
	return res
}

// getCollectionName returns the collection name for a specific dimension
func (m *milvusRepository) getCollectionName(dimension int) string {
	return fmt.Sprintf("%s_%d", m.collectionBaseName, dimension)
}

// ensureCollection ensures the collection exists for the given dimension
func (m *milvusRepository) ensureCollection(ctx context.Context, dimension int) error {
	collectionName := m.getCollectionName(dimension)

	// Check cache first
	if _, ok := m.initializedCollections.Load(dimension); ok {
		return nil
	}

	log := logger.GetLogger(ctx)

	// Check if collection exists
	hasCollection, err := m.client.HasCollection(ctx, client.NewHasCollectionOption(collectionName))
	if err != nil {
		log.Errorf("[Milvus] Failed to check collection existence: %v", err)
		return fmt.Errorf("failed to check collection existence: %w", err)
	}

	if !hasCollection {
		log.Infof("[Milvus] Creating collection %s with dimension %d", collectionName, dimension)

		// Define schema
		schema := &entity.Schema{
			CollectionName: collectionName,
			Description:    fmt.Sprintf("WeKnora embeddings collection with dimension %d", dimension),
			AutoID:         false,
			Fields: []*entity.Field{
				entity.NewField().
					WithName(fieldID).
					WithDataType(entity.FieldTypeVarChar).
					WithIsPrimaryKey(true).
					WithMaxLength(1024),
				entity.NewField().
					WithName(fieldEmbedding).
					WithDataType(entity.FieldTypeFloatVector).
					WithDim(int64(dimension)),
				entity.NewField().
					WithName(fieldContent).
					WithDataType(entity.FieldTypeVarChar).
					WithMaxLength(65535).
					WithEnableAnalyzer(true).
					WithEnableMatch(true),
				entity.NewField().
					WithName(fieldContentSparse).
					WithDataType(entity.FieldTypeSparseVector),
				entity.NewField().
					WithName(fieldSourceID).
					WithDataType(entity.FieldTypeVarChar).
					WithMaxLength(255),
				entity.NewField().
					WithName(fieldSourceType).
					WithDataType(entity.FieldTypeInt64),
				entity.NewField().
					WithName(fieldChunkID).
					WithDataType(entity.FieldTypeVarChar).
					WithMaxLength(255),
				entity.NewField().
					WithName(fieldKnowledgeID).
					WithDataType(entity.FieldTypeVarChar).
					WithMaxLength(255),
				entity.NewField().
					WithName(fieldKnowledgeBaseID).
					WithDataType(entity.FieldTypeVarChar).
					WithMaxLength(255),
				entity.NewField().
					WithName(fieldTagID).
					WithDataType(entity.FieldTypeVarChar).
					WithMaxLength(255),
				entity.NewField().
					WithName(fieldIsEnabled).
					WithDataType(entity.FieldTypeBool),
			},
		}

		// Add BM25 function for content sparse vector
		// ref: https://milvus.io/docs/zh/full-text-search.md
		schema.WithFunction(entity.NewFunction().
			WithName("text_bm25_emb").
			WithInputFields(fieldContent).
			WithOutputFields(fieldContentSparse).
			WithType(entity.FunctionTypeBM25))

		indexOpts := make([]client.CreateIndexOption, 0)
		// hnsw index for embedding field
		indexOpts = append(indexOpts, client.NewCreateIndexOption(collectionName, fieldEmbedding, index.NewHNSWIndex(entity.IP, 16, 128)))
		indexOpts = append(indexOpts, client.NewCreateIndexOption(collectionName, fieldContentSparse, index.NewAutoIndex(entity.BM25)))
		// Create payload indexes for filtering
		indexFields := []string{fieldChunkID, fieldKnowledgeID, fieldKnowledgeBaseID, fieldSourceID, fieldIsEnabled}
		for _, fieldName := range indexFields {
			indexOpts = append(indexOpts, client.NewCreateIndexOption(collectionName, fieldName, index.NewAutoIndex(entity.IP)))
		}

		// Create collection
		err = m.client.CreateCollection(ctx, client.NewCreateCollectionOption(collectionName, schema).WithIndexOptions(indexOpts...))
		if err != nil {
			log.Errorf("[Milvus] Failed to create collection: %v", err)
			return fmt.Errorf("failed to create collection: %w", err)
		}

		log.Infof("[Milvus] Successfully created collection %s", collectionName)
	}

	loadTask, err := m.client.LoadCollection(ctx, client.NewLoadCollectionOption(collectionName))
	if err != nil {
		log.Errorf("[Milvus] Failed to load collection: %v", err)
		return fmt.Errorf("failed to load collection: %w", err)
	}
	if err := loadTask.Await(ctx); err != nil {
		log.Errorf("[Milvus] Failed to await load collection: %v", err)
		return fmt.Errorf("failed to await load collection: %w", err)
	}

	// Mark as initialized
	m.initializedCollections.Store(dimension, true)
	return nil
}

func (m *milvusRepository) EngineType() types.RetrieverEngineType {
	return types.MilvusRetrieverEngineType
}

func (m *milvusRepository) Support() []types.RetrieverType {
	return []types.RetrieverType{types.KeywordsRetrieverType, types.VectorRetrieverType}
}

// EstimateStorageSize calculates the estimated storage size for a list of indices
func (m *milvusRepository) EstimateStorageSize(ctx context.Context,
	indexInfoList []*types.IndexInfo, params map[string]any,
) int64 {
	var totalStorageSize int64
	for _, embedding := range indexInfoList {
		embeddingDB := toMilvusVectorEmbedding(embedding, params)
		totalStorageSize += m.calculateStorageSize(embeddingDB)
	}
	logger.GetLogger(ctx).Infof(
		"[Milvus] Storage size for %d indices: %d bytes", len(indexInfoList), totalStorageSize,
	)
	return totalStorageSize
}

// Save stores a single point in Milvus
func (m *milvusRepository) Save(ctx context.Context,
	embedding *types.IndexInfo,
	additionalParams map[string]any,
) error {
	log := logger.GetLogger(ctx)
	log.Debugf("[Milvus] Saving index for chunk ID: %s", embedding.ChunkID)

	embeddingDB := toMilvusVectorEmbedding(embedding, additionalParams)
	if len(embeddingDB.Embedding) == 0 {
		err := fmt.Errorf("empty embedding vector for chunk ID: %s", embedding.ChunkID)
		log.Errorf("[Milvus] %v", err)
		return err
	}

	dimension := len(embeddingDB.Embedding)
	if err := m.ensureCollection(ctx, dimension); err != nil {
		return err
	}

	collectionName := m.getCollectionName(dimension)

	embeddingDB.ID = uuid.New().String()
	opts := createUpsert(collectionName, []*MilvusVectorEmbedding{embeddingDB})

	_, err := m.client.Upsert(ctx, opts)
	if err != nil {
		log.Errorf("[Milvus] Failed to save index: %v", err)
		return err
	}

	log.Infof("[Milvus] Successfully saved index for chunk ID: %s", embedding.ChunkID)
	return nil
}

// BatchSave stores multiple points in Milvus using batch insert
func (m *milvusRepository) BatchSave(ctx context.Context,
	embeddingList []*types.IndexInfo, additionalParams map[string]any,
) error {
	log := logger.GetLogger(ctx)
	if len(embeddingList) == 0 {
		log.Warn("[Milvus] Empty list provided to BatchSave, skipping")
		return nil
	}

	log.Infof("[Milvus] Batch saving %d indices", len(embeddingList))

	// Group points by dimension
	embeddingsByDimension := make(map[int][]*types.IndexInfo)

	for _, embedding := range embeddingList {
		embeddingDB := toMilvusVectorEmbedding(embedding, additionalParams)
		if len(embeddingDB.Embedding) == 0 {
			log.Warnf("[Milvus] Skipping empty embedding for chunk ID: %s", embedding.ChunkID)
			continue
		}

		dimension := len(embeddingDB.Embedding)
		embeddingsByDimension[dimension] = append(embeddingsByDimension[dimension], embedding)
		log.Debugf("[Milvus] Added chunk ID %s to batch request (dimension: %d)", embedding.ChunkID, dimension)
	}

	if len(embeddingsByDimension) == 0 {
		log.Warn("[Milvus] No valid points to save after filtering")
		return nil
	}

	// Save points to each dimension-specific collection
	totalSaved := 0
	for dimension, embeddings := range embeddingsByDimension {
		if err := m.ensureCollection(ctx, dimension); err != nil {
			return err
		}

		collectionName := m.getCollectionName(dimension)
		n := len(embeddings)
		embeddingDBList := make([]*MilvusVectorEmbedding, 0, n)

		for _, embedding := range embeddings {
			embeddingDB := toMilvusVectorEmbedding(embedding, additionalParams)
			embeddingDB.ID = uuid.New().String()
			embeddingDBList = append(embeddingDBList, embeddingDB)
		}
		opts := createUpsert(collectionName, embeddingDBList)
		_, err := m.client.Upsert(ctx, opts)
		if err != nil {
			log.Errorf("[Milvus] Failed to execute batch operation for dimension %d: %v", dimension, err)
			return fmt.Errorf("failed to batch save (dimension %d): %w", dimension, err)
		}
		totalSaved += n
		log.Infof("[Milvus] Saved %d points to collection %s", n, collectionName)
	}

	log.Infof("[Milvus] Successfully batch saved %d indices", totalSaved)
	return nil
}

// DeleteByChunkIDList removes points from the collection based on chunk IDs
func (m *milvusRepository) DeleteByChunkIDList(ctx context.Context, chunkIDList []string, dimension int, knowledgeType string) error {
	log := logger.GetLogger(ctx)
	if len(chunkIDList) == 0 {
		log.Warn("[Milvus] Empty chunk ID list provided for deletion, skipping")
		return nil
	}

	collectionName := m.getCollectionName(dimension)
	log.Infof("[Milvus] Deleting indices by chunk IDs from %s, count: %d", collectionName, len(chunkIDList))

	deleteOpt := client.NewDeleteOption(collectionName)
	deleteOpt.WithStringIDs(fieldChunkID, chunkIDList)
	_, err := m.client.Delete(ctx, deleteOpt)
	if err != nil {
		log.Errorf("[Milvus] Failed to delete by chunk IDs: %v", err)
		return fmt.Errorf("failed to delete by chunk IDs: %w", err)
	}

	log.Infof("[Milvus] Successfully deleted documents by chunk IDs")
	return nil
}

// DeleteByKnowledgeIDList removes points from the collection based on knowledge IDs
func (m *milvusRepository) DeleteByKnowledgeIDList(ctx context.Context,
	knowledgeIDList []string, dimension int, knowledgeType string,
) error {
	log := logger.GetLogger(ctx)
	if len(knowledgeIDList) == 0 {
		log.Warn("[Milvus] Empty knowledge ID list provided for deletion, skipping")
		return nil
	}

	collectionName := m.getCollectionName(dimension)
	log.Infof("[Milvus] Deleting indices by knowledge IDs from %s, count: %d", collectionName, len(knowledgeIDList))

	deleteOpt := client.NewDeleteOption(collectionName)
	deleteOpt.WithStringIDs(fieldKnowledgeID, knowledgeIDList)
	_, err := m.client.Delete(ctx, deleteOpt)
	if err != nil {
		log.Errorf("[Milvus] Failed to delete by knowledge IDs: %v", err)
		return fmt.Errorf("failed to delete by knowledge IDs: %w", err)
	}

	log.Infof("[Milvus] Successfully deleted documents by knowledge IDs")
	return nil
}

// DeleteBySourceIDList removes points from the collection based on source IDs
func (m *milvusRepository) DeleteBySourceIDList(ctx context.Context,
	sourceIDList []string, dimension int, knowledgeType string,
) error {
	log := logger.GetLogger(ctx)
	if len(sourceIDList) == 0 {
		log.Warn("[Milvus] Empty source ID list provided for deletion, skipping")
		return nil
	}

	collectionName := m.getCollectionName(dimension)
	log.Infof("[Milvus] Deleting indices by source IDs from %s, count: %d", collectionName, len(sourceIDList))

	deleteOpt := client.NewDeleteOption(collectionName)
	deleteOpt.WithStringIDs(fieldSourceID, sourceIDList)
	_, err := m.client.Delete(ctx, deleteOpt)
	if err != nil {
		log.Errorf("[Milvus] Failed to delete by source IDs: %v", err)
		return fmt.Errorf("failed to delete by source IDs: %w", err)
	}

	log.Infof("[Milvus] Successfully deleted documents by source IDs")
	return nil
}

// BatchUpdateChunkEnabledStatus updates the enabled status of chunks in batch
func (m *milvusRepository) BatchUpdateChunkEnabledStatus(ctx context.Context, chunkStatusMap map[string]bool) error {
	log := logger.GetLogger(ctx)
	if len(chunkStatusMap) == 0 {
		log.Warn("[Milvus] Empty chunk status map provided, skipping")
		return nil
	}

	log.Infof("[Milvus] Batch updating chunk enabled status, count: %d", len(chunkStatusMap))

	// Get all collections
	collections, err := m.client.ListCollections(ctx, client.NewListCollectionOption())
	if err != nil {
		log.Errorf("[Milvus] Failed to list collections: %v", err)
		return fmt.Errorf("failed to list collections: %w", err)
	}

	// Group chunks by enabled status for batch updates
	enabledChunkIDs := make([]string, 0)
	disabledChunkIDs := make([]string, 0)

	for chunkID, enabled := range chunkStatusMap {
		if enabled {
			enabledChunkIDs = append(enabledChunkIDs, chunkID)
		} else {
			disabledChunkIDs = append(disabledChunkIDs, chunkID)
		}
	}

	// Update in all matching collections
	for _, collectionName := range collections {
		// Only process collections that start with our base name
		if len(collectionName) <= len(m.collectionBaseName) ||
			collectionName[:len(m.collectionBaseName)] != m.collectionBaseName {
			continue
		}
		enabledEmbeddings, _, err := m.searchByFilter(ctx, collectionName, &universalFilterCondition{
			Field:    fieldChunkID,
			Operator: operatorIn,
			Value:    enabledChunkIDs,
		}, nil, nil)
		if err != nil {
			log.Warnf("[Milvus] Failed to search enabled chunks in %s: %v", collectionName, err)
			continue
		}
		upsertEmbeddings := make([]*MilvusVectorEmbedding, 0, len(enabledEmbeddings))
		for _, embedding := range enabledEmbeddings {
			embedding.IsEnabled = true
			upsertEmbeddings = append(upsertEmbeddings, &embedding.MilvusVectorEmbedding)
		}
		if len(upsertEmbeddings) > 0 {
			enabledReq := createUpsert(collectionName, upsertEmbeddings)
			_, err := m.client.Upsert(ctx, enabledReq)
			if err != nil {
				log.Warnf("[Milvus] Failed to update enabled chunks in %s: %v", collectionName, err)
				continue
			}
		}

		disabledEmbeddings, _, err := m.searchByFilter(ctx, collectionName, &universalFilterCondition{
			Field:    fieldChunkID,
			Operator: operatorIn,
			Value:    disabledChunkIDs,
		}, nil, nil)
		if err != nil {
			log.Warnf("[Milvus] Failed to search disabled chunks in %s: %v", collectionName, err)
			continue
		}
		upsertEmbeddings = make([]*MilvusVectorEmbedding, 0, len(disabledEmbeddings))
		for _, embedding := range disabledEmbeddings {
			embedding.IsEnabled = false
			upsertEmbeddings = append(upsertEmbeddings, &embedding.MilvusVectorEmbedding)
		}
		if len(upsertEmbeddings) > 0 {
			disabledReq := createUpsert(collectionName, upsertEmbeddings)
			_, err := m.client.Upsert(ctx, disabledReq)
			if err != nil {
				log.Warnf("[Milvus] Failed to update disabled chunks in %s: %v", collectionName, err)
				continue
			}
		}
	}

	log.Infof("[Milvus] Batch update chunk enabled status completed")
	return nil
}

func (m *milvusRepository) searchByFilter(ctx context.Context, collectionName string, filter *universalFilterCondition, limit, offset *int) ([]*MilvusVectorEmbeddingWithScore, int, error) {
	params, err := m.filter.Convert(filter)
	if err != nil {
		return nil, 0, err
	}
	queryOpt := client.NewQueryOption(collectionName)
	if params.exprStr != "" {
		queryOpt.WithFilter(params.exprStr)
		for k, v := range params.params {
			queryOpt.WithTemplateParam(k, v)
		}
	}
	queryOpt.WithOutputFields("*")
	if limit != nil {
		queryOpt.WithLimit(*limit)
	}
	if offset != nil {
		queryOpt.WithOffset(*offset)
	}
	resultSet, err := m.client.Query(ctx, queryOpt)
	if err != nil {
		return nil, 0, err
	}
	embeddings, _, err := convertResultSet([]client.ResultSet{resultSet})
	if err != nil {
		return nil, 0, err
	}
	return embeddings, resultSet.ResultCount, nil
}

// BatchUpdateChunkTagID updates the tag ID of chunks in batch
func (m *milvusRepository) BatchUpdateChunkTagID(ctx context.Context, chunkTagMap map[string]string) error {
	log := logger.GetLogger(ctx)
	if len(chunkTagMap) == 0 {
		log.Warn("[Milvus] Empty chunk tag map provided, skipping")
		return nil
	}

	log.Infof("[Milvus] Batch updating chunk tag ID, count: %d", len(chunkTagMap))

	// Get all collections
	collections, err := m.client.ListCollections(ctx, client.NewListCollectionOption())
	if err != nil {
		log.Errorf("[Milvus] Failed to list collections: %w", err)
		return fmt.Errorf("failed to list collections: %w", err)
	}

	// Group chunks by tag ID for batch updates
	tagGroups := make(map[string][]string)
	for chunkID, tagID := range chunkTagMap {
		tagGroups[tagID] = append(tagGroups[tagID], chunkID)
	}

	// Update in all matching collections
	for _, collectionName := range collections {
		// Only process collections that start with our base name
		if len(collectionName) <= len(m.collectionBaseName) ||
			collectionName[:len(m.collectionBaseName)] != m.collectionBaseName {
			continue
		}
		// Update chunks for each tag ID
		for tagID, chunkIDs := range tagGroups {
			embeddings, _, err := m.searchByFilter(ctx, collectionName, &universalFilterCondition{
				Field:    fieldChunkID,
				Operator: operatorIn,
				Value:    chunkIDs,
			}, nil, nil)
			if err != nil {
				log.Warnf("[Milvus] Failed to search chunks in %s: %v", collectionName, err)
				continue
			}
			upsertEmbeddings := make([]*MilvusVectorEmbedding, 0, len(embeddings))
			for _, embedding := range embeddings {
				embedding.TagID = tagID
				upsertEmbeddings = append(upsertEmbeddings, &embedding.MilvusVectorEmbedding)
			}
			if len(upsertEmbeddings) > 0 {
				req := createUpsert(collectionName, upsertEmbeddings)
				_, err := m.client.Upsert(ctx, req)
				if err != nil {
					log.Warnf("[Milvus] Failed to update chunks in %s: %v", collectionName, err)
					continue
				}
			}
		}

	}

	log.Infof("[Milvus] Batch update chunk tag ID completed")
	return nil
}

func (m *milvusRepository) getBaseFilterForQuery(params types.RetrieveParams) (string, map[string]any, error) {
	filters := make([]*universalFilterCondition, 0)
	if len(params.KnowledgeBaseIDs) > 0 {
		filters = append(filters, &universalFilterCondition{
			Field:    fieldKnowledgeBaseID,
			Operator: operatorIn,
			Value:    params.KnowledgeBaseIDs,
		})
	}
	if len(params.KnowledgeIDs) > 0 {
		filters = append(filters, &universalFilterCondition{
			Field:    fieldKnowledgeID,
			Operator: operatorIn,
			Value:    params.KnowledgeIDs,
		})
	}
	if len(params.TagIDs) > 0 {
		filters = append(filters, &universalFilterCondition{
			Field:    fieldTagID,
			Operator: operatorIn,
			Value:    params.TagIDs,
		})
	}
	if len(params.ExcludeKnowledgeIDs) > 0 {
		filters = append(filters, &universalFilterCondition{
			Field:    fieldKnowledgeID,
			Operator: operatorNotIn,
			Value:    params.ExcludeKnowledgeIDs,
		})
	}
	if len(params.ExcludeChunkIDs) > 0 {
		filters = append(filters, &universalFilterCondition{
			Field:    fieldChunkID,
			Operator: operatorNotIn,
			Value:    params.ExcludeChunkIDs,
		})
	}
	if len(filters) == 0 {
		return "", nil, nil
	}
	f, err := m.filter.Convert(&universalFilterCondition{
		Operator: operatorAnd,
		Value:    filters,
	})
	if err != nil {
		return "", nil, err
	}
	return f.exprStr, f.params, nil
}

// Retrieve dispatches the retrieval operation to the appropriate method based on retriever type
func (m *milvusRepository) Retrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	log.Debugf("[Milvus] Processing retrieval request of type: %s", params.RetrieverType)

	switch params.RetrieverType {
	case types.VectorRetrieverType:
		return m.VectorRetrieve(ctx, params)
	case types.KeywordsRetrieverType:
		return m.KeywordsRetrieve(ctx, params)
	}

	err := fmt.Errorf("invalid retriever type: %v", params.RetrieverType)
	log.Errorf("[Milvus] %v", err)
	return nil, err
}

// VectorRetrieve performs vector similarity search
func (m *milvusRepository) VectorRetrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	dimension := len(params.Embedding)
	log.Infof("[Milvus] Vector retrieval: dim=%d, topK=%d, threshold=%.4f",
		dimension, params.TopK, params.Threshold)

	// Get collection name based on embedding dimension
	collectionName := m.getCollectionName(dimension)

	// Check if collection exists
	hasCollection, err := m.client.HasCollection(ctx, client.NewHasCollectionOption(collectionName))
	if err != nil {
		log.Errorf("[Milvus] Failed to check collection existence: %v", err)
		return nil, fmt.Errorf("failed to check collection: %w", err)
	}
	if !hasCollection {
		log.Warnf("[Milvus] Collection %s does not exist, returning empty results", collectionName)
		return buildRetrieveResult(nil, types.VectorRetrieverType), nil
	}

	expr, paramsMap, err := m.getBaseFilterForQuery(params)
	if err != nil {
		log.Errorf("[Milvus] Failed to build base filter: %v", err)
		return nil, fmt.Errorf("failed to build filter: %w", err)
	}
	var sp *index.CustomAnnParam
	if params.Threshold > 0 {
		ann := index.NewCustomAnnParam()
		ann.WithRadius(params.Threshold)
		sp = &ann
	}
	searchOption := client.NewSearchOption(collectionName, params.TopK, []entity.Vector{entity.FloatVector(params.Embedding)})
	searchOption.WithANNSField(fieldEmbedding)
	if sp != nil {
		searchOption.WithAnnParam(sp)
	}
	if expr != "" {
		searchOption.WithFilter(expr)
		for k, v := range paramsMap {
			searchOption.WithTemplateParam(k, v)
		}
	}
	searchOption.WithOutputFields("*")
	resultSet, err := m.client.Search(ctx, searchOption)
	if err != nil {
		log.Errorf("[Milvus] Vector search failed: %v", err)
		return nil, fmt.Errorf("failed to search: %w", err)
	}
	sets, scores, err := convertResultSet(resultSet)
	if err != nil {
		log.Errorf("[Milvus] Failed to convert result set: %v", err)
		return nil, fmt.Errorf("failed to convert result set: %w", err)
	}
	var results []*types.IndexWithScore
	for i, set := range sets {
		set.Score = scores[i]
		results = append(results, fromMilvusVectorEmbedding(set.ID, set, types.MatchTypeEmbedding))
	}
	if len(results) == 0 {
		log.Warnf("[Milvus] No vector matches found that meet threshold %.4f", params.Threshold)
	} else {
		log.Infof("[Milvus] Vector retrieval found %d results", len(results))
		log.Debugf("[Milvus] Top result score: %.4f", results[0].Score)
	}
	return buildRetrieveResult(results, types.VectorRetrieverType), nil
}

// KeywordsRetrieve performs keyword-based search in document content
func (m *milvusRepository) KeywordsRetrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	log.Infof("[Milvus] Performing keywords retrieval with query: %s, topK: %d", params.Query, params.TopK)

	// Get all collections
	collections, err := m.client.ListCollections(ctx, client.NewListCollectionOption())
	if err != nil {
		log.Errorf("[Milvus] Failed to list collections: %v", err)
		return nil, fmt.Errorf("failed to list collections: %w", err)
	}

	var allResults []*types.IndexWithScore

	// Search in all matching collections
	for _, collectionName := range collections {
		// Only process collections that start with our base name
		if len(collectionName) <= len(m.collectionBaseName) ||
			collectionName[:len(m.collectionBaseName)] != m.collectionBaseName {
			continue
		}

		expr, paramsMap, err := m.getBaseFilterForQuery(params)
		if err != nil {
			log.Errorf("[Milvus] Failed to build base filter: %v", err)
			continue
		}
		searchOpt := client.NewSearchOption(collectionName, params.TopK, []entity.Vector{entity.Text(params.Query)})
		searchOpt.WithANNSField(fieldContentSparse)
		if expr != "" {
			searchOpt.WithFilter(expr)
			for k, v := range paramsMap {
				searchOpt.WithTemplateParam(k, v)
			}
		}
		searchOpt.WithOutputFields("*")
		resultSet, err := m.client.Search(ctx, searchOpt)
		if err != nil {
			log.Errorf("[Milvus] Keywords search failed: %v", err)
			continue
		}
		sets, _, err := convertResultSet(resultSet)
		if err != nil {
			log.Errorf("[Milvus] Failed to convert result set: %v", err)
			continue
		}
		for _, set := range sets {
			set.Score = 1.0
			allResults = append(allResults, fromMilvusVectorEmbedding(set.ID, set, types.MatchTypeKeywords))
		}
	}

	// Limit results to topK
	if len(allResults) > params.TopK {
		allResults = allResults[:params.TopK]
	}

	if len(allResults) == 0 {
		log.Warnf("[Milvus] No keyword matches found for query: %s", params.Query)
	} else {
		log.Infof("[Milvus] Keywords retrieval found %d results", len(allResults))
	}

	return buildRetrieveResult(allResults, types.KeywordsRetrieverType), nil
}

// CopyIndices copies index data from source knowledge base to target knowledge base
func (m *milvusRepository) CopyIndices(ctx context.Context,
	sourceKnowledgeBaseID string,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
	dimension int,
	knowledgeType string,
) error {
	log := logger.GetLogger(ctx)
	log.Infof(
		"[Milvus] Copying indices from source knowledge base %s to target knowledge base %s, count: %d, dimension: %d",
		sourceKnowledgeBaseID, targetKnowledgeBaseID, len(sourceToTargetChunkIDMap), dimension,
	)

	if len(sourceToTargetChunkIDMap) == 0 {
		log.Warn("[Milvus] Empty mapping, skipping copy")
		return nil
	}

	collectionName := m.getCollectionName(dimension)
	// Ensure target collection exists
	if err := m.ensureCollection(ctx, dimension); err != nil {
		return err
	}

	batchSize := 64
	totalCopied := 0
	var offset *int
	for {
		sourceEmbeddings, count, err := m.searchByFilter(ctx, collectionName, &universalFilterCondition{
			Field:    fieldKnowledgeBaseID,
			Operator: operatorEqual,
			Value:    sourceKnowledgeBaseID,
		}, &batchSize, offset)
		if err != nil {
			log.Errorf("[Milvus] Failed to query source points: %v", err)
			return err
		}
		if len(sourceEmbeddings) == 0 {
			break
		}
		targetEmbeddings := make([]*MilvusVectorEmbedding, 0, len(sourceEmbeddings))
		for _, sourceEmbedding := range sourceEmbeddings {
			sourceChunkID := sourceEmbedding.ChunkID
			sourceKnowledgeID := sourceEmbedding.KnowledgeID
			originalSourceID := sourceEmbedding.SourceID

			targetChunkID, ok := sourceToTargetChunkIDMap[sourceChunkID]
			if !ok {
				log.Warnf("[Milvus] Source chunk %s not found in target mapping, skipping", sourceChunkID)
				continue
			}
			targetKnowledgeID, ok := sourceToTargetKBIDMap[sourceKnowledgeID]
			if !ok {
				log.Warnf("[Milvus] Source knowledge %s not found in target mapping, skipping", sourceKnowledgeID)
				continue
			}
			var targetSourceID string
			if originalSourceID == sourceChunkID {
				targetSourceID = targetChunkID
			} else if strings.HasPrefix(originalSourceID, sourceChunkID+"-") {
				questionID := strings.TrimPrefix(originalSourceID, sourceChunkID+"-")
				targetSourceID = fmt.Sprintf("%s-%s", targetChunkID, questionID)
			} else {
				targetSourceID = uuid.New().String()
			}
			targetEmbedding := &MilvusVectorEmbedding{
				ID:              uuid.New().String(),
				Content:         sourceEmbedding.Content,
				SourceID:        targetSourceID,
				SourceType:      sourceEmbedding.SourceType,
				ChunkID:         targetChunkID,
				KnowledgeID:     targetKnowledgeID,
				KnowledgeBaseID: targetKnowledgeBaseID,
				TagID:           sourceEmbedding.TagID,
				Embedding:       sourceEmbedding.Embedding,
				IsEnabled:       sourceEmbedding.IsEnabled,
			}
			targetEmbeddings = append(targetEmbeddings, targetEmbedding)
		}
		if len(targetEmbeddings) > 0 {
			opts := createUpsert(collectionName, targetEmbeddings)
			_, err := m.client.Upsert(ctx, opts)
			if err != nil {
				log.Errorf("[Milvus] Failed to batch upsert target points: %v", err)
				return err
			}
			totalCopied += len(targetEmbeddings)
			log.Infof("[Milvus] Successfully copied batch, batch size: %d, total copied: %d",
				len(targetEmbeddings), totalCopied)
		}

		if count < batchSize {
			break
		}
		if offset == nil {
			offset = new(int)
		}
		*offset += count
	}

	log.Infof("[Milvus] Index copy completed, total copied: %d", totalCopied)
	return nil
}

func buildRetrieveResult(results []*types.IndexWithScore, retrieverType types.RetrieverType) []*types.RetrieveResult {
	return []*types.RetrieveResult{
		{
			Results:             results,
			RetrieverEngineType: types.MilvusRetrieverEngineType,
			RetrieverType:       retrieverType,
			Error:               nil,
		},
	}
}

func (m *milvusRepository) calculateStorageSize(embedding *MilvusVectorEmbedding) int64 {
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
	var indexBytes int64 = 0
	if embedding.Embedding != nil {
		dimensions := int64(len(embedding.Embedding))
		vectorSizeBytes = dimensions * 4

		// IVF_FLAT index: dimensions × (nlist × 4 + 4) bytes
		// Default nlist=16384, so: dimensions × (65536 + 4) ≈ dimensions × 65540
		const nlist = 16384
		indexBytes = dimensions * (nlist*4 + 4)
	}

	// ID tracker and metadata: ~32 bytes per vector
	const metadataBytes int64 = 32

	totalSizeBytes := payloadSizeBytes + vectorSizeBytes + indexBytes + metadataBytes
	return totalSizeBytes
}

// toMilvusVectorEmbedding converts IndexInfo to Milvus format
func toMilvusVectorEmbedding(embedding *types.IndexInfo, additionalParams map[string]interface{}) *MilvusVectorEmbedding {
	vector := &MilvusVectorEmbedding{
		Content:         embedding.Content,
		SourceID:        embedding.SourceID,
		SourceType:      int(embedding.SourceType),
		ChunkID:         embedding.ChunkID,
		KnowledgeID:     embedding.KnowledgeID,
		KnowledgeBaseID: embedding.KnowledgeBaseID,
		TagID:           embedding.TagID,
		IsEnabled:       true, // Default to enabled
	}
	if additionalParams != nil && slices.Contains(slices.Collect(maps.Keys(additionalParams)), fieldEmbedding) {
		if embeddingMap, ok := additionalParams[fieldEmbedding].(map[string][]float32); ok {
			vector.Embedding = embeddingMap[embedding.SourceID]
		}
	}
	return vector
}

// fromMilvusVectorEmbedding converts Milvus result to IndexWithScore domain model
func fromMilvusVectorEmbedding(id string,
	embedding *MilvusVectorEmbeddingWithScore,
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

func createUpsert(collectionName string, embeddings []*MilvusVectorEmbedding) client.UpsertOption {
	ids := make([]string, 0, len(embeddings))
	embeddingsData := make([][]float32, 0, len(embeddings))
	contents := make([]string, 0, len(embeddings))
	sourceIDs := make([]string, 0, len(embeddings))
	sourceTypes := make([]int64, 0, len(embeddings))
	chunkIDs := make([]string, 0, len(embeddings))
	knowledgeIDs := make([]string, 0, len(embeddings))
	knowledgeBaseIDs := make([]string, 0, len(embeddings))
	tagIDs := make([]string, 0, len(embeddings))
	isEnableds := make([]bool, 0, len(embeddings))
	var dimension int
	for _, embedding := range embeddings {
		ids = append(ids, embedding.ID)
		embeddingsData = append(embeddingsData, embedding.Embedding)
		contents = append(contents, embedding.Content)
		sourceIDs = append(sourceIDs, embedding.SourceID)
		sourceTypes = append(sourceTypes, int64(embedding.SourceType))
		chunkIDs = append(chunkIDs, embedding.ChunkID)
		knowledgeIDs = append(knowledgeIDs, embedding.KnowledgeID)
		knowledgeBaseIDs = append(knowledgeBaseIDs, embedding.KnowledgeBaseID)
		tagIDs = append(tagIDs, embedding.TagID)
		isEnableds = append(isEnableds, embedding.IsEnabled)
		dimension = len(embedding.Embedding)
	}
	opt := client.NewColumnBasedInsertOption(collectionName).
		WithVarcharColumn(fieldID, ids).
		WithFloatVectorColumn(fieldEmbedding, dimension, embeddingsData).
		WithVarcharColumn(fieldContent, contents).
		WithVarcharColumn(fieldSourceID, sourceIDs).
		WithInt64Column(fieldSourceType, sourceTypes).
		WithVarcharColumn(fieldChunkID, chunkIDs).
		WithVarcharColumn(fieldKnowledgeID, knowledgeIDs).
		WithVarcharColumn(fieldKnowledgeBaseID, knowledgeBaseIDs).
		WithVarcharColumn(fieldTagID, tagIDs).
		WithBoolColumn(fieldIsEnabled, isEnableds)
	return opt
}

func convertResultSet(resultSet []client.ResultSet) ([]*MilvusVectorEmbeddingWithScore, []float64, error) {
	var results []*MilvusVectorEmbeddingWithScore
	var scores []float64
	if len(resultSet) == 0 {
		return results, scores, nil
	}
	set := resultSet[0]
	resultLen := set.Fields[0].Len()
	for _, score := range set.Scores {
		scores = append(scores, float64(score))
	}
	docs := make([]*MilvusVectorEmbeddingWithScore, 0, resultLen)
	for i := 0; i < resultLen; i++ {
		docs = append(docs, &MilvusVectorEmbeddingWithScore{})
	}
	for _, field := range allFields {
		columns := set.GetColumn(field)
		if columns == nil || columns.Len() == 0 {
			continue
		}
		if field == fieldID {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsString(i)
				if err != nil {
					return nil, nil, err
				}
				docs[i].ID = val
			}
		}
		if field == fieldContent {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsString(i)
				if err != nil {
					return nil, nil, err
				}
				docs[i].Content = val
			}
		}
		if field == fieldSourceID {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsString(i)
				if err != nil {
					return nil, nil, err
				}
				docs[i].SourceID = val
			}
		}
		if field == fieldSourceType {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsInt64(i)
				if err != nil {
					return nil, nil, err
				}
				docs[i].SourceType = int(val)
			}
		}
		if field == fieldChunkID {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsString(i)
				if err != nil {
					return nil, nil, err
				}
				docs[i].ChunkID = val
			}
		}
		if field == fieldKnowledgeID {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsString(i)
				if err != nil {
					return nil, nil, err
				}
				docs[i].KnowledgeID = val
			}
		}
		if field == fieldKnowledgeBaseID {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsString(i)
				if err != nil {
					return nil, nil, err
				}
				docs[i].KnowledgeBaseID = val
			}
		}
		if field == fieldTagID {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsString(i)
				if err != nil {
					return nil, nil, err
				}
				docs[i].TagID = val
			}
		}
		if field == fieldIsEnabled {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsBool(i)
				if err != nil {
					return nil, nil, err
				}
				docs[i].IsEnabled = val
			}
		}
		if field == fieldEmbedding {
			vectorColumn, ok := columns.(*column.ColumnDoubleArray)
			if !ok {
				continue
			}
			for i := 0; i < vectorColumn.Len(); i++ {
				val, err := vectorColumn.Value(i)
				if err != nil {
					return nil, nil, fmt.Errorf("get vector failed: %w", err)
				}
				embedding := make([]float32, len(val))
				for j, v := range val {
					embedding[j] = float32(v)
				}
				docs[i].Embedding = embedding
			}
		}
	}
	return docs, scores, nil
}
