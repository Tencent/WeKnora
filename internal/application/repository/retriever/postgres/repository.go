package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// pgRepository implements PostgreSQL-based retrieval operations
type pgRepository struct {
	db *gorm.DB // Database connection
}

// NewPostgresRetrieveEngineRepository creates a new PostgreSQL retriever repository
func NewPostgresRetrieveEngineRepository(db *gorm.DB) interfaces.RetrieveEngineRepository {
	logger.GetLogger(context.Background()).Info("[Postgres] Initializing PostgreSQL retriever engine repository")
	return &pgRepository{db: db}
}

// EngineType returns the retriever engine type (PostgreSQL)
func (r *pgRepository) EngineType() types.RetrieverEngineType {
	return types.PostgresRetrieverEngineType
}

// Support returns supported retriever types (keywords and vector)
func (r *pgRepository) Support() []types.RetrieverType {
	return []types.RetrieverType{types.KeywordsRetrieverType, types.VectorRetrieverType}
}

// calculateIndexStorageSize calculates storage size for a single index entry
func (g *pgRepository) calculateIndexStorageSize(embeddingDB *pgVector) int64 {
	// 1. Text content size
	contentSizeBytes := int64(len(embeddingDB.Content))

	// 2. Vector storage size (2 bytes per dimension for half-precision float)
	var vectorSizeBytes int64 = 0
	if embeddingDB.Dimension > 0 {
		vectorSizeBytes = int64(embeddingDB.Dimension * 2)
	}

	// 3. Metadata size (fixed overhead for IDs, timestamps etc.)
	metadataSizeBytes := int64(200)

	// 4. Index overhead (HNSW index is ~2x vector size)
	indexOverheadBytes := vectorSizeBytes * 2

	// Total size in bytes
	totalSizeBytes := contentSizeBytes + vectorSizeBytes + metadataSizeBytes + indexOverheadBytes

	return totalSizeBytes
}

// EstimateStorageSize estimates total storage size for multiple indices
func (g *pgRepository) EstimateStorageSize(
	ctx context.Context, indexInfoList []*types.IndexInfo, additionalParams map[string]any,
) int64 {
	var totalStorageSize int64 = 0
	for _, indexInfo := range indexInfoList {
		embeddingDB := toDBVectorEmbedding(indexInfo, additionalParams)
		totalStorageSize += g.calculateIndexStorageSize(embeddingDB)
	}
	logger.GetLogger(ctx).Infof(
		"[Postgres] Estimated storage size for %d indices: %d bytes",
		len(indexInfoList), totalStorageSize,
	)
	return totalStorageSize
}

// Save stores a single index entry
func (g *pgRepository) Save(ctx context.Context, indexInfo *types.IndexInfo, additionalParams map[string]any) error {
	logger.GetLogger(ctx).Debugf("[Postgres] Saving index for source ID: %s", indexInfo.SourceID)
	embeddingDB := toDBVectorEmbedding(indexInfo, additionalParams)
	err := g.db.WithContext(ctx).Create(embeddingDB).Error
	if err != nil {
		logger.GetLogger(ctx).Errorf("[Postgres] Failed to save index: %v", err)
		return err
	}
	logger.GetLogger(ctx).Infof("[Postgres] Successfully saved index for source ID: %s", indexInfo.SourceID)
	return nil
}

// BatchSave stores multiple index entries in batch
func (g *pgRepository) BatchSave(
	ctx context.Context, indexInfoList []*types.IndexInfo, additionalParams map[string]any,
) error {
	logger.GetLogger(ctx).Infof("[Postgres] Batch saving %d indices", len(indexInfoList))
	indexInfoDBList := make([]*pgVector, len(indexInfoList))
	for i := range indexInfoList {
		indexInfoDBList[i] = toDBVectorEmbedding(indexInfoList[i], additionalParams)
	}
	err := g.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(indexInfoDBList).Error
	if err != nil {
		logger.GetLogger(ctx).Errorf("[Postgres] Batch save failed: %v", err)
		return err
	}
	logger.GetLogger(ctx).Infof("[Postgres] Successfully batch saved %d indices", len(indexInfoList))
	return nil
}

