package v8

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	elasticsearchRetriever "github.com/Tencent/WeKnora/internal/application/repository/retriever/elasticsearch"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	typesLocal "github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/typedapi/core/search"
	"github.com/elastic/go-elasticsearch/v8/typedapi/types"
	"github.com/elastic/go-elasticsearch/v8/typedapi/types/enums/scriptlanguage"
	"github.com/google/uuid"
)

// elasticsearchRepository implements the RetrieveEngineRepository interface for Elasticsearch v8
type elasticsearchRepository struct {
	client *elasticsearch.TypedClient // Elasticsearch client instance
	index  string                     // Name of the Elasticsearch index to use
}

// NewElasticsearchEngineRepository creates and initializes a new Elasticsearch v8 repository
// It sets up the index and returns a repository instance ready for use
func NewElasticsearchEngineRepository(client *elasticsearch.TypedClient,
	config *config.Config,
) interfaces.RetrieveEngineRepository {
	log := logger.GetLogger(context.Background())
	log.Info("[Elasticsearch] Initializing Elasticsearch v8 retriever engine repository")

	// Get index name from environment variable or use default
	indexName := os.Getenv("ELASTICSEARCH_INDEX")
	if indexName == "" {
		log.Warn("[Elasticsearch] ELASTICSEARCH_INDEX environment variable not set, using default index name")
		indexName = "xwrag_default"
	}

	// Create repository instance and ensure index exists
	res := &elasticsearchRepository{client: client, index: indexName}
	if err := res.createIndexIfNotExists(context.Background()); err != nil {
		log.Errorf("[Elasticsearch] Failed to create index: %v", err)
	} else {
		log.Info("[Elasticsearch] Successfully initialized repository")
	}
	return res
}

// EngineType returns the type of retriever engine (Elasticsearch)
func (e *elasticsearchRepository) EngineType() typesLocal.RetrieverEngineType {
	return typesLocal.ElasticsearchRetrieverEngineType
}

// Support returns the retrieval types supported by this repository (Keywords and Vector)
func (e *elasticsearchRepository) Support() []typesLocal.RetrieverType {
	return []typesLocal.RetrieverType{typesLocal.KeywordsRetrieverType, typesLocal.VectorRetrieverType}
}

// calculateStorageSize estimates the storage size in bytes for a single index document
func (e *elasticsearchRepository) calculateStorageSize(embedding *elasticsearchRetriever.VectorEmbedding) int64 {
	// 1. Content text size
	contentSizeBytes := int64(len(embedding.Content))

	// 2. Vector embedding size
	var vectorSizeBytes int64 = 0
	if embedding.Embedding != nil {
		// 4 bytes per dimension (full precision float)
		vectorSizeBytes = int64(len(embedding.Embedding) * 4)
	}

	// 3. Metadata size (IDs, timestamps, and other fixed overhead)
	metadataSizeBytes := int64(250) // Approximately 250 bytes of metadata

	// 4. Index overhead (Elasticsearch index expansion factor ~1.5)
	indexOverheadBytes := (contentSizeBytes + vectorSizeBytes) * 5 / 10

	// Total size in bytes
	totalSizeBytes := contentSizeBytes + vectorSizeBytes + metadataSizeBytes + indexOverheadBytes
	return totalSizeBytes
}

// EstimateStorageSize calculates the estimated storage size for a list of indices
// Returns the total size in bytes
func (e *elasticsearchRepository) EstimateStorageSize(ctx context.Context,
	indexInfoList []*typesLocal.IndexInfo, params map[string]any,
) int64 {
	var totalStorageSize int64 = 0
	for _, embedding := range indexInfoList {
		embeddingDB := elasticsearchRetriever.ToDBVectorEmbedding(embedding, params)
		totalStorageSize += e.calculateStorageSize(embeddingDB)
	}
	logger.GetLogger(ctx).Infof(
		"[Elasticsearch] Storage size for %d indices: %d bytes", len(indexInfoList), totalStorageSize,
	)
	return totalStorageSize
}

