// Package v7 implements the Elasticsearch v7 retriever engine repository
// It provides functionality for storing and retrieving embeddings using Elasticsearch v7
// The repository supports both single and batch operations for saving and deleting embeddings
// It also supports retrieving embeddings using different retrieval methods
// The repository is used to store and retrieve embeddings for different tasks
// It is used to store and retrieve embeddings for different tasks
package v7

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	elasticsearchRetriever "github.com/Tencent/WeKnora/internal/application/repository/retriever/elasticsearch"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	typesLocal "github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/elastic/go-elasticsearch/v7"
	"github.com/elastic/go-elasticsearch/v7/esapi"
	"github.com/google/uuid"
)

type elasticsearchRepository struct {
	client *elasticsearch.Client
	index  string
}

func NewElasticsearchEngineRepository(client *elasticsearch.Client,
	config *config.Config,
) interfaces.RetrieveEngineRepository {
	log := logger.GetLogger(context.Background())
	log.Info("[ElasticsearchV7] Initializing Elasticsearch v7 retriever engine repository")

	indexName := os.Getenv("ELASTICSEARCH_INDEX")
	if indexName == "" {
		log.Warn("[ElasticsearchV7] ELASTICSEARCH_INDEX environment variable not set, using default index name")
		indexName = "xwrag_default"
	}

	log.Infof("[ElasticsearchV7] Using index: %s", indexName)
	res := &elasticsearchRepository{client: client, index: indexName}
	return res
}

func (e *elasticsearchRepository) EngineType() typesLocal.RetrieverEngineType {
	return typesLocal.ElasticsearchRetrieverEngineType
}

func (e *elasticsearchRepository) Support() []typesLocal.RetrieverType {
	return []typesLocal.RetrieverType{typesLocal.KeywordsRetrieverType}
}

// EstimateStorageSize 估算存储空间大小
func (e *elasticsearchRepository) EstimateStorageSize(ctx context.Context,
	indexInfoList []*typesLocal.IndexInfo, params map[string]any,
) int64 {
	log := logger.GetLogger(ctx)
	log.Infof("[ElasticsearchV7] Estimating storage size for %d indices", len(indexInfoList))

	// 计算总存储大小
	var totalStorageSize int64 = 0
	for _, indexInfo := range indexInfoList {
		embeddingDB := elasticsearchRetriever.ToDBVectorEmbedding(indexInfo, params)
		// 计算单个文档的存储大小并累加
		totalStorageSize += e.calculateStorageSize(embeddingDB)
	}

	// 记录存储大小
	log.Infof("[ElasticsearchV7] Estimated storage size: %d bytes (%d MB) for %d indices",
		totalStorageSize, totalStorageSize/(1024*1024), len(indexInfoList))
	return totalStorageSize
}

// 计算单个索引的存储占用大小(Bytes)
func (e *elasticsearchRepository) calculateStorageSize(embedding *elasticsearchRetriever.VectorEmbedding) int64 {
	// 1. 文本内容大小
	contentSizeBytes := int64(len(embedding.Content))

	// 2. 向量存储大小
	var vectorSizeBytes int64 = 0
	if embedding.Embedding != nil {
		// 4字节/维度 (全精度浮点数)
		vectorSizeBytes = int64(len(embedding.Embedding) * 4)
	}

	// 3. 元数据大小 (ID、时间戳等固定开销)
	metadataSizeBytes := int64(250) // 约250字节的元数据

	// 4. 索引开销 (ES索引的放大系数约为1.5)
	indexOverheadBytes := (contentSizeBytes + vectorSizeBytes) * 5 / 10

	// 总大小 (字节)
	totalSizeBytes := contentSizeBytes + vectorSizeBytes + metadataSizeBytes + indexOverheadBytes

	return totalSizeBytes
}