// DeleteByChunkIDList deletes indices by chunk IDs
func (g *pgRepository) DeleteByChunkIDList(ctx context.Context, chunkIDList []string, dimension int, knowledgeType string) error {
	logger.GetLogger(ctx).Infof("[Postgres] Deleting indices by chunk IDs, count: %d", len(chunkIDList))
	result := g.db.WithContext(ctx).Where("chunk_id IN ?", chunkIDList).Delete(&pgVector{})
	if result.Error != nil {
		logger.GetLogger(ctx).Errorf("[Postgres] Failed to delete indices by chunk IDs: %v", result.Error)
		return result.Error
	}
	logger.GetLogger(ctx).Infof("[Postgres] Successfully deleted %d indices by chunk IDs", result.RowsAffected)
	return nil
}

// DeleteBySourceIDList deletes indices by source IDs
func (g *pgRepository) DeleteBySourceIDList(ctx context.Context, sourceIDList []string, dimension int, knowledgeType string) error {
	if len(sourceIDList) == 0 {
		return nil
	}
	logger.GetLogger(ctx).Infof("[Postgres] Deleting indices by source IDs, count: %d", len(sourceIDList))
	result := g.db.WithContext(ctx).Where("source_id IN ?", sourceIDList).Delete(&pgVector{})
	if result.Error != nil {
		logger.GetLogger(ctx).Errorf("[Postgres] Failed to delete indices by source IDs: %v", result.Error)
		return result.Error
	}
	logger.GetLogger(ctx).Infof("[Postgres] Successfully deleted %d indices by source IDs", result.RowsAffected)
	return nil
}

// DeleteByKnowledgeIDList deletes indices by knowledge IDs
func (g *pgRepository) DeleteByKnowledgeIDList(ctx context.Context, knowledgeIDList []string, dimension int, knowledgeType string) error {
	logger.GetLogger(ctx).Infof("[Postgres] Deleting indices by knowledge IDs, count: %d", len(knowledgeIDList))
	result := g.db.WithContext(ctx).Where("knowledge_id IN ?", knowledgeIDList).Delete(&pgVector{})
	if result.Error != nil {
		logger.GetLogger(ctx).Errorf("[Postgres] Failed to delete indices by knowledge IDs: %v", result.Error)
		return result.Error
	}
	logger.GetLogger(ctx).Infof("[Postgres] Successfully deleted %d indices by knowledge IDs", result.RowsAffected)
	return nil
}

// Retrieve handles retrieval requests and routes to appropriate method
func (g *pgRepository) Retrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	logger.GetLogger(ctx).Debugf("[Postgres] Processing retrieval request of type: %s", params.RetrieverType)
	switch params.RetrieverType {
	case types.KeywordsRetrieverType:
		return g.KeywordsRetrieve(ctx, params)
	case types.VectorRetrieverType:
		return g.VectorRetrieve(ctx, params)
	}
	err := errors.New("invalid retriever type")
	logger.GetLogger(ctx).Errorf("[Postgres] %v: %s", err, params.RetrieverType)
	return nil, err
}

