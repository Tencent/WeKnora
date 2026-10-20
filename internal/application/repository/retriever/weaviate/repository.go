package weaviate

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/weaviate/weaviate-go-client/v6"
	"github.com/weaviate/weaviate-go-client/v6/collections"
	"github.com/weaviate/weaviate-go-client/v6/collections/vectorindex"
	"github.com/weaviate/weaviate-go-client/v6/data"
	"github.com/weaviate/weaviate-go-client/v6/modules/selfprovided"
	"github.com/weaviate/weaviate-go-client/v6/query"
	"github.com/weaviate/weaviate-go-client/v6/query/filter"
	wtypes "github.com/weaviate/weaviate-go-client/v6/types"
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

	// defaultCopyQueryLimit is the row limit CopyIndices reads source vectors
	// with. Weaviate caps a single query at QUERY_MAXIMUM_RESULTS, 10000 by
	// default (weaviate/usecases/config.DefaultQueryMaximumResults), and
	// refuses anything above it with "query maximum results exceeded". Without
	// an explicit limit a query would instead fall back to the server's default
	// limit and truncate silently. The setting is not exposed to clients -
	// /v1/meta reports only version, hostname and modules - so ask for the
	// default and, if the server refuses, take the value it names in the
	// refusal.
	defaultCopyQueryLimit = 10000

	// moveQueryLimit is how many object IDs one MoveKnowledgeIndices query
	// reads before updating them.
	moveQueryLimit = 100
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
	exists, err := w.client.Collections.Exists(ctx, collectionName)
	if err != nil {
		log.Errorf("[Weaviate] Failed to check collection existence: %v", err)
		return fmt.Errorf("failed to check collection existence: %w", err)
	}
	enabled := true
	if !exists {
		log.Infof("[Weaviate] Creating collection %s with dimension %d", collectionName, dimension)

		//定义class结构
		collection := collections.Collection{
			Name:        collectionName,
			Description: fmt.Sprintf("WeKnora embeddings collection with dimension %d", dimension),
			Vectors: map[string]collections.VectorConfig{
				fieldEmbedding: {
					Index: vectorindex.HNSW{
						Distance:       vectorindex.DistanceCosine,
						EfConstruction: 128,
						MaxConnections: 32,
						Ef:             64,
					},
					Vectorizer: selfprovided.Vectorizer,
				},
			},
			Properties: []collections.Property{
				{
					Name:         fieldContent,
					DataType:     collections.DataTypeText,
					Tokenization: collections.TokenizationGSE,
				},
				{
					Name:     fieldSourceID,
					DataType: collections.DataTypeText,
				},
				{
					Name:     fieldSourceType,
					DataType: collections.DataTypeInt,
				},
				{
					Name:            fieldChunkID,
					DataType:        collections.DataTypeText,
					IndexFilterable: &enabled,
				},
				{
					Name:            fieldKnowledgeID,
					DataType:        collections.DataTypeText,
					IndexFilterable: &enabled,
				},
				{
					Name:            fieldKnowledgeBaseID,
					DataType:        collections.DataTypeText,
					IndexFilterable: &enabled,
				},
				{
					Name:            fieldTagID,
					DataType:        collections.DataTypeText,
					IndexFilterable: &enabled,
				},
				{
					Name:            fieldIsEnabled,
					DataType:        collections.DataTypeBool,
					IndexFilterable: &enabled,
				},
			},
		}
		// Set replication factor if explicitly configured (> 0)
		if w.replicationFactor > 0 {
			collection.Replication = &collections.ReplicationConfig{
				Factor: w.replicationFactor,
			}
		}
		// Set shard count if explicitly configured (> 0)
		if w.desiredShardCount > 0 {
			collection.Sharding = &collections.ShardingConfig{
				DesiredCount: w.desiredShardCount,
			}
		}
		//创建collection
		if _, err = w.client.Collections.Create(ctx, collection); err != nil {
			// The first batches written at a new dimension are saved by several
			// workers at once, so another one (or another replica) may have
			// created the class since our check. Weaviate refuses the losing
			// create with 422, worded differently depending on where it lost,
			// so look again rather than parse the message. The class is
			// created whole, so if it exists there is nothing left to do.
			if isUnprocessableEntity(err) {
				exists, _ = w.client.Collections.Exists(ctx, collectionName)
			}
			if !exists {
				log.Errorf("[Weaviate] Failed to create collection: %v", err)
				return fmt.Errorf("failed to create collection: %w", err)
			}
			log.Infof("[Weaviate] Collection %s was created concurrently", collectionName)
		} else {
			log.Infof("[Weaviate] Successfully created collection %s", collectionName)
		}
	}
	w.initializedCollections.Store(dimension, true)
	return nil
}

