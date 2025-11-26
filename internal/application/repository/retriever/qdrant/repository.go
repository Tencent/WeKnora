package qdrant

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	typesLocal "github.com/Tencent/WeKnora/internal/types"
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
	fieldEmbedding        = "embedding"
)

// NewQdrantRetrieveEngineRepository creates and initializes a new Qdrant repository
func NewQdrantRetrieveEngineRepository(client *qdrant.Client) interfaces.RetrieveEngineRepository {
	log := logger.GetLogger(context.Background())
	log.Info("[Qdrant] Initializing Qdrant retriever engine repository")

	collectionName := os.Getenv(envQdrantCollection)
	if collectionName == "" {
		log.Warn("[Qdrant] QDRANT_COLLECTION environment variable not set, using default collection name")
		collectionName = defaultCollectionName
	}

	res := &qdrantRepository{
		client:         client,
		collectionName: collectionName,
	}

	log.Info("[Qdrant] Successfully initialized repository")
	return res
}

func (q *qdrantRepository) EngineType() typesLocal.RetrieverEngineType {
	return typesLocal.QdrantRetrieverEngineType
}

func (q *qdrantRepository) Support() []typesLocal.RetrieverType {
	return []typesLocal.RetrieverType{typesLocal.KeywordsRetrieverType, typesLocal.VectorRetrieverType}
}

// EstimateStorageSize calculates the estimated storage size for a list of indices
func (q *qdrantRepository) EstimateStorageSize(ctx context.Context,
	indexInfoList []*typesLocal.IndexInfo, params map[string]any,
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
	embedding *typesLocal.IndexInfo,
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

	pointID := uuid.New().String()
	point := &qdrant.PointStruct{
		Id:      qdrant.NewID(pointID),
		Vectors: qdrant.NewVectors(embeddingDB.Embedding...),
		Payload: createPayload(embeddingDB),
	}

	_, err := q.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: q.collectionName,
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
	embeddingList []*typesLocal.IndexInfo, additionalParams map[string]any,
) error {
	log := logger.GetLogger(ctx)
	if len(embeddingList) == 0 {
		log.Warn("[Qdrant] Empty list provided to BatchSave, skipping")
		return nil
	}

	log.Infof("[Qdrant] Batch saving %d indices", len(embeddingList))

	points := make([]*qdrant.PointStruct, 0, len(embeddingList))
	for _, embedding := range embeddingList {
		embeddingDB := toQdrantVectorEmbedding(embedding, additionalParams)
		if len(embeddingDB.Embedding) == 0 {
			log.Warnf("[Qdrant] Skipping empty embedding for chunk ID: %s", embedding.ChunkID)
			continue
		}

		point := &qdrant.PointStruct{
			Id:      qdrant.NewID(uuid.New().String()),
			Vectors: qdrant.NewVectors(embeddingDB.Embedding...),
			Payload: createPayload(embeddingDB),
		}
		points = append(points, point)
		log.Debugf("[Qdrant] Added chunk ID %s to batch request", embedding.ChunkID)
	}

	if len(points) == 0 {
		log.Warn("[Qdrant] No valid points to save after filtering")
		return nil
	}

	_, err := q.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: q.collectionName,
		Points:         points,
	})
	if err != nil {
		log.Errorf("[Qdrant] Failed to execute batch operation: %v", err)
		return fmt.Errorf("failed to batch save: %w", err)
	}

	log.Infof("[Qdrant] Successfully batch saved %d indices", len(points))
	return nil
}