// KeywordsRetrieve performs keyword-based search using PostgreSQL full-text search
func (g *pgRepository) KeywordsRetrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	logger.GetLogger(ctx).Infof("[Postgres] Keywords retrieval: query=%s, topK=%d", params.Query, params.TopK)
	conds := make([]clause.Expression, 0)

	// KnowledgeBaseIDs and KnowledgeIDs use AND logic
	// - If only KnowledgeBaseIDs: search entire knowledge bases
	// - If only KnowledgeIDs: search specific documents
	// - If both: search specific documents within the knowledge bases (AND)
	if len(params.KnowledgeBaseIDs) > 0 {
		logger.GetLogger(ctx).Debugf("[Postgres] Filtering by knowledge base IDs: %v", params.KnowledgeBaseIDs)
		conds = append(conds, clause.IN{
			Column: "knowledge_base_id",
			Values: common.ToInterfaceSlice(params.KnowledgeBaseIDs),
		})
	}
	if len(params.KnowledgeIDs) > 0 {
		logger.GetLogger(ctx).Debugf("[Postgres] Filtering by knowledge IDs: %v", params.KnowledgeIDs)
		conds = append(conds, clause.IN{
			Column: "knowledge_id",
			Values: common.ToInterfaceSlice(params.KnowledgeIDs),
		})
	}
	// Filter by tag IDs if specified
	if len(params.TagIDs) > 0 {
		logger.GetLogger(ctx).Debugf("[Postgres] Filtering by tag IDs: %v", params.TagIDs)
		conds = append(conds, clause.IN{
			Column: "tag_id",
			Values: common.ToInterfaceSlice(params.TagIDs),
		})
	}
	conds = append(conds, clause.Expr{
		SQL:  "id @@@ paradedb.match(field => 'content', value => ?, distance => 1)",
		Vars: []interface{}{params.Query},
	})
	// Filter by is_enabled = true or NULL (NULL means enabled for historical data)
	conds = append(conds, clause.Expr{
		SQL:  "(is_enabled IS NULL OR is_enabled = ?)",
		Vars: []interface{}{true},
	})
	conds = append(conds, clause.OrderBy{Columns: []clause.OrderByColumn{
		{Column: clause.Column{Name: "score"}, Desc: true},
	}})

	var embeddingDBList []pgVectorWithScore
	err := g.db.WithContext(ctx).Clauses(conds...).Debug().
		Select([]string{
			"paradedb.score(id) as score",
			"id",
			"content",
			"source_id",
			"source_type",
			"chunk_id",
			"knowledge_id",
			"knowledge_base_id",
			"tag_id",
		}).
		Limit(int(params.TopK)).
		Find(&embeddingDBList).Error

	if err == gorm.ErrRecordNotFound {
		logger.GetLogger(ctx).Warnf("[Postgres] No records found for keywords query: %s", params.Query)
		return nil, nil
	}
	if err != nil {
		logger.GetLogger(ctx).Errorf("[Postgres] Keywords retrieval failed: %v", err)
		return nil, err
	}

	logger.GetLogger(ctx).Infof("[Postgres] Keywords retrieval found %d results", len(embeddingDBList))
	results := make([]*types.IndexWithScore, len(embeddingDBList))
	for i := range embeddingDBList {
		results[i] = fromDBVectorEmbeddingWithScore(&embeddingDBList[i], types.MatchTypeKeywords)
		logger.GetLogger(ctx).Debugf("[Postgres] Keywords result %d: chunk=%s, score=%f",
			i, results[i].ChunkID, results[i].Score)
	}
	return []*types.RetrieveResult{
		{
			Results:             results,
			RetrieverEngineType: types.PostgresRetrieverEngineType,
			RetrieverType:       types.KeywordsRetrieverType,
			Error:               nil,
		},
	}, nil
}