// isUnprocessableEntity reports whether err is Weaviate answering 422, which is
// how it refuses a schema change it cannot apply.
func isUnprocessableEntity(err error) bool {
	var httpErr *weaviate.HTTPError
	return errors.As(err, &httpErr) && httpErr.Code == http.StatusUnprocessableEntity
}

// embeddingVector names the vector WeKnora stores each object under. Every
// collection it creates has exactly this one named vector.
func embeddingVector(vector []float32) wtypes.Vector {
	return wtypes.Vector{Name: fieldEmbedding, Single: vector}
}

// chunkUUID parses the chunk ID an object is stored under. Weaviate object IDs
// are UUIDs, so a chunk ID that is not one cannot name an object.
func chunkUUID(chunkID string) (uuid.UUID, error) {
	id, err := uuid.Parse(chunkID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("weaviate: chunk ID %q is not a UUID: %w", chunkID, err)
	}
	return id, nil
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
	obj, err := createObject(embeddingDB)
	if err != nil {
		log.Errorf("[Weaviate] %v", err)
		return err
	}

	// Create point in Weaviate
	if _, err := w.client.Collections.Use(collectionName).Data.Insert(ctx, obj); err != nil {
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
		if err := w.ensureCollection(ctx, dimension); err != nil {
			return err
		}
		collectionName := w.getCollectionName(dimension)
		objects := make([]*data.Object, 0, len(embeddings))
		for _, embedding := range embeddings {
			obj, err := createObject(toWeaviateVectorEmbedding(embedding, additionalParams))
			if err != nil {
				log.Errorf("[Weaviate] %v", err)
				return err
			}
			objects = append(objects, obj)
		}
		// Flush batch. Objects the server refused come back as an InsertError
		// next to a successful call, so the error is checked as a whole.
		if _, err := w.client.Collections.Use(collectionName).Data.Insert(ctx, objects...); err != nil {
			log.Errorf("[Weaviate] Failed to execute batch operation for dimension %d: %v", dimension, err)
			return fmt.Errorf("failed to batch save (dimension %d): %w", dimension, describeInsertError(err))
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

	// Execute deletion
	if err := w.deleteWhereContainsAny(ctx, collectionName, fieldChunkID, chunkIDList); err != nil {
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

	// Execute deletion
	if err := w.deleteWhereContainsAny(ctx, collectionName, fieldKnowledgeID, knowledgeIDList); err != nil {
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

	// Execute deletion
	if err := w.deleteWhereContainsAny(ctx, collectionName, fieldSourceID, sourceIDList); err != nil {
		log.Errorf("[Weaviate] Failed to delete by source IDs: %v", err)
		return fmt.Errorf("failed to delete by source IDs: %w", err)
	}
	log.Infof("[Weaviate] Successfully deleted documents by source IDs")
	return nil
}

// BatchUpdateChunkEnabledStatus updates every retrieval source belonging to the chunks.
func (w *weaviateRepository) BatchUpdateChunkEnabledStatus(ctx context.Context, chunkStatusMap map[string]bool) error {
	updates := make(map[string]map[string]interface{}, len(chunkStatusMap))
	for chunkID, enabled := range chunkStatusMap {
		updates[chunkID] = map[string]interface{}{fieldIsEnabled: enabled}
	}
	return w.updateChunkProperties(ctx, updates)
}

// BatchUpdateChunkTagID updates every retrieval source belonging to the chunks.
func (w *weaviateRepository) BatchUpdateChunkTagID(ctx context.Context, chunkTagMap map[string]string) error {
	updates := make(map[string]map[string]interface{}, len(chunkTagMap))
	for chunkID, tagID := range chunkTagMap {
		updates[chunkID] = map[string]interface{}{fieldTagID: tagID}
	}
	return w.updateChunkProperties(ctx, updates)
}

// updateChunkProperties merges each chunk's properties into every object
// stored for it, in every WeKnora collection, leaving the objects' other
// properties and their vectors as they are.
func (w *weaviateRepository) updateChunkProperties(
	ctx context.Context, updates map[string]map[string]any,
) error {
	if len(updates) == 0 {
		return nil
	}
	collections, err := w.ListCollections(ctx)
	if err != nil {
		return err
	}
	for _, collection := range collections {
		if !strings.HasPrefix(collection, w.collectionBaseName+"_") {
			continue
		}
		for chunkID, properties := range updates {
			ids, err := w.chunkObjectIDs(ctx, collection, chunkID)
			if err != nil {
				return err
			}
			for _, id := range ids {
				if err := w.client.Collections.Use(collection).Data.Update(ctx, data.Object{
					UUID:       &id,
					Properties: properties,
				}); err != nil {
					return fmt.Errorf("update chunk %s object %s: %w", chunkID, id, err)
				}
			}
		}
	}
	return nil
}

// Object IDs identify individual retrieval sources, whereas chunk_id groups a
// body and its generated questions. Resolve that group before changing metadata.
func (w *weaviateRepository) chunkObjectIDs(ctx context.Context, collection, chunkID string) ([]uuid.UUID, error) {
	limit := w.copyQueryLimit
	if limit <= 0 {
		limit = defaultCopyQueryLimit
	}
	for {
		result, err := w.client.Collections.Use(collection).Query.OverAll(ctx, query.OverAll{
			Filter:           filter.Cond{Target: fieldChunkID, Operator: filter.Equal, Value: chunkID},
			Limit:            limit,
			ReturnProperties: []string{fieldChunkID},
		})
		if err != nil {
			// QUERY_MAXIMUM_RESULTS has no client-facing API; the refusal is
			// the only place it can be read.
			if serverLimit, ok := queryMaximumResults(err.Error()); ok && serverLimit < limit {
				limit = serverLimit
				continue
			}
			return nil, fmt.Errorf("list chunk %s objects: %w", chunkID, err)
		}
		if result == nil {
			return nil, fmt.Errorf("invalid chunk object response for collection %s", collection)
		}
		// A full page may have been cut off, and updating only part of a
		// chunk's sources would leave them disagreeing with each other.
		if len(result.Objects) >= limit {
			return nil, fmt.Errorf("chunk %s reaches query limit %d; metadata update stopped", chunkID, limit)
		}
		ids := make([]uuid.UUID, 0, len(result.Objects))
		for _, object := range result.Objects {
			if object.UUID == uuid.Nil {
				return nil, fmt.Errorf("chunk %s object has no id", chunkID)
			}
			ids = append(ids, object.UUID)
		}
		return ids, nil
	}
}

func (w *weaviateRepository) getBaseFilter(params types.RetrieveParams) filter.Expr {
	operands := filter.And{
		filter.Cond{Target: fieldIsEnabled, Operator: filter.Equal, Value: true},
	}

	if len(params.KnowledgeBaseIDs) > 0 {
		operands = append(operands, filter.Cond{
			Target: fieldKnowledgeBaseID, Operator: filter.ContainsAny, Value: params.KnowledgeBaseIDs,
		})
	}
	if len(params.KnowledgeIDs) > 0 {
		operands = append(operands, filter.Cond{
			Target: fieldKnowledgeID, Operator: filter.ContainsAny, Value: params.KnowledgeIDs,
		})
	}

	if len(params.TagIDs) > 0 {
		operands = append(operands, filter.Cond{
			Target: fieldTagID, Operator: filter.ContainsAny, Value: params.TagIDs,
		})
	}
	if len(params.ExcludeKnowledgeIDs) > 0 {
		operands = append(operands, filter.Cond{
			Target: fieldKnowledgeID, Operator: filter.ContainsNone, Value: params.ExcludeKnowledgeIDs,
		})
	}
	if len(params.ExcludeChunkIDs) > 0 {
		operands = append(operands, filter.Cond{
			Target: fieldChunkID, Operator: filter.ContainsNone, Value: params.ExcludeChunkIDs,
		})
	}

	return operands
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
	hasCollection, err := w.client.Collections.Exists(ctx, collectionName)
	if err != nil {
		log.Errorf("[Weaviate] Failed to check collection existence: %v", err)
		return nil, fmt.Errorf("failed to check collection: %w", err)
	}
	if !hasCollection {
		log.Warnf("[Weaviate] Collection %s does not exist, returning empty results", collectionName)
		return buildRetrieveResult(nil, types.VectorRetrieverType), nil
	}

	nearVector := query.NearVector{
		Limit:            params.TopK,
		Filter:           w.getBaseFilter(params),
		Target:           embeddingVector(params.Embedding),
		ReturnProperties: retrieveProperties(),
		ReturnMetadata:   query.ReturnMetadata{Certainty: true},
	}
	// Weaviate's certainty is (1 + cos) / 2; callers pass a cosine
	// similarity threshold, the scale every other engine uses. A zero
	// threshold means no filtering, so no cutoff is sent rather than 0.5.
	if params.Threshold > 0 {
		nearVector.Similarity = query.Certainty((1 + params.Threshold) / 2)
	}
	result, err := w.client.Collections.Use(collectionName).Query.NearVector(ctx, nearVector)
	if err != nil {
		log.Errorf("[Weaviate] Vector search failed: %v", err)
		return nil, fmt.Errorf("failed to search: %w", err)
	}
	results := parseSearchResult(result, types.MatchTypeEmbedding)

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

		//bm25 search
		result, err := w.client.Collections.Use(collectionName).Query.BM25(ctx, query.BM25{
			Query:            params.Query,
			QueryProperties:  []string{fieldContent},
			Limit:            params.TopK,
			Filter:           w.getBaseFilter(params),
			ReturnProperties: retrieveProperties(),
			ReturnMetadata:   query.ReturnMetadata{Score: true},
		})
		if err != nil {
			log.Errorf("[Weaviate] keywords search failed: %v", err)
			return nil, fmt.Errorf("failed to search: %w", err)
		}
		results := parseSearchResult(result, types.MatchTypeKeywords)
		if len(results) == 0 {
			log.Warnf("[Weaviate] No keywords matches found that meet threshold %.4f", params.Threshold)
			continue
		}
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
	reader := newCopySourceReader(w, collectionName)
	totalCopied := 0

	// Read the rows the mapping names, in batches of source chunk IDs. The
	// mapping is the set of chunks this copy has to move, so a predicate over
	// it makes every read self-contained.
	//
	// The cursor walk this replaces could not be made correct: Weaviate refuses
	// `after` together with `where` and `limit` ("where cannot be set with
	// after and limit parameters"), and anywhere a cursor is accepted it is a
	// position in a result order the data node never promised to keep between
	// requests. Every request still succeeds, which is how such a walk reports
	// an incomplete target as a finished copy. A mapping predicate has no
	// cursor that can drift.
	sourceChunkIDs := slices.Sorted(maps.Keys(sourceToTargetChunkIDMap))
	const chunkBatchSize = 64
	for chunkBatch := range slices.Chunk(sourceChunkIDs, chunkBatchSize) {
		objects, err := reader.readBatch(ctx, sourceKnowledgeBaseID, chunkBatch)
		if err != nil {
			return err
		}
		if len(objects) == 0 {
			continue
		}
		log.Infof("[Weaviate] Found %d source points in batch", len(objects))

		targetObjects := make([]*data.Object, 0, len(objects))
		for _, obj := range objects {
			// The id is no longer a cursor, but a row whose id cannot be read
			// is still a response this copy does not understand; it must not be
			// counted towards a copy reported as complete.
			if obj.UUID == uuid.Nil {
				return fmt.Errorf("weaviate: copy indices source object has no id")
			}
			props := obj.Properties

			sourceChunkID, ok := props[fieldChunkID].(string)
			if !ok {
				continue
			}
			sourceKnowledgeID, ok := props[fieldKnowledgeID].(string)
			if !ok {
				continue
			}
			originalSourceID, ok := props[fieldSourceID].(string)
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

			vector := obj.Vectors[fieldEmbedding].Single
			if len(vector) == 0 {
				continue
			}

			id := uuid.New()
			newObj := &data.Object{
				UUID: &id,
				Properties: map[string]any{
					fieldContent:         props[fieldContent],
					fieldSourceID:        targetSourceID,
					fieldSourceType:      props[fieldSourceType],
					fieldChunkID:         targetChunkID,
					fieldKnowledgeID:     targetKnowledgeID,
					fieldKnowledgeBaseID: targetKnowledgeBaseID,
					fieldTagID:           props[fieldTagID],
					fieldIsEnabled:       true,
				},
				Vectors: []wtypes.Vector{embeddingVector(vector)},
			}
			targetObjects = append(targetObjects, newObj)
		}

		if len(targetObjects) > 0 {
			// Objects the server refused come back as an InsertError; a copy
			// missing them must not be reported as complete.
			if _, err := w.client.Collections.Use(collectionName).Data.Insert(ctx, targetObjects...); err != nil {
				log.Errorf("[Weaviate] Batch upsert failed: %v", describeInsertError(err))
				return fmt.Errorf("batch upsert failed: %w", describeInsertError(err))
			}
			totalCopied += len(targetObjects)
			log.Infof("[Weaviate] Successfully copied batch, total: %d", totalCopied)
		}
	}
	log.Infof("[Weaviate] Index copy completed, total copied: %d", totalCopied)
	return nil
}

// copySourceReader reads the source vectors a copy needs, one batch of source
// chunk IDs at a time.
//
// A chunk is not one vector: it is stored as a row for its own content plus one
// row per generated question, all sharing the same chunk_id. A batch of n chunk
// IDs therefore says nothing about how many rows come back, and the only reads
// that can be trusted as complete are the ones that returned fewer rows than
// the limit they asked for. A batch that comes back full is split and re-read;
// a single chunk that still fills the limit cannot be read completely, and the
// copy reports that instead of returning success on a partial index.
type copySourceReader struct {
	repo           *weaviateRepository
	collectionName string
	// limit is the row limit every query asks for. It starts at the server's
	// documented default and is lowered to the value the server names if it
	// refuses a query for exceeding QUERY_MAXIMUM_RESULTS.
	limit int
}

func newCopySourceReader(repo *weaviateRepository, collectionName string) *copySourceReader {
	limit := repo.copyQueryLimit
	if limit <= 0 {
		limit = defaultCopyQueryLimit
	}
	return &copySourceReader{
		repo:           repo,
		collectionName: collectionName,
		limit:          limit,
	}
}

// readBatch returns every source vector whose chunk_id is in chunkIDs.
func (r *copySourceReader) readBatch(ctx context.Context,
	sourceKnowledgeBaseID string, chunkIDs []string,
) ([]query.Object[map[string]any], error) {
	if len(chunkIDs) == 0 {
		return nil, nil
	}

	objects, err := r.query(ctx, sourceKnowledgeBaseID, chunkIDs)
	if err != nil {
		return nil, err
	}
	if len(objects) < r.limit {
		return objects, nil
	}

	// The batch filled the limit, so it may have been cut off. A batch of one
	// chunk that does so cannot be split any further.
	if len(chunkIDs) == 1 {
		return nil, fmt.Errorf(
			"weaviate: source chunk %q returned a full query page of %d vectors, "+
				"which reaches the server's query limit (QUERY_MAXIMUM_RESULTS); "+
				"the chunk cannot be read completely and the copy was stopped",
			chunkIDs[0], r.limit)
	}

	middle := len(chunkIDs) / 2
	front, err := r.readBatch(ctx, sourceKnowledgeBaseID, chunkIDs[:middle])
	if err != nil {
		return nil, err
	}
	back, err := r.readBatch(ctx, sourceKnowledgeBaseID, chunkIDs[middle:])
	if err != nil {
		return nil, err
	}
	return append(front, back...), nil
}

// query runs one chunk_id-bounded read. A failed query has to be read as a
// failure here - reading it as "the source has no more rows" is how a failed
// copy gets reported as a finished one.
func (r *copySourceReader) query(ctx context.Context,
	sourceKnowledgeBaseID string, chunkIDs []string,
) ([]query.Object[map[string]any], error) {
	log := logger.GetLogger(ctx)
	result, err := r.repo.client.Collections.Use(r.collectionName).Query.OverAll(ctx, query.OverAll{
		Filter: filter.And{
			filter.Cond{Target: fieldKnowledgeBaseID, Operator: filter.Equal, Value: sourceKnowledgeBaseID},
			filter.Cond{Target: fieldChunkID, Operator: filter.ContainsAny, Value: chunkIDs},
		},
		Limit:            r.limit,
		ReturnProperties: retrieveProperties(),
		ReturnVectors:    []string{fieldEmbedding},
	})
	if err != nil {
		if serverLimit, ok := queryMaximumResults(err.Error()); ok && serverLimit < r.limit {
			// QUERY_MAXIMUM_RESULTS is a server setting with no client-facing
			// API, so the refusal is the only place it can be read. Retry the
			// same read under the limit the server actually enforces; the
			// completeness check above uses the lowered limit from here on.
			log.Warnf("[Weaviate] Source query limit %d exceeds the server's "+
				"QUERY_MAXIMUM_RESULTS %d; retrying with %d",
				r.limit, serverLimit, serverLimit)
			r.limit = serverLimit
			return r.query(ctx, sourceKnowledgeBaseID, chunkIDs)
		}
		log.Errorf("[Weaviate] Failed to query source points: %v", err)
		return nil, fmt.Errorf("weaviate: copy indices query failed: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("weaviate: copy indices invalid response for collection %s", r.collectionName)
	}
	return result.Objects, nil
}

// queryMaximumResultsPattern matches the refusal Weaviate returns for a query
// that asks for more rows than QUERY_MAXIMUM_RESULTS allows, e.g. "query
// maximum results exceeded: ... exceeds the configured value for
// QUERY_MAXIMUM_RESULTS '10000'" (weaviate/adapters/repos/db/search.go).
var queryMaximumResultsPattern = regexp.MustCompile(`QUERY_MAXIMUM_RESULTS '(\d+)'`)

// queryMaximumResults returns the row cap the server names in a refusal, if
// message is one.
func queryMaximumResults(message string) (int, bool) {
	match := queryMaximumResultsPattern.FindStringSubmatch(message)
	if match == nil {
		return 0, false
	}
	limit, err := strconv.Atoi(match[1])
	if err != nil || limit <= 0 {
		return 0, false
	}
	return limit, true
}

func (w *weaviateRepository) ListCollections(ctx context.Context) ([]string, error) {
	schema, err := w.client.Collections.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("weaviate 获取 schema 失败: %w", err)
	}
	var collectionNames []string
	for _, collection := range schema {
		collectionNames = append(collectionNames, collection.Name)
	}

	return collectionNames, nil
}

// indexObjectID keeps the existing ID of a chunk's body while assigning each
// generated question its own stable UUID. Source IDs are not necessarily UUIDs.
// Deterministic IDs let a retried batch update the same objects.
func indexObjectID(sourceID, chunkID string) string {
	if sourceID == "" || sourceID == chunkID {
		return chunkID
	}
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(chunkID+"\x00"+sourceID)).String()
}

// deleteWhereContainsAny deletes every object whose field holds one of values.
func (w *weaviateRepository) deleteWhereContainsAny(ctx context.Context,
	collectionName, field string, values []string,
) error {
	_, err := w.client.Collections.Use(collectionName).Data.DeleteSelected(ctx, data.DeleteSelected{
		Filter: filter.Cond{Target: field, Operator: filter.ContainsAny, Value: values},
	})
	return err
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

// createObject builds the object an embedding is stored as: a chunk's body
// under the chunk ID, each generated question under an ID of its own.
func createObject(embedding *WeaviateVectorEmbedding) (*data.Object, error) {
	id, err := chunkUUID(indexObjectID(embedding.SourceID, embedding.ChunkID))
	if err != nil {
		return nil, err
	}
	return &data.Object{
		UUID:       &id,
		Properties: createPayload(embedding),
		Vectors:    []wtypes.Vector{embeddingVector(embedding.Embedding)},
	}, nil
}

// describeInsertError spells out the per-object messages an InsertError
// carries; its own Error() only counts them.
func describeInsertError(err error) error {
	var insertErr data.InsertError
	if !errors.As(err, &insertErr) || len(insertErr.Errors) == 0 {
		return err
	}
	positions := slices.Sorted(maps.Keys(insertErr.Errors))
	return fmt.Errorf("%w: object %d: %s", err, positions[0], insertErr.Errors[positions[0]])
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

// retrieveProperties lists the properties every read returns.
func retrieveProperties() []string {
	return []string{
		fieldContent,
		fieldSourceID,
		fieldSourceType,
		fieldChunkID,
		fieldKnowledgeID,
		fieldKnowledgeBaseID,
		fieldTagID,
	}
}

// parseSearchResult converts vector or keyword search results to the domain model.
func parseSearchResult(result *query.Result, matchType types.MatchType) []*types.IndexWithScore {
	if result == nil {
		return nil
	}
	results := make([]*types.IndexWithScore, 0, len(result.Objects))
	for _, obj := range result.Objects {
		score := 0.0
		if matchType == types.MatchTypeKeywords {
			if obj.Metadata.Score != nil {
				score = 1.0
			}
		} else if obj.Metadata.Certainty != nil {
			// certainty = (1 + cos) / 2; report cosine similarity so
			// scores compare with the other engines and thresholds.
			score = 2*float64(*obj.Metadata.Certainty) - 1
		}

		props := obj.Properties
		getString := func(key string) string {
			if v, ok := props[key].(string); ok {
				return v
			}
			return ""
		}
		getInt := func(key string) int {
			switch v := props[key].(type) {
			case int64:
				return int(v)
			case float64:
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
			score,
		}

		results = append(results, fromWeaviateVectorEmbedding(obj.UUID.String(), embedding, matchType))
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
