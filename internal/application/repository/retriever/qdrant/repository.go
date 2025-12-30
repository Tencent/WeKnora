package qdrant

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/qdrant/go-client/qdrant"
)

const (
	envQdrantCollection   = "QDRANT_COLLECTION"
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
)

// NewQdrantRetrieveEngineRepository creates and initializes a new Qdrant repository
func NewQdrantRetrieveEngineRepository(client *qdrant.Client) interfaces.RetrieveEngineRepository {
	log := logger.GetLogger(context.Background())
	log.Info("[Qdrant] Initializing Qdrant retriever engine repository")

	collectionBaseName := os.Getenv(envQdrantCollection)
	if collectionBaseName == "" {
		log.Warn("[Qdrant] QDRANT_COLLECTION environment variable not set, using default collection name")
		collectionBaseName = defaultCollectionName
	}

	res := &qdrantRepository{
		client:             client,
		collectionBaseName: collectionBaseName,
	}

	log.Info("[Qdrant] Successfully initialized repository")
	return res
}

// getCollectionName returns the collection name for a specific dimension
func (q *qdrantRepository) getCollectionName(dimension int) string {
	return fmt.Sprintf("%s_%d", q.collectionBaseName, dimension)
}

// ensureCollection ensures the collection exists for the given dimension
func (q *qdrantRepository) ensureCollection(ctx context.Context, dimension int) error {
	collectionName := q.getCollectionName(dimension)

	// Check cache first
	if _, ok := q.initializedCollections.Load(dimension); ok {
		return nil
	}

	log := logger.GetLogger(ctx)

	// Check if collection exists
	exists, err := q.client.CollectionExists(ctx, collectionName)
	if err != nil {
		log.Errorf("[Qdrant] Failed to check collection existence: %v", err)
		return fmt.Errorf("failed to check collection existence: %w", err)
	}

	if !exists {
		log.Infof("[Qdrant] Creating collection %s with dimension %d", collectionName, dimension)

		err = q.client.CreateCollection(ctx, &qdrant.CreateCollection{
			CollectionName: collectionName,
			VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
				Size:     uint64(dimension),
				Distance: qdrant.Distance_Cosine,
			}),
		})
		if err != nil {
			log.Errorf("[Qdrant] Failed to create collection: %v", err)
			return fmt.Errorf("failed to create collection: %w", err)
		}

		// Create payload indexes for filtering
		indexFields := []string{fieldChunkID, fieldKnowledgeID, fieldKnowledgeBaseID, fieldSourceID}
		for _, field := range indexFields {
			_, err = q.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
				CollectionName: collectionName,
				FieldName:      field,
				FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
			})
			if err != nil {
				log.Warnf("[Qdrant] Failed to create index for field %s: %v", field, err)
			}
		}

		// Create bool index for is_enabled
		_, err = q.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
			CollectionName: collectionName,
			FieldName:      fieldIsEnabled,
			FieldType:      qdrant.FieldType_FieldTypeBool.Enum(),
		})
		if err != nil {
			log.Warnf("[Qdrant] Failed to create index for field %s: %v", fieldIsEnabled, err)
		}

		// Create text index for content (for keyword search) with multilingual tokenizer
		// This supports Chinese, Japanese, Korean and other languages
		lowercase := true
		_, err = q.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
			CollectionName: collectionName,
			FieldName:      fieldContent,
			FieldType:      qdrant.FieldType_FieldTypeText.Enum(),
			FieldIndexParams: &qdrant.PayloadIndexParams{
				IndexParams: &qdrant.PayloadIndexParams_TextIndexParams{
					TextIndexParams: &qdrant.TextIndexParams{
						Tokenizer: qdrant.TokenizerType_Multilingual,
						Lowercase: &lowercase,
					},
				},
			},
		})
		if err != nil {
			log.Warnf("[Qdrant] Failed to create text index for content: %v", err)
		}

		log.Infof("[Qdrant] Successfully created collection %s", collectionName)
	}

	// Mark as initialized
	q.initializedCollections.Store(dimension, true)
	return nil
}