// Save 保存索引
func (e *elasticsearchRepository) Save(ctx context.Context,
	embedding *typesLocal.IndexInfo, additionalParams map[string]any,
) error {
	log := logger.GetLogger(ctx)
	log.Debugf("[ElasticsearchV7] Saving index for chunk ID: %s", embedding.ChunkID)

	embeddingDB := elasticsearchRetriever.ToDBVectorEmbedding(embedding, additionalParams)
	if len(embeddingDB.Embedding) == 0 {
		err := fmt.Errorf("empty embedding vector for chunk ID: %s", embedding.ChunkID)
		log.Errorf("[ElasticsearchV7] %v", err)
		return err
	}

	docBytes, err := json.Marshal(embeddingDB)
	if err != nil {
		log.Errorf("[ElasticsearchV7] Failed to marshal embedding: %v", err)
		return err
	}

	docID := uuid.New().String()
	log.Debugf("[ElasticsearchV7] Creating document with ID: %s for chunk ID: %s", docID, embedding.ChunkID)

	resp, err := e.client.Create(
		e.index,
		docID,
		bytes.NewReader(docBytes),
		e.client.Create.WithContext(ctx),
	)
	if err != nil {
		log.Errorf("[ElasticsearchV7] Failed to create document: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.IsError() {
		log.Errorf("[ElasticsearchV7] Failed to index document: %s", resp.String())
		return fmt.Errorf("failed to index document: %s", resp.String())
	}

	log.Infof(
		"[ElasticsearchV7] Successfully saved index for chunk ID: %s with document ID: %s",
		embedding.ChunkID, docID,
	)
	return nil
}

// BatchSave performs bulk indexing of multiple embeddings in Elasticsearch
// It constructs a bulk request with index operations for each embedding
// Returns error if any operation fails during the bulk indexing process
func (e *elasticsearchRepository) BatchSave(ctx context.Context,
	embeddingList []*typesLocal.IndexInfo, additionalParams map[string]any,
) error {
	log := logger.GetLogger(ctx)
	if len(embeddingList) == 0 {
		log.Warn("[ElasticsearchV7] Empty list provided to BatchSave, skipping")
		return nil
	}

	log.Infof("[ElasticsearchV7] Batch saving %d indices", len(embeddingList))

	// Prepare bulk request body
	body, processedCount, err := e.prepareBulkRequestBody(ctx, embeddingList, additionalParams)
	if err != nil {
		return err
	}

	if processedCount == 0 {
		log.Warn("[ElasticsearchV7] No valid documents to index after filtering, skipping bulk request")
		return nil
	}

	// Execute bulk request
	log.Debugf("[ElasticsearchV7] Executing bulk request with %d documents", processedCount)
	resp, err := e.client.Bulk(
		body,
		e.client.Bulk.WithIndex(e.index),
		e.client.Bulk.WithContext(ctx),
	)
	if err != nil {
		log.Errorf("[ElasticsearchV7] Failed to execute bulk index operation: %v", err)
		return err
	}
	defer resp.Body.Close()

	// Process bulk response
	err = e.processBulkResponse(ctx, resp, len(embeddingList))
	if err != nil {
		return err
	}

	log.Infof("[ElasticsearchV7] Successfully batch saved %d indices", processedCount)
	return nil
}

// prepareBulkRequestBody prepares the bulk request body for batch indexing
func (e *elasticsearchRepository) prepareBulkRequestBody(ctx context.Context,
	embeddingList []*typesLocal.IndexInfo, additionalParams map[string]any,
) (*bytes.Buffer, int, error) {
	log := logger.GetLogger(ctx)
	body := &bytes.Buffer{}
	processedCount := 0

	// Prepare bulk request body
	for _, embedding := range embeddingList {
		// Convert to Elasticsearch document format
		embeddingDB := elasticsearchRetriever.ToDBVectorEmbedding(embedding, additionalParams)

		// Generate document ID and metadata line
		docID := uuid.New().String()
		meta := []byte(fmt.Sprintf(`{ "index" : { "_id" : "%s" } }%s`, docID, "\n"))

		// Marshal document to JSON
		data, err := json.Marshal(embeddingDB)
		if err != nil {
			log.Errorf("[ElasticsearchV7] Failed to marshal embedding for chunk ID %s: %v", embedding.ChunkID, err)
			return nil, 0, err
		}

		// Append newline and add to bulk request body
		data = append(data, "\n"...)
		body.Grow(len(meta) + len(data))
		body.Write(meta)
		body.Write(data)
		processedCount++

		log.Debugf("[ElasticsearchV7] Added document for chunk ID: %s to bulk request", embedding.ChunkID)
	}

	return body, processedCount, nil
}

// processBulkResponse processes the response from a bulk indexing operation
func (e *elasticsearchRepository) processBulkResponse(ctx context.Context,
	resp *esapi.Response, totalDocuments int,
) error {
	log := logger.GetLogger(ctx)

	// Check for bulk operation errors
	if resp.IsError() {
		log.Errorf("[ElasticsearchV7] Bulk operation failed: %s", resp.String())
		return fmt.Errorf("failed to index documents: %s", resp.String())
	}

	// Parse bulk response to check for individual document errors
	var bulkResponse map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&bulkResponse); err != nil {
		log.Warnf("[ElasticsearchV7] Could not parse bulk response: %v", err)
		return nil
	}

	// Check for errors in individual operations
	if hasErrors, ok := bulkResponse["errors"].(bool); ok && hasErrors {
		errorCount := e.countBulkErrors(ctx, bulkResponse, totalDocuments)
		if errorCount > 0 {
			log.Warnf("[ElasticsearchV7] %d/%d documents failed to index", errorCount, totalDocuments)
		}
	}

	return nil
}

// countBulkErrors counts the number of errors in a bulk response
func (e *elasticsearchRepository) countBulkErrors(ctx context.Context,
	bulkResponse map[string]interface{}, totalDocuments int,
) int {
	log := logger.GetLogger(ctx)
	log.Warn("[ElasticsearchV7] Bulk operation completed with some errors")

	errorCount := 0
	if items, ok := bulkResponse["items"].([]interface{}); ok {
		for _, item := range items {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if indexResp, ok := itemMap["index"].(map[string]interface{}); ok {
					if indexResp["error"] != nil {
						errorCount++
						log.Errorf("[ElasticsearchV7] Item error: %v", indexResp["error"])
					}
				}
			}
		}
	}

	return errorCount
}

// DeleteByChunkIDList Delete indices by chunk ID list
func (e *elasticsearchRepository) DeleteByChunkIDList(ctx context.Context, chunkIDList []string, dimension int, knowledgeType string) error {
	return e.deleteByFieldList(ctx, "chunk_id.keyword", chunkIDList)
}

// DeleteBySourceIDList Delete indices by source ID list
func (e *elasticsearchRepository) DeleteBySourceIDList(ctx context.Context, sourceIDList []string, dimension int, knowledgeType string) error {
	return e.deleteByFieldList(ctx, "source_id.keyword", sourceIDList)
}