// Save stores a single index document in Elasticsearch
// Returns an error if the operation fails
func (e *elasticsearchRepository) Save(ctx context.Context,
	embedding *typesLocal.IndexInfo,
	additionalParams map[string]any,
) error {
	log := logger.GetLogger(ctx)
	log.Debugf("[Elasticsearch] Saving index for chunk ID: %s", embedding.ChunkID)

	// Convert to database format
	embeddingDB := elasticsearchRetriever.ToDBVectorEmbedding(embedding, additionalParams)
	if len(embeddingDB.Embedding) == 0 {
		err := fmt.Errorf("empty embedding vector for chunk ID: %s", embedding.ChunkID)
		log.Errorf("[Elasticsearch] %v", err)
		return err
	}

	// Index the document
	resp, err := e.client.Index(e.index).Request(embeddingDB).Do(ctx)
	if err != nil {
		log.Errorf("[Elasticsearch] Failed to save index: %v", err)
		return err
	}

	log.Infof("[Elasticsearch] Successfully saved index for chunk ID: %s, document ID: %s", embedding.ChunkID, resp.Id_)
	return nil
}

// BatchSave stores multiple index documents in Elasticsearch using bulk API
// Returns an error if the operation fails
func (e *elasticsearchRepository) BatchSave(ctx context.Context,
	embeddingList []*typesLocal.IndexInfo, additionalParams map[string]any,
) error {
	log := logger.GetLogger(ctx)
	if len(embeddingList) == 0 {
		log.Warn("[Elasticsearch] Empty list provided to BatchSave, skipping")
		return nil
	}

	log.Infof("[Elasticsearch] Batch saving %d indices", len(embeddingList))
	indexRequest := e.client.Bulk().Index(e.index)

	// Add each document to the bulk request
	for _, embedding := range embeddingList {
		embeddingDB := elasticsearchRetriever.ToDBVectorEmbedding(embedding, additionalParams)
		err := indexRequest.CreateOp(types.CreateOperation{Index_: &e.index}, embeddingDB)
		if err != nil {
			log.Errorf("[Elasticsearch] Failed to create bulk operation: %v", err)
			return fmt.Errorf("failed to create op: %w", err)
		}
		log.Debugf("[Elasticsearch] Added chunk ID %s to bulk request", embedding.ChunkID)
	}

	// Execute the bulk request
	_, err := indexRequest.Do(ctx)
	if err != nil {
		log.Errorf("[Elasticsearch] Failed to execute bulk operation: %v", err)
		return fmt.Errorf("failed to do bulk: %w", err)
	}

	log.Infof("[Elasticsearch] Successfully batch saved %d indices", len(embeddingList))
	return nil
}

// DeleteByChunkIDList removes documents from the index based on chunk IDs
// Returns an error if the delete operation fails
func (e *elasticsearchRepository) DeleteByChunkIDList(ctx context.Context, chunkIDList []string, dimension int, knowledgeType string) error {
	log := logger.GetLogger(ctx)
	if len(chunkIDList) == 0 {
		log.Warn("[Elasticsearch] Empty chunk ID list provided for deletion, skipping")
		return nil
	}

	log.Infof("[Elasticsearch] Deleting indices by chunk IDs, count: %d", len(chunkIDList))
	// Use DeleteByQuery to delete all documents matching the chunk IDs
	_, err := e.client.DeleteByQuery(e.index).Query(&types.Query{
		Terms: &types.TermsQuery{TermsQuery: map[string]types.TermsQueryField{"chunk_id.keyword": chunkIDList}},
	}).Do(ctx)
	if err != nil {
		log.Errorf("[Elasticsearch] Failed to delete by chunk IDs: %v", err)
		return fmt.Errorf("failed to delete by query: %w", err)
	}

	log.Infof("[Elasticsearch] Successfully deleted documents by chunk IDs")
	return nil
}