func (q *qdrantRepository) EngineType() types.RetrieverEngineType {
	return types.QdrantRetrieverEngineType
}

func (q *qdrantRepository) Support() []types.RetrieverType {
	return []types.RetrieverType{types.KeywordsRetrieverType, types.VectorRetrieverType}
}

// EstimateStorageSize calculates the estimated storage size for a list of indices
func (q *qdrantRepository) EstimateStorageSize(ctx context.Context,
	indexInfoList []*types.IndexInfo, params map[string]any,
) int64 {
	var totalStorageSize int64
	for _, embedding := range indexInfoList {
		embeddingDB := toQdrantVectorEmbedding(embedding, params)
		totalStorageSize += q.calculateStorageSize(embeddingDB)
	}
	logger.GetLogger(ctx).Infof(
		"[Qdrant] Storage size for %d indices: %d bytes", len(indexInfoList), totalStorageSize,
	)
	return totalStorageSize
}

// Save stores a single point in Qdrant
func (q *qdrantRepository) Save(ctx context.Context,
	embedding *types.IndexInfo,
	additionalParams map[string]any,
) error {
	log := logger.GetLogger(ctx)
	log.Debugf("[Qdrant] Saving index for chunk ID: %s", embedding.ChunkID)

	embeddingDB := toQdrantVectorEmbedding(embedding, additionalParams)
	if len(embeddingDB.Embedding) == 0 {
		err := fmt.Errorf("empty embedding vector for chunk ID: %s", embedding.ChunkID)
		log.Errorf("[Qdrant] %v", err)
		return err
	}

	dimension := len(embeddingDB.Embedding)
	if err := q.ensureCollection(ctx, dimension); err != nil {
		return err
	}

	collectionName := q.getCollectionName(dimension)
	pointID := uuid.New().String()
	point := &qdrant.PointStruct{
		Id:      qdrant.NewID(pointID),
		Vectors: qdrant.NewVectors(embeddingDB.Embedding...),
		Payload: createPayload(embeddingDB),
	}

	_, err := q.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: collectionName,
		Points:         []*qdrant.PointStruct{point},
	})
	if err != nil {
		log.Errorf("[Qdrant] Failed to save index: %v", err)
		return err
	}

	log.Infof("[Qdrant] Successfully saved index for chunk ID: %s, point ID: %s", embedding.ChunkID, pointID)
	return nil
}

// BatchSave stores multiple points in Qdrant using batch upsert
func (q *qdrantRepository) BatchSave(ctx context.Context,
	embeddingList []*types.IndexInfo, additionalParams map[string]any,
) error {
	log := logger.GetLogger(ctx)
	if len(embeddingList) == 0 {
		log.Warn("[Qdrant] Empty list provided to BatchSave, skipping")
		return nil
	}

	log.Infof("[Qdrant] Batch saving %d indices", len(embeddingList))

	// Group points by dimension
	pointsByDimension := make(map[int][]*qdrant.PointStruct)

	for _, embedding := range embeddingList {
		embeddingDB := toQdrantVectorEmbedding(embedding, additionalParams)
		if len(embeddingDB.Embedding) == 0 {
			log.Warnf("[Qdrant] Skipping empty embedding for chunk ID: %s", embedding.ChunkID)
			continue
		}

		dimension := len(embeddingDB.Embedding)
		point := &qdrant.PointStruct{
			Id:      qdrant.NewID(uuid.New().String()),
			Vectors: qdrant.NewVectors(embeddingDB.Embedding...),
			Payload: createPayload(embeddingDB),
		}
		pointsByDimension[dimension] = append(pointsByDimension[dimension], point)
		log.Debugf("[Qdrant] Added chunk ID %s to batch request (dimension: %d)", embedding.ChunkID, dimension)
	}

	if len(pointsByDimension) == 0 {
		log.Warn("[Qdrant] No valid points to save after filtering")
		return nil
	}

	// Save points to each dimension-specific collection
	totalSaved := 0
	for dimension, points := range pointsByDimension {
		if err := q.ensureCollection(ctx, dimension); err != nil {
			return err
		}

		collectionName := q.getCollectionName(dimension)
		_, err := q.client.Upsert(ctx, &qdrant.UpsertPoints{
			CollectionName: collectionName,
			Points:         points,
		})
		if err != nil {
			log.Errorf("[Qdrant] Failed to execute batch operation for dimension %d: %v", dimension, err)
			return fmt.Errorf("failed to batch save (dimension %d): %w", dimension, err)
		}
		totalSaved += len(points)
		log.Infof("[Qdrant] Saved %d points to collection %s", len(points), collectionName)
	}

	log.Infof("[Qdrant] Successfully batch saved %d indices", totalSaved)
	return nil
}