// DeleteByKnowledgeIDList Delete indices by knowledge ID list
func (e *elasticsearchRepository) DeleteByKnowledgeIDList(ctx context.Context,
	knowledgeIDList []string, dimension int, knowledgeType string,
) error {
	return e.deleteByFieldList(ctx, "knowledge_id.keyword", knowledgeIDList)
}

// deleteByFieldList Delete documents by field value list
func (e *elasticsearchRepository) deleteByFieldList(ctx context.Context, field string, valueList []string) error {
	log := logger.GetLogger(ctx)
	if len(valueList) == 0 {
		log.Warnf("[ElasticsearchV7] Empty %s list provided for deletion, skipping", field)
		return nil
	}

	log.Infof("[ElasticsearchV7] Deleting indices by %s, count: %d", field, len(valueList))
	ids, err := json.Marshal(valueList)
	if err != nil {
		log.Errorf("[ElasticsearchV7] Failed to marshal %s list: %v", field, err)
		return err
	}

	query := fmt.Sprintf(`{"query": {"terms": {"%s": %s}}}`, field, ids)
	log.Debugf("[ElasticsearchV7] Executing delete by query: %s", query)

	resp, err := e.client.DeleteByQuery(
		[]string{e.index},
		bytes.NewReader([]byte(query)),
		e.client.DeleteByQuery.WithContext(ctx),
	)
	if err != nil {
		log.Errorf("[ElasticsearchV7] Failed to execute delete by query: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.IsError() {
		errMsg := fmt.Sprintf("failed to delete by query: %s", resp.String())
		log.Errorf("[ElasticsearchV7] %s", errMsg)
		return fmt.Errorf("failed to delete by query: %s", resp.String())
	}

	// Try to extract deletion count from response
	var deleteResponse map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&deleteResponse); err != nil {
		log.Warnf("[ElasticsearchV7] Could not parse delete response: %v", err)
	} else {
		if deleted, ok := deleteResponse["deleted"].(float64); ok {
			log.Infof("[ElasticsearchV7] Successfully deleted %d documents by %s", int(deleted), field)
		} else {
			log.Infof("[ElasticsearchV7] Successfully deleted documents by %s", field)
		}
	}

	return nil
}

// getBaseConds Construct base Elasticsearch query conditions based on retrieval parameters
// It creates MUST conditions for required fields and MUST_NOT conditions for excluded fields
// KnowledgeBaseIDs and KnowledgeIDs use AND logic (search specific documents within knowledge bases)
// Returns a JSON string representing the query conditions
func (e *elasticsearchRepository) getBaseConds(params typesLocal.RetrieveParams) string {
	// Build MUST conditions (positive filters)
	must := make([]map[string]interface{}, 0)

	// KnowledgeBaseIDs and KnowledgeIDs use AND logic
	// - If only KnowledgeBaseIDs: search entire knowledge bases
	// - If only KnowledgeIDs: search specific documents
	// - If both: search specific documents within the knowledge bases (AND)
	if len(params.KnowledgeBaseIDs) > 0 {
		must = append(must, map[string]interface{}{
			"terms": map[string]interface{}{
				"knowledge_base_id.keyword": params.KnowledgeBaseIDs,
			},
		})
	}
	if len(params.KnowledgeIDs) > 0 {
		must = append(must, map[string]interface{}{
			"terms": map[string]interface{}{
				"knowledge_id.keyword": params.KnowledgeIDs,
			},
		})
	}
	// Filter by tag IDs if specified
	if len(params.TagIDs) > 0 {
		must = append(must, map[string]interface{}{
			"terms": map[string]interface{}{
				"tag_id.keyword": params.TagIDs,
			},
		})
	}

	// Build MUST_NOT conditions (negative filters)
	mustNot := make([]map[string]interface{}, 0)
	// Exclude disabled chunks (is_enabled = false)
	// Note: Historical data without is_enabled field will be included (not matching must_not)
	mustNot = append(mustNot, map[string]interface{}{
		"term": map[string]interface{}{
			"is_enabled": false,
		},
	})
	if len(params.ExcludeKnowledgeIDs) > 0 {
		mustNot = append(mustNot, map[string]interface{}{
			"terms": map[string]interface{}{
				"knowledge_id.keyword": params.ExcludeKnowledgeIDs,
			},
		})
	}
	if len(params.ExcludeChunkIDs) > 0 {
		mustNot = append(mustNot, map[string]interface{}{
			"terms": map[string]interface{}{
				"chunk_id.keyword": params.ExcludeChunkIDs,
			},
		})
	}

	// Combine conditions based on presence
	var query map[string]interface{}
	if len(must) == 0 && len(mustNot) == 0 {
		query = map[string]interface{}{}
	} else if len(must) == 0 {
		query = map[string]interface{}{
			"bool": map[string]interface{}{
				"must_not": mustNot,
			},
		}
	} else if len(mustNot) == 0 {
		query = map[string]interface{}{
			"bool": map[string]interface{}{
				"must": must,
			},
		}
	} else {
		query = map[string]interface{}{
			"bool": map[string]interface{}{
				"must":     must,
				"must_not": mustNot,
			},
		}
	}

	// Marshal to JSON string
	jsonBytes, err := json.Marshal(query)
	if err != nil {
		return "{}"
	}
	return string(jsonBytes)
}