// DeleteBySourceIDList removes documents from the index based on source IDs
// Returns an error if the delete operation fails
func (e *elasticsearchRepository) DeleteBySourceIDList(ctx context.Context, sourceIDList []string, dimension int, knowledgeType string) error {
	log := logger.GetLogger(ctx)
	if len(sourceIDList) == 0 {
		log.Warn("[Elasticsearch] Empty source ID list provided for deletion, skipping")
		return nil
	}

	log.Infof("[Elasticsearch] Deleting indices by source IDs, count: %d", len(sourceIDList))
	// Use DeleteByQuery to delete all documents matching the source IDs
	_, err := e.client.DeleteByQuery(e.index).Query(&types.Query{
		Terms: &types.TermsQuery{TermsQuery: map[string]types.TermsQueryField{"source_id.keyword": sourceIDList}},
	}).Do(ctx)
	if err != nil {
		log.Errorf("[Elasticsearch] Failed to delete by source IDs: %v", err)
		return fmt.Errorf("failed to delete by query: %w", err)
	}

	log.Infof("[Elasticsearch] Successfully deleted documents by source IDs")
	return nil
}

// DeleteByKnowledgeIDList removes documents from the index based on knowledge IDs
// Returns an error if the delete operation fails
func (e *elasticsearchRepository) DeleteByKnowledgeIDList(ctx context.Context,
	knowledgeIDList []string, dimension int, knowledgeType string,
) error {
	log := logger.GetLogger(ctx)
	if len(knowledgeIDList) == 0 {
		log.Warn("[Elasticsearch] Empty knowledge ID list provided for deletion, skipping")
		return nil
	}

	log.Infof("[Elasticsearch] Deleting indices by knowledge IDs, count: %d", len(knowledgeIDList))
	// Use DeleteByQuery to delete all documents matching the knowledge IDs
	_, err := e.client.DeleteByQuery(e.index).Query(&types.Query{
		Terms: &types.TermsQuery{TermsQuery: map[string]types.TermsQueryField{"knowledge_id.keyword": knowledgeIDList}},
	}).Do(ctx)
	if err != nil {
		log.Errorf("[Elasticsearch] Failed to delete by knowledge IDs: %v", err)
		return fmt.Errorf("failed to delete by query: %w", err)
	}

	log.Infof("[Elasticsearch] Successfully deleted documents by knowledge IDs")
	return nil
}

// getBaseConds creates the base query conditions for retrieval operations
// Returns a slice of Query objects with must and must_not conditions
// KnowledgeBaseIDs and KnowledgeIDs use AND logic (search specific documents within knowledge bases)
func (e *elasticsearchRepository) getBaseConds(params typesLocal.RetrieveParams) []types.Query {
	must := []types.Query{}

	// KnowledgeBaseIDs and KnowledgeIDs use AND logic
	// - If only KnowledgeBaseIDs: search entire knowledge bases
	// - If only KnowledgeIDs: search specific documents
	// - If both: search specific documents within the knowledge bases (AND)
	if len(params.KnowledgeBaseIDs) > 0 {
		must = append(must, types.Query{Terms: &types.TermsQuery{
			TermsQuery: map[string]types.TermsQueryField{
				"knowledge_base_id.keyword": params.KnowledgeBaseIDs,
			},
		}})
	}
	if len(params.KnowledgeIDs) > 0 {
		must = append(must, types.Query{Terms: &types.TermsQuery{
			TermsQuery: map[string]types.TermsQueryField{
				"knowledge_id.keyword": params.KnowledgeIDs,
			},
		}})
	}
	// Filter by tag IDs if specified
	if len(params.TagIDs) > 0 {
		must = append(must, types.Query{Terms: &types.TermsQuery{
			TermsQuery: map[string]types.TermsQueryField{
				"tag_id.keyword": params.TagIDs,
			},
		}})
	}

	mustNot := make([]types.Query, 0)
	// Exclude disabled chunks (is_enabled = false)
	// Note: Historical data without is_enabled field will be included (not matching must_not)
	mustNot = append(mustNot, types.Query{Term: map[string]types.TermQuery{
		"is_enabled": {Value: false},
	}})
	if len(params.ExcludeKnowledgeIDs) > 0 {
		mustNot = append(mustNot, types.Query{Terms: &types.TermsQuery{
			TermsQuery: map[string]types.TermsQueryField{"knowledge_id.keyword": params.ExcludeKnowledgeIDs},
		}})
	}
	if len(params.ExcludeChunkIDs) > 0 {
		mustNot = append(mustNot, types.Query{Terms: &types.TermsQuery{
			TermsQuery: map[string]types.TermsQueryField{"chunk_id.keyword": params.ExcludeChunkIDs},
		}})
	}
	return []types.Query{{Bool: &types.BoolQuery{Must: must, MustNot: mustNot}}}
}