// DeleteByChunkIDList removes points from the collection based on chunk IDs
func (q *qdrantRepository) DeleteByChunkIDList(ctx context.Context, chunkIDList []string, dimension int, knowledgeType string) error {
	log := logger.GetLogger(ctx)
	if len(chunkIDList) == 0 {
		log.Warn("[Qdrant] Empty chunk ID list provided for deletion, skipping")
		return nil
	}

	collectionName := q.getCollectionName(dimension)
	log.Infof("[Qdrant] Deleting indices by chunk IDs from %s, count: %d", collectionName, len(chunkIDList))

	_, err := q.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: collectionName,
		Points: qdrant.NewPointsSelectorFilter(&qdrant.Filter{
			Must: []*qdrant.Condition{
				qdrant.NewMatchKeywords(fieldChunkID, chunkIDList...),
			},
		}),
	})
	if err != nil {
		log.Errorf("[Qdrant] Failed to delete by chunk IDs: %v", err)
		return fmt.Errorf("failed to delete by chunk IDs: %w", err)
	}

	log.Infof("[Qdrant] Successfully deleted documents by chunk IDs")
	return nil
}

// DeleteByKnowledgeIDList removes points from the collection based on knowledge IDs
func (q *qdrantRepository) DeleteByKnowledgeIDList(ctx context.Context,
	knowledgeIDList []string, dimension int, knowledgeType string,
) error {
	log := logger.GetLogger(ctx)
	if len(knowledgeIDList) == 0 {
		log.Warn("[Qdrant] Empty knowledge ID list provided for deletion, skipping")
		return nil
	}

	collectionName := q.getCollectionName(dimension)
	log.Infof("[Qdrant] Deleting indices by knowledge IDs from %s, count: %d", collectionName, len(knowledgeIDList))

	_, err := q.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: collectionName,
		Points: qdrant.NewPointsSelectorFilter(&qdrant.Filter{
			Must: []*qdrant.Condition{
				qdrant.NewMatchKeywords(fieldKnowledgeID, knowledgeIDList...),
			},
		}),
	})
	if err != nil {
		log.Errorf("[Qdrant] Failed to delete by knowledge IDs: %v", err)
		return fmt.Errorf("failed to delete by knowledge IDs: %w", err)
	}

	log.Infof("[Qdrant] Successfully deleted documents by knowledge IDs")
	return nil
}

// DeleteBySourceIDList removes points from the collection based on source IDs
func (q *qdrantRepository) DeleteBySourceIDList(ctx context.Context,
	sourceIDList []string, dimension int, knowledgeType string,
) error {
	log := logger.GetLogger(ctx)
	if len(sourceIDList) == 0 {
		log.Warn("[Qdrant] Empty source ID list provided for deletion, skipping")
		return nil
	}

	collectionName := q.getCollectionName(dimension)
	log.Infof("[Qdrant] Deleting indices by source IDs from %s, count: %d", collectionName, len(sourceIDList))

	_, err := q.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: collectionName,
		Points: qdrant.NewPointsSelectorFilter(&qdrant.Filter{
			Must: []*qdrant.Condition{
				qdrant.NewMatchKeywords(fieldSourceID, sourceIDList...),
			},
		}),
	})
	if err != nil {
		log.Errorf("[Qdrant] Failed to delete by source IDs: %v", err)
		return fmt.Errorf("failed to delete by source IDs: %w", err)
	}

	log.Infof("[Qdrant] Successfully deleted documents by source IDs")
	return nil
}