func (e *elasticsearchRepository) Retrieve(ctx context.Context,
	params typesLocal.RetrieveParams,
) ([]*typesLocal.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	log.Debugf("[ElasticsearchV7] Processing retrieval request of type: %s", params.RetrieverType)

	switch params.RetrieverType {
	case typesLocal.KeywordsRetrieverType:
		return e.KeywordsRetrieve(ctx, params)
	}

	err := fmt.Errorf("invalid retriever type: %v", params.RetrieverType)
	log.Errorf("[ElasticsearchV7] %v", err)
	return nil, err
}

// VectorRetrieve Implement vector similarity retrieval
func (e *elasticsearchRepository) VectorRetrieve(ctx context.Context,
	params typesLocal.RetrieveParams,
) ([]*typesLocal.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	log.Infof("[ElasticsearchV7] Vector retrieval: dim=%d, topK=%d, threshold=%.4f",
		len(params.Embedding), params.TopK, params.Threshold)

	// Build search query
	query, err := e.buildVectorSearchQuery(ctx, params)
	if err != nil {
		return nil, err
	}

	// Execute search
	results, err := e.executeVectorSearch(ctx, query)
	if err != nil {
		return nil, err
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

// buildVectorSearchQuery builds the vector search query JSON
func (e *elasticsearchRepository) buildVectorSearchQuery(ctx context.Context,
	params typesLocal.RetrieveParams,
) (string, error) {
	log := logger.GetLogger(ctx)

	// Parse filter conditions
	var filterQuery map[string]interface{}
	filterJSON := e.getBaseConds(params)
	if err := json.Unmarshal([]byte(filterJSON), &filterQuery); err != nil {
		log.Errorf("[ElasticsearchV7] Failed to unmarshal filter: %v", err)
		filterQuery = map[string]interface{}{}
	}

	// Construct the script_score query using structured objects
	queryObj := map[string]interface{}{
		"query": map[string]interface{}{
			"script_score": map[string]interface{}{
				"query": map[string]interface{}{
					"bool": map[string]interface{}{
						"filter": []interface{}{filterQuery},
					},
				},
				"script": map[string]interface{}{
					"source": "cosineSimilarity(params.query_vector,'embedding')",
					"params": map[string]interface{}{
						"query_vector": params.Embedding,
					},
				},
				"min_score": params.Threshold,
			},
		},
		"size": params.TopK,
	}

	// Marshal to JSON string
	queryBytes, err := json.Marshal(queryObj)
	if err != nil {
		log.Errorf("[ElasticsearchV7] Failed to marshal query: %v", err)
		return "", fmt.Errorf("failed to marshal query: %w", err)
	}

	query := string(queryBytes)
	log.Debugf("[ElasticsearchV7] Executing vector search with query: %s", query)
	return query, nil
}

// executeVectorSearch executes the vector search query
func (e *elasticsearchRepository) executeVectorSearch(
	ctx context.Context,
	query string,
) ([]*typesLocal.IndexWithScore, error) {
	log := logger.GetLogger(ctx)

	response, err := e.client.Search(
		e.client.Search.WithIndex(e.index),
		e.client.Search.WithBody(strings.NewReader(query)),
		e.client.Search.WithContext(ctx),
	)
	if err != nil {
		log.Errorf("[ElasticsearchV7] Vector search failed: %v", err)
		return nil, err
	}
	defer response.Body.Close()

	results, err := e.processSearchResponse(ctx, response, typesLocal.VectorRetrieverType)
	if err != nil {
		return nil, err
	}

	return results, nil
}

// KeywordsRetrieve Implement keyword retrieval
func (e *elasticsearchRepository) KeywordsRetrieve(ctx context.Context,
	params typesLocal.RetrieveParams,
) ([]*typesLocal.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	log.Infof("[ElasticsearchV7] Keywords retrieval: query=%s, topK=%d", params.Query, params.TopK)

	// Build search query
	query, err := e.buildKeywordSearchQuery(ctx, params)
	if err != nil {
		return nil, err
	}

	// Execute search
	results, err := e.executeKeywordSearch(ctx, query)
	if err != nil {
		return nil, err
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

// buildKeywordSearchQuery builds the keyword search query JSON
func (e *elasticsearchRepository) buildKeywordSearchQuery(ctx context.Context,
	params typesLocal.RetrieveParams,
) (string, error) {
	log := logger.GetLogger(ctx)
	content, err := json.Marshal(params.Query)
	if err != nil {
		log.Errorf("[ElasticsearchV7] Failed to marshal query: %v", err)
		return "", err
	}

	filter := e.getBaseConds(params)
	query := fmt.Sprintf(
		`{"query": {"bool": {"must": [{"match": {"content": %s}}], "filter": [%s]}}}`,
		string(content), filter,
	)

	log.Debugf("[ElasticsearchV7] Executing keyword search with query: %s", query)
	return query, nil
}

// executeKeywordSearch executes the keyword search query
func (e *elasticsearchRepository) executeKeywordSearch(
	ctx context.Context, query string,
) ([]*typesLocal.IndexWithScore, error) {
	log := logger.GetLogger(ctx)

	response, err := e.client.Search(
		e.client.Search.WithIndex(e.index),
		e.client.Search.WithBody(
			strings.NewReader(query),
		),
		e.client.Search.WithContext(ctx),
	)
	if err != nil {
		log.Errorf("[ElasticsearchV7] Keywords search failed: %v", err)
		return nil, err
	}
	defer response.Body.Close()

	results, err := e.processSearchResponse(ctx, response, typesLocal.KeywordsRetrieverType)
	if err != nil {
		return nil, err
	}

	return results, nil
}

// processSearchResponse Process search response
func (e *elasticsearchRepository) processSearchResponse(ctx context.Context,
	response *esapi.Response, retrieverType typesLocal.RetrieverType,
) ([]*typesLocal.IndexWithScore, error) {
	log := logger.GetLogger(ctx)

	if response.IsError() {
		errMsg := fmt.Sprintf("failed to retrieve: %s", response.String())
		log.Errorf("[ElasticsearchV7] %s", errMsg)
		return nil, errors.New(errMsg)
	}

	// Decode response body
	rJson, err := e.decodeSearchResponse(ctx, response)
	if err != nil {
		return nil, err
	}

	// Extract hits from response
	hitsList, err := e.extractHitsFromResponse(ctx, rJson)
	if err != nil {
		return nil, err
	}

	// Process hits into results
	results, err := e.processHits(ctx, hitsList, retrieverType)
	if err != nil {
		return nil, err
	}

	// Log results summary
	e.logResultsSummary(ctx, results, retrieverType)

	return results, nil
}

// decodeSearchResponse decodes the search response body
func (e *elasticsearchRepository) decodeSearchResponse(ctx context.Context,
	response *esapi.Response,
) (map[string]any, error) {
	log := logger.GetLogger(ctx)
	var rJson map[string]any

	if err := json.NewDecoder(response.Body).Decode(&rJson); err != nil {
		log.Errorf("[ElasticsearchV7] Failed to decode search response: %v", err)
		return nil, err
	}

	return rJson, nil
}

// extractHitsFromResponse extracts the hits list from the response JSON
func (e *elasticsearchRepository) extractHitsFromResponse(ctx context.Context,
	rJson map[string]any,
) ([]interface{}, error) {
	log := logger.GetLogger(ctx)

	// Extract hits from response
	hitsObj, ok := rJson["hits"].(map[string]interface{})
	if !ok {
		log.Errorf("[ElasticsearchV7] Invalid search response format: 'hits' object missing")
		return nil, fmt.Errorf("invalid search response format")
	}

	hitsList, ok := hitsObj["hits"].([]interface{})
	if !ok {
		log.Warnf("[ElasticsearchV7] No hits found in search response")
		return []interface{}{}, nil
	}

	return hitsList, nil
}

// processHits processes the hits into IndexWithScore results
func (e *elasticsearchRepository) processHits(ctx context.Context,
	hitsList []interface{}, retrieverType typesLocal.RetrieverType,
) ([]*typesLocal.IndexWithScore, error) {
	log := logger.GetLogger(ctx)
	results := make([]*typesLocal.IndexWithScore, 0, len(hitsList))

	for _, hit := range hitsList {
		indexWithScore, err := e.processHit(ctx, hit, retrieverType)
		if err != nil {
			// Log error but continue processing other hits
			log.Warnf("[ElasticsearchV7] Error processing hit: %v", err)
			continue
		}

		if indexWithScore != nil {
			results = append(results, indexWithScore)
		}
	}

	return results, nil
}

// processHit processes a single hit into an IndexWithScore
func (e *elasticsearchRepository) processHit(ctx context.Context,
	hit interface{}, retrieverType typesLocal.RetrieverType,
) (*typesLocal.IndexWithScore, error) {
	log := logger.GetLogger(ctx)

	hitMap, ok := hit.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid hit object format")
	}

	// Get document ID
	docID, ok := hitMap["_id"].(string)
	if !ok {
		return nil, fmt.Errorf("hit missing document ID")
	}

	// Get document source
	sourceObj, ok := hitMap["_source"]
	if !ok {
		return nil, fmt.Errorf("hit %s missing _source", docID)
	}

	// Get score
	score, ok := hitMap["_score"].(float64)
	if !ok {
		return nil, fmt.Errorf("hit %s missing score", docID)
	}

	// Convert source to embedding
	embedding, err := e.convertSourceToEmbedding(ctx, sourceObj, docID, score)
	if err != nil {
		return nil, err
	}

	result := elasticsearchRetriever.FromDBVectorEmbeddingWithScore(
		docID, embedding, typesLocal.MatchTypeKeywords,
	)

	matchType := "keyword"
	if retrieverType == typesLocal.VectorRetrieverType {
		matchType = "vector"
	}
	log.Debugf("[ElasticsearchV7] %s search result: id=%s, score=%.4f", matchType, docID, score)

	return result, nil
}

// convertSourceToEmbedding converts the source object to an embedding
func (e *elasticsearchRepository) convertSourceToEmbedding(ctx context.Context,
	sourceObj interface{}, docID string, score float64,
) (*elasticsearchRetriever.VectorEmbeddingWithScore, error) {
	log := logger.GetLogger(ctx)

	// Convert source to embedding
	var embedding *elasticsearchRetriever.VectorEmbeddingWithScore
	sourceBytes, err := json.Marshal(sourceObj)
	if err != nil {
		log.Warnf("[ElasticsearchV7] Failed to marshal source for hit %s: %v", docID, err)
		return nil, fmt.Errorf("failed to marshal source for hit %s: %v", docID, err)
	}

	if err := json.Unmarshal(sourceBytes, &embedding); err != nil {
		log.Warnf("[ElasticsearchV7] Failed to unmarshal source for hit %s: %v", docID, err)
		return nil, fmt.Errorf("failed to unmarshal source for hit %s: %v", docID, err)
	}

	embedding.Score = score
	return embedding, nil
}

// logResultsSummary logs a summary of the results
func (e *elasticsearchRepository) logResultsSummary(ctx context.Context,
	results []*typesLocal.IndexWithScore, retrieverType typesLocal.RetrieverType,
) {
	log := logger.GetLogger(ctx)

	if len(results) == 0 {
		if retrieverType == typesLocal.KeywordsRetrieverType {
			log.Warnf("[ElasticsearchV7] No keyword matches found")
		} else {
			log.Warnf("[ElasticsearchV7] No vector matches found that meet threshold")
		}
	} else {
		retrievalType := "Keywords"
		if retrieverType == typesLocal.VectorRetrieverType {
			retrievalType = "Vector"
		}
		log.Infof("[ElasticsearchV7] %s retrieval found %d results", retrievalType, len(results))
		log.Debugf("[ElasticsearchV7] Top result score: %.4f", results[0].Score)
	}
}

// CopyIndices Copy index data
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
		"[ElasticsearchV7] Copying indices from source knowledge base %s to target knowledge base %s, count: %d",
		sourceKnowledgeBaseID, targetKnowledgeBaseID, len(sourceToTargetChunkIDMap),
	)

	if len(sourceToTargetChunkIDMap) == 0 {
		log.Warnf("[ElasticsearchV7] Empty mapping, skipping copy")
		return nil
	}

	// Build query parameters
	retrieveParams := typesLocal.RetrieveParams{
		KnowledgeBaseIDs: []string{sourceKnowledgeBaseID},
	}

	// Set batch processing parameters
	batchSize := 500
	from := 0
	totalCopied := 0

	for {
		// Query source data batch
		hitsList, err := e.querySourceBatch(ctx, retrieveParams, from, batchSize)
		if err != nil {
			return err
		}

		// If no more data, break the loop
		if len(hitsList) == 0 {
			break
		}

		log.Infof("[ElasticsearchV7] Found %d source index data, batch start position: %d", len(hitsList), from)

		// Process the batch and create index information
		indexInfoList, err := e.processSourceBatch(ctx, hitsList, sourceToTargetKBIDMap,
			sourceToTargetChunkIDMap, targetKnowledgeBaseID)
		if err != nil {
			return err
		}

		// Save processed indices
		if len(indexInfoList) > 0 {
			err := e.saveCopiedIndices(ctx, indexInfoList)
			if err != nil {
				return err
			}

			totalCopied += len(indexInfoList)
			log.Infof("[ElasticsearchV7] Successfully copied batch data, batch size: %d, total copied: %d",
				len(indexInfoList), totalCopied)
		}

		// Move to next batch
		from += len(hitsList)

		// If the number of returned records is less than the request size, it means the last page has been reached
		if len(hitsList) < batchSize {
			break
		}
	}

	log.Infof("[ElasticsearchV7] Index copy completed, total copied: %d", totalCopied)
	return nil
}

// querySourceBatch queries a batch of source data
func (e *elasticsearchRepository) querySourceBatch(ctx context.Context,
	retrieveParams typesLocal.RetrieveParams, from int, batchSize int,
) ([]interface{}, error) {
	log := logger.GetLogger(ctx)

	// Build query request safely
	filterJSON := e.getBaseConds(retrieveParams)
	var filter map[string]interface{}
	if err := json.Unmarshal([]byte(filterJSON), &filter); err != nil {
		log.Errorf("[ElasticsearchV7] Failed to parse base conditions: %v", err)
		filter = map[string]interface{}{}
	}

	queryBody := map[string]interface{}{
		"query": filter,
		"from":  from,
		"size":  batchSize,
	}

	queryBytes, err := json.Marshal(queryBody)
	if err != nil {
		log.Errorf("[ElasticsearchV7] Failed to marshal query body: %v", err)
		return nil, err
	}

	// Execute query
	response, err := e.client.Search(
		e.client.Search.WithIndex(e.index),
		e.client.Search.WithBody(strings.NewReader(string(queryBytes))),
		e.client.Search.WithContext(ctx),
	)
	if err != nil {
		log.Errorf("[ElasticsearchV7] Failed to query source index data: %v", err)
		return nil, err
	}
	defer response.Body.Close()

	if response.IsError() {
		log.Errorf("[ElasticsearchV7] Failed to query source index data: %s", response.String())
		return nil, fmt.Errorf("failed to query source index data: %s", response.String())
	}

	// 解析搜索结果
	var searchResult map[string]interface{}
	if err := json.NewDecoder(response.Body).Decode(&searchResult); err != nil {
		log.Errorf("[ElasticsearchV7] Failed to parse query result: %v", err)
		return nil, err
	}

	// 提取结果列表
	hitsObj, ok := searchResult["hits"].(map[string]interface{})
	if !ok {
		log.Errorf("[ElasticsearchV7] Invalid search result format: 'hits' object missing")
		return nil, fmt.Errorf("invalid search result format")
	}

	hitsList, ok := hitsObj["hits"].([]interface{})
	if !ok || len(hitsList) == 0 {
		if from == 0 {
			log.Warnf("[ElasticsearchV7] No source index data found")
		}
		return []interface{}{}, nil
	}

	return hitsList, nil
}

// processSourceBatch processes a batch of source data and creates index information
func (e *elasticsearchRepository) processSourceBatch(ctx context.Context,
	hitsList []interface{},
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
) ([]*typesLocal.IndexInfo, error) {
	log := logger.GetLogger(ctx)

	// Prepare index information for batch save
	indexInfoList := make([]*typesLocal.IndexInfo, 0, len(hitsList))
	embeddingMap := make(map[string][]float32)

	// Process each hit result
	for _, hit := range hitsList {
		indexInfo, embeddingVector, err := e.processSingleHit(ctx, hit,
			sourceToTargetKBIDMap, sourceToTargetChunkIDMap, targetKnowledgeBaseID)
		if err != nil {
			log.Warnf("[ElasticsearchV7] Error processing hit: %v", err)
			continue
		}

		if indexInfo != nil {
			indexInfoList = append(indexInfoList, indexInfo)
			if embeddingVector != nil {
				embeddingMap[indexInfo.ChunkID] = embeddingVector
			}
		}
	}

	return indexInfoList, nil
}

// processSingleHit processes a single hit and creates index information
func (e *elasticsearchRepository) processSingleHit(ctx context.Context,
	hit interface{},
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
) (*typesLocal.IndexInfo, []float32, error) {
	log := logger.GetLogger(ctx)

	hitMap, ok := hit.(map[string]interface{})
	if !ok {
		log.Warnf("[ElasticsearchV7] Invalid hit object format")
		return nil, nil, fmt.Errorf("invalid hit object format")
	}

	// Get document source
	sourceObj, ok := hitMap["_source"].(map[string]interface{})
	if !ok {
		log.Warnf("[ElasticsearchV7] Hit missing _source field")
		return nil, nil, fmt.Errorf("hit missing _source field")
	}

	// Get source ChunkID and corresponding target ChunkID
	sourceChunkID, ok := sourceObj["chunk_id"].(string)
	if !ok {
		log.Warnf("[ElasticsearchV7] Source index data missing chunk_id field")
		return nil, nil, fmt.Errorf("source index data missing chunk_id field")
	}

	targetChunkID, ok := sourceToTargetChunkIDMap[sourceChunkID]
	if !ok {
		log.Warnf("[ElasticsearchV7] Source chunk ID %s not found in mapping", sourceChunkID)
		return nil, nil, fmt.Errorf("source chunk ID %s not found in mapping", sourceChunkID)
	}

	// Get mapped target knowledge ID
	sourceKnowledgeID, ok := sourceObj["knowledge_id"].(string)
	if !ok {
		log.Warnf("[ElasticsearchV7] Source index data missing knowledge_id field")
		return nil, nil, fmt.Errorf("source index data missing knowledge_id field")
	}

	targetKnowledgeID, ok := sourceToTargetKBIDMap[sourceKnowledgeID]
	if !ok {
		log.Warnf("[ElasticsearchV7] Source knowledge ID %s not found in mapping", sourceKnowledgeID)
		return nil, nil, fmt.Errorf("source knowledge ID %s not found in mapping", sourceKnowledgeID)
	}

	// Extract basic content
	content, _ := sourceObj["content"].(string)
	originalSourceID, _ := sourceObj["source_id"].(string)
	sourceType := 0
	if st, ok := sourceObj["source_type"].(float64); ok {
		sourceType = int(st)
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

	// Extract embedding vector (if exists)
	var embedding []float32
	if embeddingInterface, ok := sourceObj["embedding"].([]interface{}); ok {
		embedding = make([]float32, len(embeddingInterface))
		for i, v := range embeddingInterface {
			if f, ok := v.(float64); ok {
				embedding[i] = float32(f)
			}
		}
		log.Debugf("[ElasticsearchV7] Extracted embedding vector with %d dimensions for chunk %s",
			len(embedding), targetChunkID)
	}

	// Create IndexInfo object
	indexInfo := &typesLocal.IndexInfo{
		ChunkID:         targetChunkID,
		SourceID:        targetSourceID,
		KnowledgeID:     targetKnowledgeID,
		KnowledgeBaseID: targetKnowledgeBaseID,
		Content:         content,
		SourceType:      typesLocal.SourceType(sourceType),
	}

	return indexInfo, embedding, nil
}

// saveCopiedIndices saves the copied indices
func (e *elasticsearchRepository) saveCopiedIndices(ctx context.Context, indexInfoList []*typesLocal.IndexInfo) error {
	log := logger.GetLogger(ctx)

	if len(indexInfoList) == 0 {
		log.Info("[ElasticsearchV7] No indices to save, skipping")
		return nil
	}

	// Prepare additional params with embedding map
	additionalParams := make(map[string]any)
	embeddingMap := make(map[string][]float32)

	// No need to extract embeddings from metadata as they're not stored there
	// We'll use the embeddings directly from the embedding map created in processSourceBatch

	if len(embeddingMap) > 0 {
		additionalParams["embedding"] = embeddingMap
		log.Infof("[ElasticsearchV7] Found %d embeddings to save", len(embeddingMap))
	}

	// Perform batch save
	err := e.BatchSave(ctx, indexInfoList, additionalParams)
	if err != nil {
		log.Errorf("[ElasticsearchV7] Failed to batch save copied indices: %v", err)
		return err
	}

	log.Infof("[ElasticsearchV7] Successfully saved %d indices", len(indexInfoList))
	return nil
}

// BatchUpdateChunkEnabledStatus updates the enabled status of chunks in batch
func (e *elasticsearchRepository) BatchUpdateChunkEnabledStatus(
	ctx context.Context,
	chunkStatusMap map[string]bool,
) error {
	log := logger.GetLogger(ctx)
	if len(chunkStatusMap) == 0 {
		log.Warnf("[ElasticsearchV7] Chunk status map is empty, skipping update")
		return nil
	}

	log.Infof("[ElasticsearchV7] Batch updating chunk enabled status, count: %d", len(chunkStatusMap))

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
		query := map[string]interface{}{
			"query": map[string]interface{}{
				"terms": map[string]interface{}{
					"chunk_id.keyword": enabledChunkIDs,
				},
			},
			"script": map[string]interface{}{
				"source": "ctx._source.is_enabled = true",
				"lang":   "painless",
			},
		}
		queryJSON, _ := json.Marshal(query)
		res, err := esapi.UpdateByQueryRequest{
			Index: []string{e.index},
			Body:  strings.NewReader(string(queryJSON)),
		}.Do(ctx, e.client)
		if err != nil {
			log.Errorf("[ElasticsearchV7] Failed to update enabled chunks: %v", err)
			return err
		}
		defer res.Body.Close()
		if res.IsError() {
			var e map[string]interface{}
			if err := json.NewDecoder(res.Body).Decode(&e); err != nil {
				log.Errorf("[ElasticsearchV7] Error parsing the response body: %v", err)
			} else {
				log.Errorf("[ElasticsearchV7] Error updating enabled chunks: %v", e["error"])
			}
			return fmt.Errorf("elasticsearch update_by_query failed with status: %d", res.StatusCode)
		}
		log.Infof("[ElasticsearchV7] Updated %d chunks to enabled", len(enabledChunkIDs))
	}

	// Batch update disabled chunks using update_by_query
	if len(disabledChunkIDs) > 0 {
		query := map[string]interface{}{
			"query": map[string]interface{}{
				"terms": map[string]interface{}{
					"chunk_id.keyword": disabledChunkIDs,
				},
			},
			"script": map[string]interface{}{
				"source": "ctx._source.is_enabled = false",
				"lang":   "painless",
			},
		}
		queryJSON, _ := json.Marshal(query)
		res, err := esapi.UpdateByQueryRequest{
			Index: []string{e.index},
			Body:  strings.NewReader(string(queryJSON)),
		}.Do(ctx, e.client)
		if err != nil {
			log.Errorf("[ElasticsearchV7] Failed to update disabled chunks: %v", err)
			return err
		}
		defer res.Body.Close()
		if res.IsError() {
			var e map[string]interface{}
			if err := json.NewDecoder(res.Body).Decode(&e); err != nil {
				log.Errorf("[ElasticsearchV7] Error parsing the response body: %v", err)
			} else {
				log.Errorf("[ElasticsearchV7] Error updating disabled chunks: %v", e["error"])
			}
			return fmt.Errorf("elasticsearch update_by_query failed with status: %d", res.StatusCode)
		}
		log.Infof("[ElasticsearchV7] Updated %d chunks to disabled", len(disabledChunkIDs))
	}

	log.Infof("[ElasticsearchV7] Successfully batch updated chunk enabled status")
	return nil
}

// BatchUpdateChunkTagID updates the tag ID of chunks in batch
func (e *elasticsearchRepository) BatchUpdateChunkTagID(
	ctx context.Context,
	chunkTagMap map[string]string,
) error {
	log := logger.GetLogger(ctx)
	if len(chunkTagMap) == 0 {
		log.Warnf("[ElasticsearchV7] Chunk tag map is empty, skipping update")
		return nil
	}

	log.Infof("[ElasticsearchV7] Batch updating chunk tag ID, count: %d", len(chunkTagMap))

	// Group chunks by tag ID for batch updates
	tagGroups := make(map[string][]string)
	for chunkID, tagID := range chunkTagMap {
		tagGroups[tagID] = append(tagGroups[tagID], chunkID)
	}

	// Batch update chunks for each tag ID using update_by_query
	for tagID, chunkIDs := range tagGroups {
		query := map[string]interface{}{
			"query": map[string]interface{}{
				"terms": map[string]interface{}{
					"chunk_id.keyword": chunkIDs,
				},
			},
			"script": map[string]interface{}{
				"source": "ctx._source.tag_id = params.tag_id",
				"lang":   "painless",
				"params": map[string]interface{}{
					"tag_id": tagID,
				},
			},
		}
		queryJSON, _ := json.Marshal(query)
		res, err := esapi.UpdateByQueryRequest{
			Index: []string{e.index},
			Body:  strings.NewReader(string(queryJSON)),
		}.Do(ctx, e.client)
		if err != nil {
			log.Errorf("[ElasticsearchV7] Failed to update chunks with tag_id %s: %v", tagID, err)
			return err
		}
		defer res.Body.Close()
		if res.IsError() {
			var e map[string]interface{}
			if err := json.NewDecoder(res.Body).Decode(&e); err != nil {
				log.Errorf("[ElasticsearchV7] Error parsing the response body: %v", err)
			} else {
				log.Errorf("[ElasticsearchV7] Error updating chunks with tag_id: %v", e["error"])
			}
			return fmt.Errorf("elasticsearch update_by_query failed with status: %d", res.StatusCode)
		}
		log.Infof("[ElasticsearchV7] Updated %d chunks to tag_id=%s", len(chunkIDs), tagID)
	}

	log.Infof("[ElasticsearchV7] Successfully batch updated chunk tag ID")
	return nil
}