// createIndexIfNotExists checks if the specified index exists and creates it if not
// Returns an error if the operation fails
func (e *elasticsearchRepository) createIndexIfNotExists(ctx context.Context) error {
	log := logger.GetLogger(ctx)
	log.Debugf("[Elasticsearch] Checking if index exists: %s", e.index)

	// Check if index exists
	exists, err := e.client.Indices.Exists(e.index).Do(ctx)
	if err != nil {
		log.Errorf("[Elasticsearch] Failed to check if index exists: %v", err)
		return err
	}

	if exists {
		log.Debugf("[Elasticsearch] Index already exists: %s", e.index)
		return nil
	}

	// Create index if it doesn't exist
	log.Infof("[Elasticsearch] Creating index: %s", e.index)
	_, err = e.client.Indices.Create(e.index).Do(ctx)
	if err != nil {
		log.Errorf("[Elasticsearch] Failed to create index: %v", err)
		return err
	}

	log.Infof("[Elasticsearch] Index created successfully: %s", e.index)
	return nil
}

// Retrieve dispatches the retrieval operation to the appropriate method based on retriever type
// Returns a slice of RetrieveResult and an error if the operation fails
func (e *elasticsearchRepository) Retrieve(ctx context.Context,
	params typesLocal.RetrieveParams,
) ([]*typesLocal.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	log.Debugf("[Elasticsearch] Processing retrieval request of type: %s", params.RetrieverType)

	// Route to appropriate retrieval method
	switch params.RetrieverType {
	case typesLocal.VectorRetrieverType:
		return e.VectorRetrieve(ctx, params)
	case typesLocal.KeywordsRetrieverType:
		return e.KeywordsRetrieve(ctx, params)
	}

	err := fmt.Errorf("invalid retriever type: %v", params.RetrieverType)
	log.Errorf("[Elasticsearch] %v", err)
	return nil, err
}

// VectorRetrieve performs vector similarity search using cosine similarity
// Returns a slice of RetrieveResult containing matching documents
func (e *elasticsearchRepository) VectorRetrieve(ctx context.Context,
	params typesLocal.RetrieveParams,
) ([]*typesLocal.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	log.Infof("[Elasticsearch] Vector retrieval: dim=%d, topK=%d, threshold=%.4f",
		len(params.Embedding), params.TopK, params.Threshold)

	filter := e.getBaseConds(params)

	// Build script scoring query with cosine similarity
	queryVectorJSON, err := json.Marshal(params.Embedding)
	if err != nil {
		log.Errorf("[Elasticsearch] Failed to marshal query vector: %v", err)
		return nil, fmt.Errorf("failed to marshal query embedding: %w", err)
	}

	scoreSource := "cosineSimilarity(params.query_vector, 'embedding')"
	minScore := float32(params.Threshold)
	scriptScore := &types.ScriptScoreQuery{
		Query: types.Query{Bool: &types.BoolQuery{Filter: filter}},
		Script: types.Script{
			Source: &scoreSource,
			Params: map[string]json.RawMessage{
				"query_vector": json.RawMessage(queryVectorJSON),
			},
		},
		MinScore: &minScore,
	}

	log.Debugf("[Elasticsearch] Executing vector search in index: %s", e.index)
	// Execute search with minimum score threshold
	response, err := e.client.Search().Index(e.index).Request(&search.Request{
		Query: &types.Query{ScriptScore: scriptScore},
		Size:  &params.TopK,
	}).Do(ctx)
	if err != nil {
		log.Errorf("[Elasticsearch] Vector search failed: %v", err)
		return nil, err
	}

	// Process search results
	var results []*typesLocal.IndexWithScore
	for _, hit := range response.Hits.Hits {
		var embedding *elasticsearchRetriever.VectorEmbeddingWithScore
		if err := json.Unmarshal(hit.Source_, &embedding); err != nil {
			log.Errorf("[Elasticsearch] Failed to unmarshal search result: %v", err)
			return nil, err
		}
		embedding.Score = float64(*hit.Score_)
		results = append(results,
			elasticsearchRetriever.FromDBVectorEmbeddingWithScore(*hit.Id_, embedding, typesLocal.MatchTypeEmbedding))
	}

	if len(results) == 0 {
		log.Warnf("[Elasticsearch] No vector matches found that meet threshold %.4f", params.Threshold)
	} else {
		log.Infof("[Elasticsearch] Vector retrieval found %d results", len(results))
		log.Debugf("[Elasticsearch] Top result score: %.4f", results[0].Score)
	}

	return []*typesLocal.RetrieveResult{
		{
			Results:             results,
			RetrieverEngineType: typesLocal.ElasticsearchRetrieverEngineType,
			RetrieverType:       typesLocal.VectorRetrieverType,
			Error:               nil,
		},
	}, nil
}