// BatchUpdateChunkEnabledStatus updates the enabled status of chunks in batch
// This method operates on all collections since dimension is not provided
func (q *qdrantRepository) BatchUpdateChunkEnabledStatus(ctx context.Context, chunkStatusMap map[string]bool) error {
	log := logger.GetLogger(ctx)
	if len(chunkStatusMap) == 0 {
		log.Warn("[Qdrant] Empty chunk status map provided, skipping")
		return nil
	}

	log.Infof("[Qdrant] Batch updating chunk enabled status, count: %d", len(chunkStatusMap))

	// Get all collections that match our base name pattern
	collections, err := q.client.ListCollections(ctx)
	if err != nil {
		log.Errorf("[Qdrant] Failed to list collections: %v", err)
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
		if len(collectionName) <= len(q.collectionBaseName) ||
			collectionName[:len(q.collectionBaseName)] != q.collectionBaseName {
			continue
		}

		// Update enabled chunks
		if len(enabledChunkIDs) > 0 {
			_, err := q.client.SetPayload(ctx, &qdrant.SetPayloadPoints{
				CollectionName: collectionName,
				Payload:        qdrant.NewValueMap(map[string]any{fieldIsEnabled: true}),
				PointsSelector: qdrant.NewPointsSelectorFilter(&qdrant.Filter{
					Must: []*qdrant.Condition{
						qdrant.NewMatchKeywords(fieldChunkID, enabledChunkIDs...),
					},
				}),
			})
			if err != nil {
				log.Warnf("[Qdrant] Failed to update enabled chunks in %s: %v", collectionName, err)
			}
		}

		// Update disabled chunks
		if len(disabledChunkIDs) > 0 {
			_, err := q.client.SetPayload(ctx, &qdrant.SetPayloadPoints{
				CollectionName: collectionName,
				Payload:        qdrant.NewValueMap(map[string]any{fieldIsEnabled: false}),
				PointsSelector: qdrant.NewPointsSelectorFilter(&qdrant.Filter{
					Must: []*qdrant.Condition{
						qdrant.NewMatchKeywords(fieldChunkID, disabledChunkIDs...),
					},
				}),
			})
			if err != nil {
				log.Warnf("[Qdrant] Failed to update disabled chunks in %s: %v", collectionName, err)
			}
		}
	}

	log.Infof("[Qdrant] Batch update chunk enabled status completed")
	return nil
}

// BatchUpdateChunkTagID updates the tag ID of chunks in batch
func (q *qdrantRepository) BatchUpdateChunkTagID(ctx context.Context, chunkTagMap map[string]string) error {
	log := logger.GetLogger(ctx)
	if len(chunkTagMap) == 0 {
		log.Warn("[Qdrant] Empty chunk tag map provided, skipping")
		return nil
	}

	log.Infof("[Qdrant] Batch updating chunk tag ID, count: %d", len(chunkTagMap))

	// Get all collections that match our base name pattern
	collections, err := q.client.ListCollections(ctx)
	if err != nil {
		log.Errorf("[Qdrant] Failed to list collections: %v", err)
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
		if len(collectionName) <= len(q.collectionBaseName) ||
			collectionName[:len(q.collectionBaseName)] != q.collectionBaseName {
			continue
		}

		// Update chunks for each tag ID
		for tagID, chunkIDs := range tagGroups {
			_, err := q.client.SetPayload(ctx, &qdrant.SetPayloadPoints{
				CollectionName: collectionName,
				Payload:        qdrant.NewValueMap(map[string]any{fieldTagID: tagID}),
				PointsSelector: qdrant.NewPointsSelectorFilter(&qdrant.Filter{
					Must: []*qdrant.Condition{
						qdrant.NewMatchKeywords(fieldChunkID, chunkIDs...),
					},
				}),
			})
			if err != nil {
				log.Warnf("[Qdrant] Failed to update chunks with tag_id %s in %s: %v", tagID, collectionName, err)
			}
		}
	}

	log.Infof("[Qdrant] Batch update chunk tag ID completed")
	return nil
}