// VectorRetrieve performs vector similarity search using pgvector
// Optimized to use HNSW index efficiently and avoid recalculating vector distance
func (g *pgRepository) VectorRetrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	logger.GetLogger(ctx).Infof("[Postgres] Vector retrieval: dim=%d, topK=%d, threshold=%.4f",
		len(params.Embedding), params.TopK, params.Threshold)

	dimension := len(params.Embedding)
	queryVector := pgvector.NewHalfVector(params.Embedding)

	// Build WHERE conditions for filtering
	whereParts := make([]string, 0)
	allVars := make([]interface{}, 0)

	// Add query vector first (used in ORDER BY for HNSW index)
	allVars = append(allVars, queryVector)

	// Dimension filter (required for HNSW index WHERE clause)
	whereParts = append(whereParts, fmt.Sprintf("dimension = $%d", len(allVars)+1))
	allVars = append(allVars, dimension)

	// KnowledgeBaseIDs and KnowledgeIDs use AND logic
	// - If only KnowledgeBaseIDs: search entire knowledge bases
	// - If only KnowledgeIDs: search specific documents
	// - If both: search specific documents within the knowledge bases (AND)
	if len(params.KnowledgeBaseIDs) > 0 {
		logger.GetLogger(ctx).Debugf(
			"[Postgres] Filtering vector search by knowledge base IDs: %v",
			params.KnowledgeBaseIDs,
		)
		placeholders := make([]string, len(params.KnowledgeBaseIDs))
		paramStart := len(allVars) + 1
		for i := range params.KnowledgeBaseIDs {
			placeholders[i] = fmt.Sprintf("$%d", paramStart+i)
			allVars = append(allVars, params.KnowledgeBaseIDs[i])
		}
		whereParts = append(whereParts, fmt.Sprintf("knowledge_base_id IN (%s)",
			strings.Join(placeholders, ", ")))
	}
	if len(params.KnowledgeIDs) > 0 {
		logger.GetLogger(ctx).Debugf(
			"[Postgres] Filtering vector search by knowledge IDs: %v",
			params.KnowledgeIDs,
		)
		placeholders := make([]string, len(params.KnowledgeIDs))
		paramStart := len(allVars) + 1
		for i := range params.KnowledgeIDs {
			placeholders[i] = fmt.Sprintf("$%d", paramStart+i)
			allVars = append(allVars, params.KnowledgeIDs[i])
		}
		whereParts = append(whereParts, fmt.Sprintf("knowledge_id IN (%s)",
			strings.Join(placeholders, ", ")))
	}
	// Filter by tag IDs if specified
	if len(params.TagIDs) > 0 {
		logger.GetLogger(ctx).Debugf(
			"[Postgres] Filtering vector search by tag IDs: %v",
			params.TagIDs,
		)
		placeholders := make([]string, len(params.TagIDs))
		paramStart := len(allVars) + 1
		for i := range params.TagIDs {
			placeholders[i] = fmt.Sprintf("$%d", paramStart+i)
			allVars = append(allVars, params.TagIDs[i])
		}
		whereParts = append(whereParts, fmt.Sprintf("tag_id IN (%s)",
			strings.Join(placeholders, ", ")))
	}

	// is_enabled filter
	whereParts = append(whereParts, fmt.Sprintf("(is_enabled IS NULL OR is_enabled = $%d)", len(allVars)+1))
	allVars = append(allVars, true)

	// Build WHERE clause string
	whereClause := ""
	if len(whereParts) > 0 {
		whereClause = "WHERE " + strings.Join(whereParts, " AND ")
	}

	// Expand TopK to get more candidates before threshold filtering
	expandedTopK := params.TopK * 2
	if expandedTopK < 100 {
		expandedTopK = 100 // Minimum 100 candidates
	}
	if expandedTopK > 1000 {
		expandedTopK = 1000 // Maximum 1000 candidates
	}

	// Optimized query: Use subquery to calculate distance once
	// Strategy: Use ORDER BY with vector distance to leverage HNSW index,
	// then filter by threshold in outer query
	// This allows PostgreSQL to use HNSW index efficiently
	subqueryLimitParam := len(allVars) + 1
	thresholdParam := len(allVars) + 2
	finalLimitParam := len(allVars) + 3

	querySQL := fmt.Sprintf(`
		SELECT 
			id, content, source_id, source_type, chunk_id, knowledge_id, knowledge_base_id, tag_id,
			(1 - distance) as score
		FROM (
			SELECT 
				id, content, source_id, source_type, chunk_id, knowledge_id, knowledge_base_id, tag_id,
				embedding::halfvec(%d) <=> $1::halfvec as distance
			FROM embeddings
			%s
			ORDER BY embedding::halfvec(%d) <=> $1::halfvec
			LIMIT $%d
		) AS candidates
		WHERE distance <= $%d
		ORDER BY distance ASC
		LIMIT $%d
	`, dimension, whereClause, dimension, subqueryLimitParam, thresholdParam, finalLimitParam)

	allVars = append(allVars, expandedTopK)       // LIMIT in subquery
	allVars = append(allVars, 1-params.Threshold) // Distance threshold
	allVars = append(allVars, params.TopK)        // Final LIMIT

	var embeddingDBList []pgVectorWithScore

	err := g.db.WithContext(ctx).Raw(querySQL, allVars...).Scan(&embeddingDBList).Error

	if err == gorm.ErrRecordNotFound {
		logger.GetLogger(ctx).Warnf("[Postgres] No vector matches found that meet threshold %.4f", params.Threshold)
		return nil, nil
	}
	if err != nil {
		logger.GetLogger(ctx).Errorf("[Postgres] Vector retrieval failed: %v", err)
		return nil, err
	}

	// Apply final TopK limit (in case we got more results than needed)
	if len(embeddingDBList) > int(params.TopK) {
		embeddingDBList = embeddingDBList[:params.TopK]
	}

	logger.GetLogger(ctx).Infof("[Postgres] Vector retrieval found %d results", len(embeddingDBList))
	results := make([]*types.IndexWithScore, len(embeddingDBList))
	for i := range embeddingDBList {
		results[i] = fromDBVectorEmbeddingWithScore(&embeddingDBList[i], types.MatchTypeEmbedding)
		logger.GetLogger(ctx).Debugf("[Postgres] Vector search result %d: chunk_id %s, score %.4f",
			i, results[i].ChunkID, results[i].Score)
	}
	return []*types.RetrieveResult{
		{
			Results:             results,
			RetrieverEngineType: types.PostgresRetrieverEngineType,
			RetrieverType:       types.VectorRetrieverType,
			Error:               nil,
		},
	}, nil
}