// KeywordsRetrieve performs keyword-based search in document content
// Returns a slice of RetrieveResult containing matching documents
func (e *elasticsearchRepository) KeywordsRetrieve(ctx context.Context,
	params typesLocal.RetrieveParams,
) ([]*typesLocal.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	log.Infof("[Elasticsearch] Performing keywords retrieval with query: %s, topK: %d", params.Query, params.TopK)

	filter := e.getBaseConds(params)
	// Build must conditions for content matching
	must := []types.Query{
		{Match: map[string]types.MatchQuery{"content": {Query: params.Query}}},
	}

	log.Debugf("[Elasticsearch] Executing keyword search in index: %s", e.index)
	response, err := e.client.Search().Index(e.index).Request(&search.Request{
		Query: &types.Query{Bool: &types.BoolQuery{Filter: filter, Must: must}},
		Size:  &params.TopK,
	}).Do(ctx)
	if err != nil {
		log.Errorf("[Elasticsearch] Keywords search failed: %v", err)
		return nil, err
	}

	// Process search results
	var results []*typesLocal.IndexWithScore
	for _, hit := range response.Hits.Hits {
		var embedding *elasticsearchRetriever.VectorEmbeddingWithScore
		if err := json.Unmarshal(hit.Source_, &embedding); err != nil {
			log.Errorf("[Elasticsearch] Failed to unmarshal search result: %v", err)
			return nil, err
		}
		embedding.Score = float64(*hit.Score_)
		results = append(results,
			elasticsearchRetriever.FromDBVectorEmbeddingWithScore(*hit.Id_, embedding, typesLocal.MatchTypeKeywords),
		)
	}

	if len(results) == 0 {
		log.Warnf("[Elasticsearch] No keyword matches found for query: %s", params.Query)
	} else {
		log.Infof("[Elasticsearch] Keywords retrieval found %d results", len(results))
		log.Debugf("[Elasticsearch] Top result score: %.4f", results[0].Score)
	}

	return []*typesLocal.RetrieveResult{
		{
			Results:             results,
			RetrieverEngineType: typesLocal.ElasticsearchRetrieverEngineType,
			RetrieverType:       typesLocal.KeywordsRetrieverType,
			Error:               nil,
		},
	}, nil
}