// DeleteByChunkIDList removes points from the collection based on chunk IDs
func (q *qdrantRepository) DeleteByChunkIDList(ctx context.Context, chunkIDList []string, dimension int) error {
	log := logger.GetLogger(ctx)
	if len(chunkIDList) == 0 {
		log.Warn("[Qdrant] Empty chunk ID list provided for deletion, skipping")
		return nil
	}

	log.Infof("[Qdrant] Deleting indices by chunk IDs, count: %d", len(chunkIDList))

	_, err := q.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: q.collectionName,
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
	knowledgeIDList []string, dimension int,
) error {
	log := logger.GetLogger(ctx)
	if len(knowledgeIDList) == 0 {
		log.Warn("[Qdrant] Empty knowledge ID list provided for deletion, skipping")
		return nil
	}

	log.Infof("[Qdrant] Deleting indices by knowledge IDs, count: %d", len(knowledgeIDList))

	_, err := q.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: q.collectionName,
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

func (q *qdrantRepository) getBaseFilter(params typesLocal.RetrieveParams) *qdrant.Filter {
	must := make([]*qdrant.Condition, 0)
	mustNot := make([]*qdrant.Condition, 0)

	if len(params.KnowledgeBaseIDs) > 0 {
		must = append(must, qdrant.NewMatchKeywords(fieldKnowledgeBaseID, params.KnowledgeBaseIDs...))
	}

	if len(params.ExcludeKnowledgeIDs) > 0 {
		mustNot = append(mustNot, qdrant.NewMatchKeywords(fieldKnowledgeID, params.ExcludeKnowledgeIDs...))
	}

	if len(params.ExcludeChunkIDs) > 0 {
		mustNot = append(mustNot, qdrant.NewMatchKeywords(fieldChunkID, params.ExcludeChunkIDs...))
	}

	return &qdrant.Filter{
		Must:    must,
		MustNot: mustNot,
	}
}

// Retrieve dispatches the retrieval operation to the appropriate method based on retriever type
func (q *qdrantRepository) Retrieve(ctx context.Context,
	params typesLocal.RetrieveParams,
) ([]*typesLocal.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	log.Debugf("[Qdrant] Processing retrieval request of type: %s", params.RetrieverType)

	switch params.RetrieverType {
	case typesLocal.VectorRetrieverType:
		return q.VectorRetrieve(ctx, params)
	case typesLocal.KeywordsRetrieverType:
		return q.KeywordsRetrieve(ctx, params)
	}

	err := fmt.Errorf("invalid retriever type: %v", params.RetrieverType)
	log.Errorf("[Qdrant] %v", err)
	return nil, err
}

// VectorRetrieve performs vector similarity search
func (q *qdrantRepository) VectorRetrieve(ctx context.Context,
	params typesLocal.RetrieveParams,
) ([]*typesLocal.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	log.Infof("[Qdrant] Vector retrieval: dim=%d, topK=%d, threshold=%.4f",
		len(params.Embedding), params.TopK, params.Threshold)

	filter := q.getBaseFilter(params)

	limit := uint64(params.TopK)
	scoreThreshold := float32(params.Threshold)

	searchResult, err := q.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: q.collectionName,
		Query:          qdrant.NewQuery(params.Embedding...),
		Filter:         filter,
		Limit:          &limit,
		ScoreThreshold: &scoreThreshold,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		log.Errorf("[Qdrant] Vector search failed: %v", err)
		return nil, err
	}

	var results []*typesLocal.IndexWithScore
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
			},
			Score: float64(point.Score),
		}

		pointID := point.Id.GetUuid()
		results = append(results, fromQdrantVectorEmbedding(pointID, embedding, typesLocal.MatchTypeEmbedding))
	}

	if len(results) == 0 {
		log.Warnf("[Qdrant] No vector matches found that meet threshold %.4f", params.Threshold)
	} else {
		log.Infof("[Qdrant] Vector retrieval found %d results", len(results))
		log.Debugf("[Qdrant] Top result score: %.4f", results[0].Score)
	}

	return buildRetrieveResult(results, typesLocal.VectorRetrieverType), nil
}

// KeywordsRetrieve performs keyword-based search in document content
func (q *qdrantRepository) KeywordsRetrieve(ctx context.Context,
	params typesLocal.RetrieveParams,
) ([]*typesLocal.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	log.Infof("[Qdrant] Performing keywords retrieval with query: %s, topK: %d", params.Query, params.TopK)

	filter := q.getBaseFilter(params)

	filter.Must = append(filter.Must, qdrant.NewMatchText(fieldContent, params.Query))

	limit := uint32(params.TopK)
	scrollResult, err := q.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: q.collectionName,
		Filter:         filter,
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		log.Errorf("[Qdrant] Keywords search failed: %v", err)
		return nil, err
	}

	var results []*typesLocal.IndexWithScore
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
			},
			Score: 1.0,
		}

		pointID := point.Id.GetUuid()
		results = append(results, fromQdrantVectorEmbedding(pointID, embedding, typesLocal.MatchTypeKeywords))
	}

	if len(results) == 0 {
		log.Warnf("[Qdrant] No keyword matches found for query: %s", params.Query)
	} else {
		log.Infof("[Qdrant] Keywords retrieval found %d results", len(results))
	}

	return buildRetrieveResult(results, typesLocal.KeywordsRetrieverType), nil
}

// CopyIndices copies index data from source knowledge base to target knowledge base
func (q *qdrantRepository) CopyIndices(ctx context.Context,
	sourceKnowledgeBaseID string,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
	dimension int,
) error {
	log := logger.GetLogger(ctx)
	log.Infof(
		"[Qdrant] Copying indices from source knowledge base %s to target knowledge base %s, count: %d",
		sourceKnowledgeBaseID, targetKnowledgeBaseID, len(sourceToTargetChunkIDMap),
	)

	if len(sourceToTargetChunkIDMap) == 0 {
		log.Warn("[Qdrant] Empty mapping, skipping copy")
		return nil
	}

	batchSize := uint32(64)
	var offset *qdrant.PointId = nil
	totalCopied := 0

	for {
		scrollResult, err := q.client.Scroll(ctx, &qdrant.ScrollPoints{
			CollectionName: q.collectionName,
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

			newPayload := qdrant.NewValueMap(map[string]any{
				fieldContent:         payload[fieldContent].GetStringValue(),
				fieldSourceID:        targetChunkID,
				fieldSourceType:      payload[fieldSourceType].GetIntegerValue(),
				fieldChunkID:         targetChunkID,
				fieldKnowledgeID:     targetKnowledgeID,
				fieldKnowledgeBaseID: targetKnowledgeBaseID,
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
				CollectionName: q.collectionName,
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
	}
	return qdrant.NewValueMap(payload)
}

func buildRetrieveResult(results []*typesLocal.IndexWithScore, retrieverType typesLocal.RetrieverType) []*typesLocal.RetrieveResult {
	return []*typesLocal.RetrieveResult{
		{
			Results:             results,
			RetrieverEngineType: typesLocal.QdrantRetrieverEngineType,
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
		Content:         embedding.Content,
		Score:           embedding.Score,
		MatchType:       matchType,
	}
}