func (q *qdrantRepository) getBaseFilter(params types.RetrieveParams) *qdrant.Filter {
	must := make([]*qdrant.Condition, 0)
	mustNot := make([]*qdrant.Condition, 0)

	// Only retrieve enabled chunks
	must = append(must, qdrant.NewMatchBool(fieldIsEnabled, true))

	// KnowledgeBaseIDs and KnowledgeIDs use AND logic
	// - If only KnowledgeBaseIDs: search entire knowledge bases
	// - If only KnowledgeIDs: search specific documents
	// - If both: search specific documents within the knowledge bases (AND)
	if len(params.KnowledgeBaseIDs) > 0 {
		must = append(must, qdrant.NewMatchKeywords(fieldKnowledgeBaseID, params.KnowledgeBaseIDs...))
	}
	if len(params.KnowledgeIDs) > 0 {
		must = append(must, qdrant.NewMatchKeywords(fieldKnowledgeID, params.KnowledgeIDs...))
	}
	// Filter by tag IDs if specified
	if len(params.TagIDs) > 0 {
		must = append(must, qdrant.NewMatchKeywords(fieldTagID, params.TagIDs...))
	}

	if len(params.ExcludeKnowledgeIDs) > 0 {
		mustNot = append(mustNot, qdrant.NewMatchKeywords(fieldKnowledgeID, params.ExcludeKnowledgeIDs...))
	}

	if len(params.ExcludeChunkIDs) > 0 {
		mustNot = append(mustNot, qdrant.NewMatchKeywords(fieldChunkID, params.ExcludeChunkIDs...))
	}

	filter := &qdrant.Filter{
		Must:    must,
		MustNot: mustNot,
	}

	return filter
}

// Retrieve dispatches the retrieval operation to the appropriate method based on retriever type
func (q *qdrantRepository) Retrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	log.Debugf("[Qdrant] Processing retrieval request of type: %s", params.RetrieverType)

	switch params.RetrieverType {
	case types.VectorRetrieverType:
		return q.VectorRetrieve(ctx, params)
	case types.KeywordsRetrieverType:
		return q.KeywordsRetrieve(ctx, params)
	}

	err := fmt.Errorf("invalid retriever type: %v", params.RetrieverType)
	log.Errorf("[Qdrant] %v", err)
	return nil, err
}