// CopyIndices 复制索引数据
func (e *elasticsearchRepository) CopyIndices(ctx context.Context,
	sourceKnowledgeBaseID string,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
	dimension int,
	knowledgeType string,
) error {
	log := logger.GetLogger(ctx)
	log.Infof(
		"[Elasticsearch] Copying indices from source knowledge base %s to target knowledge base %s, count: %d",
		sourceKnowledgeBaseID, targetKnowledgeBaseID, len(sourceToTargetChunkIDMap),
	)

	if len(sourceToTargetChunkIDMap) == 0 {
		log.Warn("[Elasticsearch] Empty mapping, skipping copy")
		return nil
	}

	// Build query parameters
	params := typesLocal.RetrieveParams{
		KnowledgeBaseIDs: []string{sourceKnowledgeBaseID},
	}

	// Build base query conditions
	filter := e.getBaseConds(params)

	// Set batch processing parameters
	batchSize := 500
	from := 0
	totalCopied := 0

	for {
		// Execute pagination query
		searchResponse, err := e.client.Search().Index(e.index).
			Query(&types.Query{Bool: &types.BoolQuery{Filter: filter}}).
			From(from).
			Size(batchSize).
			Do(ctx)
		if err != nil {
			log.Errorf("[Elasticsearch] Failed to query source index data: %v", err)
			return err
		}

		hitsCount := len(searchResponse.Hits.Hits)
		if hitsCount == 0 {
			break
		}

		log.Infof("[Elasticsearch] Found %d source index data, batch start position: %d", hitsCount, from)

		// Prepare index list for BatchSave
		var indexInfoList []*typesLocal.IndexInfo

		// Collect all embedding vector data for additionalParams
		embeddingMap := make(map[string][]float32)

		for _, hit := range searchResponse.Hits.Hits {
			// Parse source document
			var sourceDoc elasticsearchRetriever.VectorEmbedding
			if err := json.Unmarshal(hit.Source_, &sourceDoc); err != nil {
				log.Errorf("[Elasticsearch] Failed to parse source index data: %v", err)
				continue
			}

			// Get mapped target chunk ID and knowledge ID
			targetChunkID, ok := sourceToTargetChunkIDMap[sourceDoc.ChunkID]
			if !ok {
				log.Warnf("[Elasticsearch] Source chunk %s not found in target mapping, skipping", sourceDoc.ChunkID)
				continue
			}

			targetKnowledgeID, ok := sourceToTargetKBIDMap[sourceDoc.KnowledgeID]
			if !ok {
				log.Warnf(
					"[Elasticsearch] Source knowledge %s not found in target mapping, skipping",
					sourceDoc.KnowledgeID,
				)
				continue
			}

			// Save embedding vector to embeddingMap
			if len(sourceDoc.Embedding) > 0 {
				embeddingMap[targetChunkID] = sourceDoc.Embedding
			}

			// Handle SourceID transformation for generated questions
			// Generated questions have SourceID format: {chunkID}-{questionID}
			// Regular chunks have SourceID == ChunkID
			var targetSourceID string
			if sourceDoc.SourceID == sourceDoc.ChunkID {
				// Regular chunk, use targetChunkID as SourceID
				targetSourceID = targetChunkID
			} else if strings.HasPrefix(sourceDoc.SourceID, sourceDoc.ChunkID+"-") {
				// This is a generated question, preserve the questionID part
				questionID := strings.TrimPrefix(sourceDoc.SourceID, sourceDoc.ChunkID+"-")
				targetSourceID = fmt.Sprintf("%s-%s", targetChunkID, questionID)
			} else {
				// For other complex scenarios, generate new unique SourceID
				targetSourceID = uuid.New().String()
			}

			// Create new index information
			indexInfo := &typesLocal.IndexInfo{
				Content:         sourceDoc.Content,
				SourceID:        targetSourceID,
				SourceType:      typesLocal.SourceType(sourceDoc.SourceType),
				ChunkID:         targetChunkID,
				KnowledgeID:     targetKnowledgeID,
				KnowledgeBaseID: targetKnowledgeBaseID,
			}

			indexInfoList = append(indexInfoList, indexInfo)
			totalCopied++
		}

		// Use BatchSave function to save index
		if len(indexInfoList) > 0 {
			// Add embedding vector to additional parameters
			additionalParams := map[string]any{
				"embedding": embeddingMap,
			}

			if err := e.BatchSave(ctx, indexInfoList, additionalParams); err != nil {
				log.Errorf("[Elasticsearch] Failed to batch save index: %v", err)
				return err
			}

			log.Infof("[Elasticsearch] Successfully copied batch data, batch size: %d, total copied: %d",
				len(indexInfoList), totalCopied)
		}

		// Move to next batch
		from += hitsCount

		// If the number of returned records is less than the request size, it means the last page has been reached
		if hitsCount < batchSize {
			break
		}
	}

	log.Infof("[Elasticsearch] Index copy completed, total copied: %d", totalCopied)
	return nil
}