// CopyIndices copies index data
func (g *pgRepository) CopyIndices(ctx context.Context,
	sourceKnowledgeBaseID string,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
	dimension int,
	knowledgeType string,
) error {
	logger.GetLogger(ctx).Infof(
		"[Postgres] Copying indices, source knowledge base: %s, target knowledge base: %s, mapping count: %d",
		sourceKnowledgeBaseID, targetKnowledgeBaseID, len(sourceToTargetChunkIDMap),
	)

	if len(sourceToTargetChunkIDMap) == 0 {
		logger.GetLogger(ctx).Warnf("[Postgres] Mapping is empty, no need to copy")
		return nil
	}

	// Batch processing parameters
	batchSize := 500 // Number of records to process per batch
	offset := 0      // Offset for pagination
	totalCopied := 0 // Total number of copied records

	for {
		// Paginated query for source data
		var sourceVectors []*pgVector
		if err := g.db.WithContext(ctx).
			Where("knowledge_base_id = ?", sourceKnowledgeBaseID).
			Limit(batchSize).
			Offset(offset).
			Find(&sourceVectors).Error; err != nil {
			logger.GetLogger(ctx).Errorf("[Postgres] Failed to query source index data: %v", err)
			return err
		}

		// If no more data, exit the loop
		if len(sourceVectors) == 0 {
			if offset == 0 {
				logger.GetLogger(ctx).Warnf("[Postgres] No source index data found")
			}
			break
		}

		batchCount := len(sourceVectors)
		logger.GetLogger(ctx).Infof(
			"[Postgres] Found %d source index data, batch start position: %d",
			batchCount, offset,
		)

		// Create target vector index
		targetVectors := make([]*pgVector, 0, batchCount)
		for _, sourceVector := range sourceVectors {
			// Get the mapped target chunk ID
			targetChunkID, ok := sourceToTargetChunkIDMap[sourceVector.ChunkID]
			if !ok {
				logger.GetLogger(ctx).Warnf(
					"[Postgres] Source chunk %s not found in target chunk mapping, skipping",
					sourceVector.ChunkID,
				)
				continue
			}

			// Get the mapped target knowledge ID
			targetKnowledgeID, ok := sourceToTargetKBIDMap[sourceVector.KnowledgeID]
			if !ok {
				logger.GetLogger(ctx).Warnf(
					"[Postgres] Source knowledge %s not found in target knowledge mapping, skipping",
					sourceVector.KnowledgeID,
				)
				continue
			}

			// Handle SourceID transformation for generated questions
			// Generated questions have SourceID format: {chunkID}-{questionID}
			// Regular chunks have SourceID == ChunkID
			var targetSourceID string
			if sourceVector.SourceID == sourceVector.ChunkID {
				// Regular chunk, use targetChunkID as SourceID
				targetSourceID = targetChunkID
			} else if strings.HasPrefix(sourceVector.SourceID, sourceVector.ChunkID+"-") {
				// This is a generated question, preserve the questionID part
				questionID := strings.TrimPrefix(sourceVector.SourceID, sourceVector.ChunkID+"-")
				targetSourceID = fmt.Sprintf("%s-%s", targetChunkID, questionID)
			} else {
				// For other complex scenarios, generate new unique SourceID
				targetSourceID = uuid.New().String()
			}

			// Create new vector index, copy the content and vector of the source index
			targetVector := &pgVector{
				Content:         sourceVector.Content,
				SourceID:        targetSourceID,        // Handle SourceID transformation properly
				SourceType:      sourceVector.SourceType,
				ChunkID:         targetChunkID,         // Update to target chunk ID
				KnowledgeID:     targetKnowledgeID,     // Update to target knowledge ID
				KnowledgeBaseID: targetKnowledgeBaseID, // Update to target knowledge base ID
				Dimension:       sourceVector.Dimension,
				Embedding:       sourceVector.Embedding, // Copy the vector embedding directly, avoid recalculation
			}

			targetVectors = append(targetVectors, targetVector)
		}

		// Batch insert target vector index
		if len(targetVectors) > 0 {
			if err := g.db.WithContext(ctx).
				Clauses(clause.OnConflict{DoNothing: true}).Create(targetVectors).Error; err != nil {
				logger.GetLogger(ctx).Errorf("[Postgres] Failed to batch create target index: %v", err)
				return err
			}

			totalCopied += len(targetVectors)
			logger.GetLogger(ctx).Infof(
				"[Postgres] Successfully copied batch data, batch size: %d, total copied: %d",
				len(targetVectors),
				totalCopied,
			)
		}

		// Move to the next batch
		offset += batchCount

		// If the number of returned records is less than the requested size, it means the last page has been reached
		if batchCount < batchSize {
			break
		}
	}

	logger.GetLogger(ctx).Infof("[Postgres] Index copying completed, total copied: %d", totalCopied)
	return nil
}