// VectorRetrieve performs vector similarity search
func (q *qdrantRepository) VectorRetrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	dimension := len(params.Embedding)
	log.Infof("[Qdrant] Vector retrieval: dim=%d, topK=%d, threshold=%.4f",
		dimension, params.TopK, params.Threshold)

	// Get collection name based on embedding dimension
	collectionName := q.getCollectionName(dimension)

	// Check if collection exists
	exists, err := q.client.CollectionExists(ctx, collectionName)
	if err != nil {
		log.Errorf("[Qdrant] Failed to check collection existence: %v", err)
		return nil, fmt.Errorf("failed to check collection: %w", err)
	}
	if !exists {
		log.Warnf("[Qdrant] Collection %s does not exist, returning empty results", collectionName)
		return buildRetrieveResult(nil, types.VectorRetrieverType), nil
	}

	filter := q.getBaseFilter(params)

	limit := uint64(params.TopK)
	scoreThreshold := float32(params.Threshold)

	searchResult, err := q.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: collectionName,
		Query:          qdrant.NewQuery(params.Embedding...),
		Filter:         filter,
		Limit:          &limit,
		ScoreThreshold: &scoreThreshold,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		log.Errorf("[Qdrant] Vector search failed: %v", err)
		return nil, fmt.Errorf("%s: %w", collectionName, err)
	}

	var results []*types.IndexWithScore
	for _, point := range searchResult {
		payload := point.Payload
		embedding := &QdrantVectorEmbeddingWithScore{
			QdrantVectorEmbedding: QdrantVectorEmbedding{
				Content:         payload[fieldContent].GetStringValue(),
				SourceID:        payload[fieldSourceID].GetStringValue(),
				SourceType:      int(payload[fieldSourceType].GetIntegerValue()),
				ChunkID:         payload[fieldChunkID].GetStringValue(),
				KnowledgeID:     payload[fieldKnowledgeID].GetStringValue(),
				KnowledgeBaseID: payload[fieldKnowledgeBaseID].GetStringValue(),
				TagID:           payload[fieldTagID].GetStringValue(),
			},
			Score: float64(point.Score),
		}

		pointID := point.Id.GetUuid()
		results = append(results, fromQdrantVectorEmbedding(pointID, embedding, types.MatchTypeEmbedding))
	}

	if len(results) == 0 {
		log.Warnf("[Qdrant] No vector matches found that meet threshold %.4f", params.Threshold)
	} else {
		log.Infof("[Qdrant] Vector retrieval found %d results", len(results))
		log.Debugf("[Qdrant] Top result score: %.4f", results[0].Score)
	}

	return buildRetrieveResult(results, types.VectorRetrieverType), nil
}

// KeywordsRetrieve performs keyword-based search in document content
// This searches across all collections since keyword search doesn't depend on dimension
func (q *qdrantRepository) KeywordsRetrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	log.Infof("[Qdrant] Performing keywords retrieval with query: %s, topK: %d", params.Query, params.TopK)

	// Get all collections that match our base name pattern
	collections, err := q.client.ListCollections(ctx)
	if err != nil {
		log.Errorf("[Qdrant] Failed to list collections: %v", err)
		return nil, fmt.Errorf("failed to list collections: %w", err)
	}

	var allResults []*types.IndexWithScore
	limit := uint32(params.TopK)

	log.Debugf("[Qdrant] Found %d collections, base name: %s", len(collections), q.collectionBaseName)

	// Tokenize query for OR-based search (better for Chinese and multi-word queries)
	queryTokens := tokenizeQuery(params.Query)
	log.Debugf("[Qdrant] Tokenized query into %d tokens: %v", len(queryTokens), queryTokens)

	// Search in all matching collections
	for _, collectionName := range collections {
		log.Debugf("[Qdrant] Checking collection: %s", collectionName)
		// Only process collections that start with our base name
		if len(collectionName) <= len(q.collectionBaseName) ||
			collectionName[:len(q.collectionBaseName)] != q.collectionBaseName {
			log.Debugf("[Qdrant] Skipping collection %s (doesn't match base name %s)", collectionName, q.collectionBaseName)
			continue
		}

		filter := q.getBaseFilter(params)

		// Build should conditions for each token (OR logic)
		// This allows matching documents that contain any of the query tokens
		if len(queryTokens) > 0 {
			shouldConditions := make([]*qdrant.Condition, 0, len(queryTokens))
			for _, token := range queryTokens {
				shouldConditions = append(shouldConditions, qdrant.NewMatchText(fieldContent, token))
			}
			filter.Should = shouldConditions
		} else {
			// Fallback to original query if tokenization fails
			filter.Must = append(filter.Must, qdrant.NewMatchText(fieldContent, params.Query))
		}

		log.Debugf("[Qdrant] Searching in collection %s with %d should conditions", collectionName, len(filter.Should))

		scrollResult, err := q.client.Scroll(ctx, &qdrant.ScrollPoints{
			CollectionName: collectionName,
			Filter:         filter,
			Limit:          &limit,
			WithPayload:    qdrant.NewWithPayload(true),
		})
		if err != nil {
			log.Warnf("[Qdrant] Keywords search failed in %s: %v", collectionName, err)
			continue
		}

		log.Debugf("[Qdrant] Found %d results in collection %s", len(scrollResult), collectionName)

		for _, point := range scrollResult {
			payload := point.Payload
			embedding := &QdrantVectorEmbeddingWithScore{
				QdrantVectorEmbedding: QdrantVectorEmbedding{
					Content:         payload[fieldContent].GetStringValue(),
					SourceID:        payload[fieldSourceID].GetStringValue(),
					SourceType:      int(payload[fieldSourceType].GetIntegerValue()),
					ChunkID:         payload[fieldChunkID].GetStringValue(),
					KnowledgeID:     payload[fieldKnowledgeID].GetStringValue(),
					KnowledgeBaseID: payload[fieldKnowledgeBaseID].GetStringValue(),
					TagID:           payload[fieldTagID].GetStringValue(),
				},
				Score: 1.0,
			}

			pointID := point.Id.GetUuid()
			allResults = append(allResults, fromQdrantVectorEmbedding(pointID, embedding, types.MatchTypeKeywords))
		}
	}

	// Limit results to topK
	if len(allResults) > params.TopK {
		allResults = allResults[:params.TopK]
	}

	if len(allResults) == 0 {
		log.Warnf("[Qdrant] No keyword matches found for query: %s", params.Query)
	} else {
		log.Infof("[Qdrant] Keywords retrieval found %d results", len(allResults))
	}

	return buildRetrieveResult(allResults, types.KeywordsRetrieverType), nil
}