// BatchUpdateChunkEnabledStatus updates the enabled status of chunks in batch
func (e *elasticsearchRepository) BatchUpdateChunkEnabledStatus(
	ctx context.Context,
	chunkStatusMap map[string]bool,
) error {
	log := logger.GetLogger(ctx)
	if len(chunkStatusMap) == 0 {
		log.Warnf("[Elasticsearch] Chunk status map is empty, skipping update")
		return nil
	}

	log.Infof("[Elasticsearch] Batch updating chunk enabled status, count: %d", len(chunkStatusMap))

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

	// Batch update enabled chunks using update_by_query
	if len(enabledChunkIDs) > 0 {
		query := types.NewQuery()
		query.Bool = &types.BoolQuery{
			Must: []types.Query{
				{Terms: &types.TermsQuery{
					TermsQuery: map[string]types.TermsQueryField{
						"chunk_id.keyword": enabledChunkIDs,
					},
				}},
			},
		}
		source := "ctx._source.is_enabled = true"
		lang := scriptlanguage.Painless
		script := types.Script{
			Source: &source,
			Lang:   &lang,
		}
		_, err := e.client.UpdateByQuery(e.index).Query(query).Script(&script).Do(ctx)
		if err != nil {
			log.Errorf("[Elasticsearch] Failed to update enabled chunks: %v", err)
			return err
		}
		log.Infof("[Elasticsearch] Updated %d chunks to enabled", len(enabledChunkIDs))
	}

	// Batch update disabled chunks using update_by_query
	if len(disabledChunkIDs) > 0 {
		query := types.NewQuery()
		query.Bool = &types.BoolQuery{
			Must: []types.Query{
				{Terms: &types.TermsQuery{
					TermsQuery: map[string]types.TermsQueryField{
						"chunk_id.keyword": disabledChunkIDs,
					},
				}},
			},
		}
		source := "ctx._source.is_enabled = false"
		lang := scriptlanguage.Painless
		script := types.Script{
			Source: &source,
			Lang:   &lang,
		}
		_, err := e.client.UpdateByQuery(e.index).Query(query).Script(&script).Do(ctx)
		if err != nil {
			log.Errorf("[Elasticsearch] Failed to update disabled chunks: %v", err)
			return err
		}
		log.Infof("[Elasticsearch] Updated %d chunks to disabled", len(disabledChunkIDs))
	}

	log.Infof("[Elasticsearch] Successfully batch updated chunk enabled status")
	return nil
}

// BatchUpdateChunkTagID updates the tag ID of chunks in batch
func (e *elasticsearchRepository) BatchUpdateChunkTagID(
	ctx context.Context,
	chunkTagMap map[string]string,
) error {
	log := logger.GetLogger(ctx)
	if len(chunkTagMap) == 0 {
		log.Warnf("[Elasticsearch] Chunk tag map is empty, skipping update")
		return nil
	}

	log.Infof("[Elasticsearch] Batch updating chunk tag ID, count: %d", len(chunkTagMap))

	// Group chunks by tag ID for batch updates
	tagGroups := make(map[string][]string)
	for chunkID, tagID := range chunkTagMap {
		tagGroups[tagID] = append(tagGroups[tagID], chunkID)
	}

	// Batch update chunks for each tag ID using update_by_query
	for tagID, chunkIDs := range tagGroups {
		query := types.NewQuery()
		query.Bool = &types.BoolQuery{
			Must: []types.Query{
				{Terms: &types.TermsQuery{
					TermsQuery: map[string]types.TermsQueryField{
						"chunk_id.keyword": chunkIDs,
					},
				}},
			},
		}
		source := "ctx._source.tag_id = params.tag_id"
		lang := scriptlanguage.Painless
		script := types.Script{
			Source: &source,
			Lang:   &lang,
			Params: map[string]json.RawMessage{
				"tag_id": json.RawMessage(`"` + tagID + `"`),
			},
		}
		_, err := e.client.UpdateByQuery(e.index).Query(query).Script(&script).Do(ctx)
		if err != nil {
			log.Errorf("[Elasticsearch] Failed to update chunks with tag_id %s: %v", tagID, err)
			return err
		}
		log.Infof("[Elasticsearch] Updated %d chunks to tag_id=%s", len(chunkIDs), tagID)
	}

	log.Infof("[Elasticsearch] Successfully batch updated chunk tag ID")
	return nil
}