// BatchUpdateChunkEnabledStatus updates the enabled status of chunks in batch
func (g *pgRepository) BatchUpdateChunkEnabledStatus(ctx context.Context, chunkStatusMap map[string]bool) error {
	if len(chunkStatusMap) == 0 {
		logger.GetLogger(ctx).Warnf("[Postgres] Chunk status map is empty, skipping update")
		return nil
	}

	logger.GetLogger(ctx).Infof("[Postgres] Batch updating chunk enabled status, count: %d", len(chunkStatusMap))

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

	// Batch update enabled chunks
	if len(enabledChunkIDs) > 0 {
		result := g.db.WithContext(ctx).Model(&pgVector{}).
			Where("chunk_id IN ?", enabledChunkIDs).
			Update("is_enabled", true)
		if result.Error != nil {
			logger.GetLogger(ctx).Errorf("[Postgres] Failed to update enabled chunks: %v", result.Error)
			return result.Error
		}
		logger.GetLogger(ctx).
			Infof("[Postgres] Updated %d chunks to enabled, rows affected: %d", len(enabledChunkIDs), result.RowsAffected)
	}

	// Batch update disabled chunks
	if len(disabledChunkIDs) > 0 {
		result := g.db.WithContext(ctx).Model(&pgVector{}).
			Where("chunk_id IN ?", disabledChunkIDs).
			Update("is_enabled", false)
		if result.Error != nil {
			logger.GetLogger(ctx).Errorf("[Postgres] Failed to update disabled chunks: %v", result.Error)
			return result.Error
		}
		logger.GetLogger(ctx).
			Infof("[Postgres] Updated %d chunks to disabled, rows affected: %d", len(disabledChunkIDs), result.RowsAffected)
	}

	logger.GetLogger(ctx).Infof("[Postgres] Successfully batch updated chunk enabled status")
	return nil
}

// BatchUpdateChunkTagID updates the tag ID of chunks in batch
func (g *pgRepository) BatchUpdateChunkTagID(ctx context.Context, chunkTagMap map[string]string) error {
	if len(chunkTagMap) == 0 {
		logger.GetLogger(ctx).Warnf("[Postgres] Chunk tag map is empty, skipping update")
		return nil
	}

	logger.GetLogger(ctx).Infof("[Postgres] Batch updating chunk tag ID, count: %d", len(chunkTagMap))

	// Group chunks by tag ID for batch updates
	tagGroups := make(map[string][]string)
	for chunkID, tagID := range chunkTagMap {
		tagGroups[tagID] = append(tagGroups[tagID], chunkID)
	}

	// Batch update chunks for each tag ID
	for tagID, chunkIDs := range tagGroups {
		result := g.db.WithContext(ctx).Model(&pgVector{}).
			Where("chunk_id IN ?", chunkIDs).
			Update("tag_id", tagID)
		if result.Error != nil {
			logger.GetLogger(ctx).Errorf("[Postgres] Failed to update chunks with tag_id %s: %v", tagID, result.Error)
			return result.Error
		}
		logger.GetLogger(ctx).
			Infof("[Postgres] Updated %d chunks to tag_id=%s, rows affected: %d", len(chunkIDs), tagID, result.RowsAffected)
	}

	logger.GetLogger(ctx).Infof("[Postgres] Successfully batch updated chunk tag ID")
	return nil
}