// CopyIndices copies index data from source knowledge base to target knowledge base
func (q *qdrantRepository) CopyIndices(ctx context.Context,
	sourceKnowledgeBaseID string,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
	dimension int,
	knowledgeType string,
) error {
	log := logger.GetLogger(ctx)
	log.Infof(
		"[Qdrant] Copying indices from source knowledge base %s to target knowledge base %s, count: %d, dimension: %d",
		sourceKnowledgeBaseID, targetKnowledgeBaseID, len(sourceToTargetChunkIDMap), dimension,
	)

	if len(sourceToTargetChunkIDMap) == 0 {
		log.Warn("[Qdrant] Empty mapping, skipping copy")
		return nil
	}

	collectionName := q.getCollectionName(dimension)

	// Ensure target collection exists
	if err := q.ensureCollection(ctx, dimension); err != nil {
		return err
	}

	batchSize := uint32(64)
	var offset *qdrant.PointId = nil
	totalCopied := 0

	for {
		scrollResult, err := q.client.Scroll(ctx, &qdrant.ScrollPoints{
			CollectionName: collectionName,
			Filter: &qdrant.Filter{
				Must: []*qdrant.Condition{
					qdrant.NewMatch(fieldKnowledgeBaseID, sourceKnowledgeBaseID),
				},
			},
			Limit:       &batchSize,
			Offset:      offset,
			WithPayload: qdrant.NewWithPayload(true),
			WithVectors: qdrant.NewWithVectors(true),
		})
		if err != nil {
			log.Errorf("[Qdrant] Failed to query source points: %v", err)
			return err
		}

		pointsCount := len(scrollResult)
		if pointsCount == 0 {
			break
		}

		log.Infof("[Qdrant] Found %d source points in batch", pointsCount)

		targetPoints := make([]*qdrant.PointStruct, 0, pointsCount)
		for _, sourcePoint := range scrollResult {
			payload := sourcePoint.Payload

			sourceChunkID := payload[fieldChunkID].GetStringValue()
			sourceKnowledgeID := payload[fieldKnowledgeID].GetStringValue()
			originalSourceID := payload[fieldSourceID].GetStringValue()

			targetChunkID, ok := sourceToTargetChunkIDMap[sourceChunkID]
			if !ok {
				log.Warnf("[Qdrant] Source chunk %s not found in target mapping, skipping", sourceChunkID)
				continue
			}

			targetKnowledgeID, ok := sourceToTargetKBIDMap[sourceKnowledgeID]
			if !ok {
				log.Warnf("[Qdrant] Source knowledge %s not found in target mapping, skipping", sourceKnowledgeID)
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

			newPayload := qdrant.NewValueMap(map[string]any{
				fieldContent:         payload[fieldContent].GetStringValue(),
				fieldSourceID:        targetSourceID,
				fieldSourceType:      payload[fieldSourceType].GetIntegerValue(),
				fieldChunkID:         targetChunkID,
				fieldKnowledgeID:     targetKnowledgeID,
				fieldKnowledgeBaseID: targetKnowledgeBaseID,
				fieldIsEnabled:       true,
			})

			var vectors *qdrant.Vectors
			if vectorOutput := sourcePoint.Vectors.GetVector(); vectorOutput != nil {
				if denseVector := vectorOutput.GetDenseVector(); denseVector != nil {
					vectors = qdrant.NewVectors(denseVector.Data...)
				}
			}

			if vectors == nil {
				log.Warnf("[Qdrant] No vectors found for source point with chunk %s, skipping", sourceChunkID)
				continue
			}

			newPoint := &qdrant.PointStruct{
				Id:      qdrant.NewID(uuid.New().String()),
				Vectors: vectors,
				Payload: newPayload,
			}

			targetPoints = append(targetPoints, newPoint)
		}

		if len(targetPoints) > 0 {
			_, err := q.client.Upsert(ctx, &qdrant.UpsertPoints{
				CollectionName: collectionName,
				Points:         targetPoints,
			})
			if err != nil {
				log.Errorf("[Qdrant] Failed to batch upsert target points: %v", err)
				return err
			}

			totalCopied += len(targetPoints)
			log.Infof("[Qdrant] Successfully copied batch, batch size: %d, total copied: %d",
				len(targetPoints), totalCopied)
		}

		if pointsCount > 0 {
			offset = scrollResult[pointsCount-1].Id
		}

		if pointsCount < int(batchSize) {
			break
		}
	}

	log.Infof("[Qdrant] Index copy completed, total copied: %d", totalCopied)
	return nil
}

func createPayload(embedding *QdrantVectorEmbedding) map[string]*qdrant.Value {
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
	return qdrant.NewValueMap(payload)
}

func buildRetrieveResult(results []*types.IndexWithScore, retrieverType types.RetrieverType) []*types.RetrieveResult {
	return []*types.RetrieveResult{
		{
			Results:             results,
			RetrieverEngineType: types.QdrantRetrieverEngineType,
			RetrieverType:       retrieverType,
			Error:               nil,
		},
	}
}

// Ref: https://github.com/qdrant/qdrant-sizing-calculator
func (q *qdrantRepository) calculateStorageSize(embedding *QdrantVectorEmbedding) int64 {
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

		// HNSW index: dimensions × (M × 2) × 4 bytes
		// Default M=16, so: dimensions × 32 × 4 = dimensions × 128
		const hnswM = 16
		hnswIndexBytes = dimensions * (hnswM * 2) * 4
	}

	// ID tracker metadata: 24 bytes per vector
	// (forward refs + backward refs + version tracking = 8 + 8 + 8)
	const idTrackerBytes int64 = 24

	totalSizeBytes := payloadSizeBytes + vectorSizeBytes + hnswIndexBytes + idTrackerBytes
	return totalSizeBytes
}

// toQdrantVectorEmbedding converts IndexInfo to Qdrant payload format
func toQdrantVectorEmbedding(embedding *types.IndexInfo, additionalParams map[string]interface{}) *QdrantVectorEmbedding {
	vector := &QdrantVectorEmbedding{
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

// fromQdrantVectorEmbedding converts Qdrant point to IndexWithScore domain model
func fromQdrantVectorEmbedding(id string,
	embedding *QdrantVectorEmbeddingWithScore,
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
