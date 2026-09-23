// Package vastbase implements the vector retrieval backend for Vastbase G100.
//
// Vastbase G100 speaks the PostgreSQL wire protocol and ships native vector
// types (floatvector / halfvector / int8vector) plus its own ANN index type,
// graph_index, so this driver reuses the gorm postgres dialector while
// replacing pgvector/ParadeDB-specific DDL and SQL with Vastbase equivalents:
//
//   - halfvector columns (no extension required — vector support is built in)
//   - per-dimension partial expression indexes:
//     CREATE INDEX ... USING graph_index((embedding::halfvector(N))
//     halfvector_cosine_ops) WITH (m, ef_construction, parallel_workers[,
//     quantizer]) WHERE (dimension = N)
//   - the session GUC hnsw_ef_search (note: underscore, unlike pgvector's
//     hnsw.ef_search) to widen the graph search candidate list
//   - cosine distance via the <=> operator, identical literal syntax
//
// Only vector retrieval is supported; keyword retrieval stays on engines that
// provide a full-text/BM25 facility (postgres+ParadeDB, elasticsearch, ...).
package vastbase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
)

// Config holds Vastbase Graph_Index build parameters.
type Config struct {
	// M is the maximum number of links per graph node (2~100, default 16).
	M int
	// EFConstruction is the candidate list size while building (4~1000, default 64).
	EFConstruction int
	// ParallelWorkers is the number of threads used for index build/vacuum
	// (0~64, default 0). Vastbase recommends ~75% of CPU cores for large
	// datasets.
	ParallelWorkers int
	// Quantizer optionally enables vector quantization on the graph index:
	// "pq" (~1/4 storage) or "rabitq" (~1/2 storage, faster queries).
	// Empty means no quantization. Quantization silently degrades to a plain
	// Graph_Index until the table holds enough rows (10000+), so it is safe
	// to request on empty tables.
	Quantizer string
	// EnableAsyncInsert marks the index for Vastbase's asynchronous insert
	// path (V3.0 Build 8 Patch 4+): new vectors land in a heap section first
	// and are merged into the graph in the background, greatly raising write
	// throughput at the cost of search freshness. It only takes effect when
	// the server GUC enable_async_vec_insert is also on, so enabling it is
	// safe on builds/servers that ignore it.
	EnableAsyncInsert bool
}

// ConfigFromEnv builds Config from VASTBASE_GRAPH_INDEX_* environment
// variables, falling back to Vastbase defaults.
func ConfigFromEnv() Config {
	cfg := Config{M: 16, EFConstruction: 64, ParallelWorkers: 0}
	if v, err := strconv.Atoi(os.Getenv("VASTBASE_GRAPH_INDEX_M")); err == nil && v >= 2 && v <= 100 {
		cfg.M = v
	}
	if v, err := strconv.Atoi(os.Getenv("VASTBASE_GRAPH_INDEX_EF_CONSTRUCTION")); err == nil && v >= 4 && v <= 1000 {
		cfg.EFConstruction = v
	}
	if v, err := strconv.Atoi(os.Getenv("VASTBASE_GRAPH_INDEX_PARALLEL_WORKERS")); err == nil && v >= 0 && v <= 64 {
		cfg.ParallelWorkers = v
	}
	switch strings.ToLower(os.Getenv("VASTBASE_GRAPH_INDEX_QUANTIZER")) {
	case "pq":
		cfg.Quantizer = "pq"
	case "rabitq":
		cfg.Quantizer = "rabitq"
	}
	if strings.EqualFold(os.Getenv("VASTBASE_GRAPH_INDEX_ASYNC_INSERT"), "true") {
		cfg.EnableAsyncInsert = true
	}
	return cfg
}

// vastbaseRepository implements Vastbase-based vector retrieval operations.
type vastbaseRepository struct {
	db       *gorm.DB
	cfg      Config
	ensureMu sync.Mutex
	ensured  map[int]bool // dimensions whose Graph_Index is known to exist
}

// NewVastbaseRetrieveEngineRepository creates a new Vastbase retriever
// repository and makes sure the embeddings schema exists. The Vastbase
// database is dedicated to retrieval (it is not touched by WeKnora's
// golang-migrate pipeline), so the schema is created here idempotently.
func NewVastbaseRetrieveEngineRepository(db *gorm.DB, cfg Config) (interfaces.RetrieveEngineRepository, error) {
	ctx := context.Background()
	logger.GetLogger(ctx).Info("[Vastbase] Initializing Vastbase retriever engine repository")
	r := &vastbaseRepository{db: db, cfg: cfg, ensured: make(map[int]bool)}
	if err := r.migrateSchema(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

// migrateSchema creates the embeddings table and auxiliary indexes if absent.
func (r *vastbaseRepository) migrateSchema(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS embeddings (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			source_id VARCHAR(64) NOT NULL,
			source_type INTEGER NOT NULL,
			chunk_id VARCHAR(64),
			knowledge_id VARCHAR(64),
			knowledge_base_id VARCHAR(64),
			tag_id VARCHAR(64),
			content TEXT NOT NULL,
			dimension INTEGER NOT NULL,
			embedding halfvector NOT NULL,
			is_enabled BOOLEAN DEFAULT TRUE
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS embeddings_unique_source ON embeddings(source_id, source_type)`,
		`CREATE INDEX IF NOT EXISTS idx_embeddings_knowledge_base_id ON embeddings(knowledge_base_id)`,
		`CREATE INDEX IF NOT EXISTS idx_embeddings_is_enabled ON embeddings(is_enabled)`,
		`CREATE INDEX IF NOT EXISTS idx_embeddings_tag_id ON embeddings(tag_id)`,
	}
	for _, stmt := range statements {
		if err := r.db.WithContext(ctx).Exec(stmt).Error; err != nil {
			logger.GetLogger(ctx).Errorf("[Vastbase] Schema migration failed: %v", err)
			return err
		}
	}
	logger.GetLogger(ctx).Info("[Vastbase] Schema migration completed")
	return nil
}

// ensureGraphIndex creates (once per dimension per process) the partial
// expression Graph_Index that accelerates cosine search for the given
// embedding dimension.
//
// The index expression must match the ORDER BY expression used by
// VectorRetrieve EXACTLY, otherwise the planner falls back to a sequential
// scan — hence the shared halfvectorCastExpr helper.
func (r *vastbaseRepository) ensureGraphIndex(ctx context.Context, dimension int) error {
	if dimension <= 0 {
		return nil
	}
	r.ensureMu.Lock()
	defer r.ensureMu.Unlock()
	if r.ensured[dimension] {
		return nil
	}

	withArgs := fmt.Sprintf("m=%d, ef_construction=%d, parallel_workers=%d",
		r.cfg.M, r.cfg.EFConstruction, r.cfg.ParallelWorkers)
	if r.cfg.Quantizer != "" {
		withArgs += ", quantizer=" + r.cfg.Quantizer
	}
	if r.cfg.EnableAsyncInsert {
		withArgs += ", enable_async_insert=true"
	}
	indexName := fmt.Sprintf("embeddings_graph_idx_cosine_%d", dimension)
	stmt := fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS %s ON embeddings USING graph_index((%s) halfvector_cosine_ops) WITH (%s) WHERE (dimension = %d)`,
		indexName, halfvectorCastExpr(dimension), withArgs, dimension,
	)
	logger.GetLogger(ctx).Infof("[Vastbase] Ensuring Graph_Index for dimension %d: %s", dimension, stmt)
	if err := r.db.WithContext(ctx).Exec(stmt).Error; err != nil {
		logger.GetLogger(ctx).Errorf("[Vastbase] Failed to create Graph_Index for dimension %d: %v", dimension, err)
		return err
	}
	r.ensured[dimension] = true
	return nil
}

// halfvectorCastExpr renders the canonical cast expression shared by the
// Graph_Index definition and the retrieval ORDER BY clause.
func halfvectorCastExpr(dimension int) string {
	return fmt.Sprintf("embedding::halfvector(%d)", dimension)
}

// EngineType returns the retriever engine type (Vastbase)
func (r *vastbaseRepository) EngineType() types.RetrieverEngineType {
	return types.VastbaseRetrieverEngineType
}

// Support returns supported retriever types (vector only)
func (r *vastbaseRepository) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

// calculateIndexStorageSize calculates storage size for a single index entry
func (r *vastbaseRepository) calculateIndexStorageSize(embeddingDB *vbVector) int64 {
	// 1. Text content size
	contentSizeBytes := int64(len(embeddingDB.Content))

	// 2. Vector storage size (2 bytes per dimension for half-precision float)
	var vectorSizeBytes int64 = 0
	if embeddingDB.Dimension > 0 {
		vectorSizeBytes = int64(embeddingDB.Dimension * 2)
	}

	// 3. Metadata size (fixed overhead for IDs, timestamps etc.)
	metadataSizeBytes := int64(200)

	// 4. Index overhead (Graph_Index keeps the graph structure plus a copy of
	// the vectors, ~2x vector size without quantization)
	indexOverheadBytes := vectorSizeBytes * 2

	return contentSizeBytes + vectorSizeBytes + metadataSizeBytes + indexOverheadBytes
}

// EstimateStorageSize estimates total storage size for multiple indices
func (r *vastbaseRepository) EstimateStorageSize(
	ctx context.Context, indexInfoList []*types.IndexInfo, additionalParams map[string]any,
) int64 {
	var totalStorageSize int64 = 0
	for _, indexInfo := range indexInfoList {
		embeddingDB := toDBVectorEmbedding(indexInfo, additionalParams)
		totalStorageSize += r.calculateIndexStorageSize(embeddingDB)
	}
	logger.GetLogger(ctx).Infof(
		"[Vastbase] Estimated storage size for %d indices: %d bytes",
		len(indexInfoList), totalStorageSize,
	)
	return totalStorageSize
}

// Save stores a single index entry
func (r *vastbaseRepository) Save(ctx context.Context, indexInfo *types.IndexInfo, additionalParams map[string]any) error {
	logger.GetLogger(ctx).Debugf("[Vastbase] Saving index for source ID: %s", indexInfo.SourceID)
	embeddingDB := toDBVectorEmbedding(indexInfo, additionalParams)
	if err := r.ensureGraphIndex(ctx, embeddingDB.Dimension); err != nil {
		return err
	}
	if err := r.db.WithContext(ctx).Create(embeddingDB).Error; err != nil {
		logger.GetLogger(ctx).Errorf("[Vastbase] Failed to save index: %v", err)
		return err
	}
	logger.GetLogger(ctx).Infof("[Vastbase] Successfully saved index for source ID: %s", indexInfo.SourceID)
	return nil
}

// BatchSave stores multiple index entries in batch.
//
// Note: Vastbase does not support target-less ON CONFLICT DO NOTHING, so the
// conflict target (source_id, source_type) — backed by the unique index
// embeddings_unique_source — must be spelled out.
func (r *vastbaseRepository) BatchSave(
	ctx context.Context, indexInfoList []*types.IndexInfo, additionalParams map[string]any,
) error {
	logger.GetLogger(ctx).Infof("[Vastbase] Batch saving %d indices", len(indexInfoList))
	indexInfoDBList := make([]*vbVector, len(indexInfoList))
	dims := make(map[int]struct{})
	for i := range indexInfoList {
		indexInfoDBList[i] = toDBVectorEmbedding(indexInfoList[i], additionalParams)
		dims[indexInfoDBList[i].Dimension] = struct{}{}
	}
	for dim := range dims {
		if err := r.ensureGraphIndex(ctx, dim); err != nil {
			return err
		}
	}
	err := r.batchInsertOnConflict(ctx, indexInfoDBList)
	if err != nil {
		logger.GetLogger(ctx).Errorf("[Vastbase] Batch save failed: %v", err)
		return err
	}
	logger.GetLogger(ctx).Infof("[Vastbase] Successfully batch saved %d indices", len(indexInfoDBList))
	return nil
}

// batchInsertOnConflict inserts rows with ON CONFLICT (source_id,
// source_type) DO NOTHING semantics.
//
// Raw SQL is required here: Vastbase rewrites INSERT ... ON CONFLICT into its
// ON DUPLICATE KEY form, which does not support a RETURNING clause — and
// gorm's Create always appends RETURNING "id" to fetch the serial primary
// key. The IDs of freshly inserted embeddings are never consumed upstream,
// so dropping RETURNING is safe.
func (r *vastbaseRepository) batchInsertOnConflict(ctx context.Context, rows []*vbVector) error {
	const chunkSize = 500 // 10 params/row keeps us far below the protocol param limit
	for start := 0; start < len(rows); start += chunkSize {
		end := min(start+chunkSize, len(rows))
		batch := rows[start:end]

		var sb strings.Builder
		vars := make([]interface{}, 0, len(batch)*10)
		sb.WriteString(`INSERT INTO embeddings (created_at, updated_at, source_id, source_type, chunk_id,
			knowledge_id, knowledge_base_id, tag_id, content, dimension, embedding, is_enabled) VALUES `)
		for i, row := range batch {
			if i > 0 {
				sb.WriteString(", ")
			}
			base := i * 10
			sb.WriteString(fmt.Sprintf(
				"(CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, $%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
				base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10,
			))
			vars = append(vars,
				row.SourceID, row.SourceType, row.ChunkID, row.KnowledgeID,
				row.KnowledgeBaseID, row.TagID, row.Content, row.Dimension,
				row.Embedding, row.IsEnabled,
			)
		}
		sb.WriteString(" ON CONFLICT (source_id, source_type) DO NOTHING")
		if err := r.db.WithContext(ctx).Exec(sb.String(), vars...).Error; err != nil {
			return err
		}
	}
	return nil
}

// DeleteByChunkIDList deletes indices by chunk IDs
func (r *vastbaseRepository) DeleteByChunkIDList(ctx context.Context, chunkIDList []string, dimension int, knowledgeType string) error {
	logger.GetLogger(ctx).Infof("[Vastbase] Deleting indices by chunk IDs, count: %d", len(chunkIDList))
	result := r.db.WithContext(ctx).Where("chunk_id IN ?", chunkIDList).Delete(&vbVector{})
	if result.Error != nil {
		logger.GetLogger(ctx).Errorf("[Vastbase] Failed to delete indices by chunk IDs: %v", result.Error)
		return result.Error
	}
	logger.GetLogger(ctx).Infof("[Vastbase] Successfully deleted %d indices by chunk IDs", result.RowsAffected)
	return nil
}

// DeleteBySourceIDList deletes indices by source IDs
func (r *vastbaseRepository) DeleteBySourceIDList(ctx context.Context, sourceIDList []string, dimension int, knowledgeType string) error {
	if len(sourceIDList) == 0 {
		return nil
	}
	logger.GetLogger(ctx).Infof("[Vastbase] Deleting indices by source IDs, count: %d", len(sourceIDList))
	result := r.db.WithContext(ctx).Where("source_id IN ?", sourceIDList).Delete(&vbVector{})
	if result.Error != nil {
		logger.GetLogger(ctx).Errorf("[Vastbase] Failed to delete indices by source IDs: %v", result.Error)
		return result.Error
	}
	logger.GetLogger(ctx).Infof("[Vastbase] Successfully deleted %d indices by source IDs", result.RowsAffected)
	return nil
}

// DeleteByKnowledgeIDList deletes indices by knowledge IDs
func (r *vastbaseRepository) DeleteByKnowledgeIDList(ctx context.Context, knowledgeIDList []string, dimension int, knowledgeType string) error {
	logger.GetLogger(ctx).Infof("[Vastbase] Deleting indices by knowledge IDs, count: %d", len(knowledgeIDList))
	result := r.db.WithContext(ctx).Where("knowledge_id IN ?", knowledgeIDList).Delete(&vbVector{})
	if result.Error != nil {
		logger.GetLogger(ctx).Errorf("[Vastbase] Failed to delete indices by knowledge IDs: %v", result.Error)
		return result.Error
	}
	logger.GetLogger(ctx).Infof("[Vastbase] Successfully deleted %d indices by knowledge IDs", result.RowsAffected)
	return nil
}

// Retrieve handles retrieval requests and routes to appropriate method
func (r *vastbaseRepository) Retrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	logger.GetLogger(ctx).Debugf("[Vastbase] Processing retrieval request of type: %s", params.RetrieverType)
	switch params.RetrieverType {
	case types.VectorRetrieverType:
		return r.VectorRetrieve(ctx, params)
	}
	err := fmt.Errorf("vastbase retriever does not support retriever type: %s", params.RetrieverType)
	logger.GetLogger(ctx).Errorf("[Vastbase] %v", err)
	return nil, err
}

// VectorRetrieve performs vector similarity search using Vastbase's native
// halfvector type and Graph_Index.
//
// Optimized for the Graph_Index ANN scan:
//   - the ORDER BY expression is byte-identical to the indexed expression
//     (embedding::halfvector(N) halfvector_cosine_ops)
//   - the partial index predicate (dimension = N) is inlined as a literal so
//     the planner can always prove predicate containment
//   - hnsw_ef_search is raised per query to cover the expanded candidate set
func (r *vastbaseRepository) VectorRetrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	logger.GetLogger(ctx).Infof("[Vastbase] Vector retrieval: dim=%d, topK=%d, threshold=%.4f",
		len(params.Embedding), params.TopK, params.Threshold)

	dimension := len(params.Embedding)
	if dimension == 0 {
		return nil, errors.New("empty query embedding")
	}
	queryVector := pgvector.NewHalfVector(params.Embedding)

	if err := r.ensureGraphIndex(ctx, dimension); err != nil {
		// Non-fatal: the query still works via sequential scan.
		logger.GetLogger(ctx).Warnf("[Vastbase] Graph_Index unavailable for dim %d: %v", dimension, err)
	}

	// Build WHERE conditions for filtering
	whereParts := make([]string, 0)
	allVars := make([]interface{}, 0)

	// Add query vector first (used in ORDER BY for the Graph_Index scan)
	allVars = append(allVars, queryVector)

	// Dimension predicate — inlined as a literal (safe: int we compute) so it
	// matches the partial index WHERE clause even under generic plans.
	whereParts = append(whereParts, fmt.Sprintf("dimension = %d", dimension))

	// KnowledgeBaseIDs and KnowledgeIDs use AND logic
	// - If only KnowledgeBaseIDs: search entire knowledge bases
	// - If only KnowledgeIDs: search specific documents
	// - If both: search specific documents within the knowledge bases (AND)
	if len(params.KnowledgeBaseIDs) > 0 {
		logger.GetLogger(ctx).Debugf(
			"[Vastbase] Filtering vector search by knowledge base IDs: %v",
			params.KnowledgeBaseIDs,
		)
		whereParts = append(whereParts, inClause("knowledge_base_id", params.KnowledgeBaseIDs, &allVars))
	}
	if len(params.KnowledgeIDs) > 0 {
		logger.GetLogger(ctx).Debugf(
			"[Vastbase] Filtering vector search by knowledge IDs: %v",
			params.KnowledgeIDs,
		)
		whereParts = append(whereParts, inClause("knowledge_id", params.KnowledgeIDs, &allVars))
	}
	// Filter by tag IDs if specified
	if len(params.TagIDs) > 0 {
		logger.GetLogger(ctx).Debugf("[Vastbase] Filtering vector search by tag IDs: %v", params.TagIDs)
		whereParts = append(whereParts, inClause("tag_id", params.TagIDs, &allVars))
	}

	// is_enabled filter
	whereParts = append(whereParts, fmt.Sprintf("(is_enabled IS NULL OR is_enabled = $%d)", len(allVars)+1))
	allVars = append(allVars, true)

	whereClause := "WHERE " + strings.Join(whereParts, " AND ")

	// Expand TopK to get more candidates before threshold filtering, same
	// budget policy as the postgres driver: enough headroom for the
	// post-filter without ballooning the graph candidate list.
	expandedTopK := params.TopK * 2
	if expandedTopK < 100 {
		expandedTopK = 100 // Minimum 100 candidates
	}
	if expandedTopK > 200 {
		expandedTopK = 200 // Maximum 200 candidates (keeps the ANN scan efficient)
	}
	if expandedTopK < params.TopK {
		expandedTopK = params.TopK
	}

	subqueryLimitParam := len(allVars) + 1
	thresholdParam := len(allVars) + 2
	finalLimitParam := len(allVars) + 3

	castExpr := halfvectorCastExpr(dimension)
	querySQL := fmt.Sprintf(`
		SELECT
			id, content, source_id, source_type, chunk_id, knowledge_id, knowledge_base_id, tag_id,
			(1 - distance) as score
		FROM (
			SELECT
				id, content, source_id, source_type, chunk_id, knowledge_id, knowledge_base_id, tag_id,
				%[1]s <=> $1::halfvector(%[2]d) as distance
			FROM embeddings
			%[3]s
			ORDER BY %[1]s <=> $1::halfvector(%[2]d)
			LIMIT $%[4]d
		) AS candidates
		WHERE distance <= $%[5]d
		ORDER BY distance ASC
		LIMIT $%[6]d
	`, castExpr, dimension, whereClause, subqueryLimitParam, thresholdParam, finalLimitParam)

	allVars = append(allVars, expandedTopK)       // LIMIT in subquery
	allVars = append(allVars, 1-params.Threshold) // Distance threshold
	allVars = append(allVars, params.TopK)        // Final LIMIT

	// Vastbase's Graph_Index honors the session GUC hnsw_ef_search (default
	// 100); the effective candidate list is max(limit, hnsw_ef_search). Raise
	// it to the expanded TopK so threshold/post-filtering does not silently
	// lose recall. SET LOCAL requires a transaction; we never write inside
	// it, so the cost is negligible.
	efSearch := expandedTopK
	if efSearch < 100 {
		efSearch = 100
	}

	var embeddingDBList []vbVectorWithScore

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(fmt.Sprintf("SET LOCAL hnsw_ef_search = %d", efSearch)).Error; err != nil {
			logger.GetLogger(ctx).Warnf("[Vastbase] Failed to set hnsw_ef_search=%d: %v", efSearch, err)
			return err
		}
		return tx.Raw(querySQL, allVars...).Scan(&embeddingDBList).Error
	})

	// Fallback: if the transaction failed because the GUC is unavailable on
	// this Vastbase build, retry the query without the SET so we still
	// return results (with default recall).
	if err != nil && len(embeddingDBList) == 0 && strings.Contains(err.Error(), "hnsw_ef_search") {
		logger.GetLogger(ctx).Warnf("[Vastbase] Retrying vector query without hnsw_ef_search override: %v", err)
		err = r.db.WithContext(ctx).Raw(querySQL, allVars...).Scan(&embeddingDBList).Error
	}

	if err == gorm.ErrRecordNotFound {
		logger.GetLogger(ctx).Warnf("[Vastbase] No vector matches found that meet threshold %.4f", params.Threshold)
		return nil, nil
	}
	if err != nil {
		logger.GetLogger(ctx).Errorf("[Vastbase] Vector retrieval failed: %v", err)
		return nil, err
	}

	// Apply final TopK limit (in case we got more results than needed)
	if len(embeddingDBList) > int(params.TopK) {
		embeddingDBList = embeddingDBList[:params.TopK]
	}

	logger.GetLogger(ctx).Infof("[Vastbase] Vector retrieval found %d results", len(embeddingDBList))
	results := make([]*types.IndexWithScore, len(embeddingDBList))
	const maxVectorResultLog = 8
	for i := range embeddingDBList {
		results[i] = fromDBVectorEmbeddingWithScore(&embeddingDBList[i], types.MatchTypeEmbedding)
		if i < maxVectorResultLog {
			logger.GetLogger(ctx).Debugf("[Vastbase] Vector search result %d: chunk_id %s, score %.4f",
				i, results[i].ChunkID, results[i].Score)
		}
	}
	if len(results) > maxVectorResultLog {
		logger.GetLogger(ctx).Debugf(
			"[Vastbase] Vector search result summary: total=%d logged=%d truncated=%d",
			len(results), maxVectorResultLog, len(results)-maxVectorResultLog,
		)
	}
	return []*types.RetrieveResult{
		{
			Results:             results,
			RetrieverEngineType: types.VastbaseRetrieverEngineType,
			RetrieverType:       types.VectorRetrieverType,
			Error:               nil,
		},
	}, nil
}

// inClause renders "col IN ($a, $b, ...)" appending values to vars.
func inClause(column string, values []string, vars *[]interface{}) string {
	placeholders := make([]string, len(values))
	paramStart := len(*vars) + 1
	for i := range values {
		placeholders[i] = fmt.Sprintf("$%d", paramStart+i)
		*vars = append(*vars, values[i])
	}
	return fmt.Sprintf("%s IN (%s)", column, strings.Join(placeholders, ", "))
}

// CopyIndices copies index data
func (r *vastbaseRepository) CopyIndices(ctx context.Context,
	sourceKnowledgeBaseID string,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
	dimension int,
	knowledgeType string,
) error {
	logger.GetLogger(ctx).Infof(
		"[Vastbase] Copying indices, source knowledge base: %s, target knowledge base: %s, mapping count: %d",
		sourceKnowledgeBaseID, targetKnowledgeBaseID, len(sourceToTargetChunkIDMap),
	)

	if len(sourceToTargetChunkIDMap) == 0 {
		logger.GetLogger(ctx).Warnf("[Vastbase] Mapping is empty, no need to copy")
		return nil
	}

	// Batch processing parameters
	batchSize := 500 // Number of records to process per batch
	offset := 0      // Offset for pagination
	totalCopied := 0 // Total number of copied records

	for {
		// Paginated query for source data
		var sourceVectors []*vbVector
		if err := r.db.WithContext(ctx).
			Where("knowledge_base_id = ?", sourceKnowledgeBaseID).
			Limit(batchSize).
			Offset(offset).
			Find(&sourceVectors).Error; err != nil {
			logger.GetLogger(ctx).Errorf("[Vastbase] Failed to query source index data: %v", err)
			return err
		}

		// If no more data, exit the loop
		if len(sourceVectors) == 0 {
			if offset == 0 {
				logger.GetLogger(ctx).Warnf("[Vastbase] No source index data found")
			}
			break
		}

		batchCount := len(sourceVectors)
		logger.GetLogger(ctx).Infof(
			"[Vastbase] Found %d source index data, batch start position: %d",
			batchCount, offset,
		)

		// Create target vector index
		targetVectors := make([]*vbVector, 0, batchCount)
		for _, sourceVector := range sourceVectors {
			// Get the mapped target chunk ID
			targetChunkID, ok := sourceToTargetChunkIDMap[sourceVector.ChunkID]
			if !ok {
				logger.GetLogger(ctx).Warnf(
					"[Vastbase] Source chunk %s not found in target chunk mapping, skipping",
					sourceVector.ChunkID,
				)
				continue
			}

			// Get the mapped target knowledge ID
			targetKnowledgeID, ok := sourceToTargetKBIDMap[sourceVector.KnowledgeID]
			if !ok {
				logger.GetLogger(ctx).Warnf(
					"[Vastbase] Source knowledge %s not found in target knowledge mapping, skipping",
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
			targetVector := &vbVector{
				Content:         sourceVector.Content,
				SourceID:        targetSourceID, // Handle SourceID transformation properly
				SourceType:      sourceVector.SourceType,
				ChunkID:         targetChunkID,         // Update to target chunk ID
				KnowledgeID:     targetKnowledgeID,     // Update to target knowledge ID
				KnowledgeBaseID: targetKnowledgeBaseID, // Update to target knowledge base ID
				Dimension:       sourceVector.Dimension,
				Embedding:       sourceVector.Embedding, // Copy the vector embedding directly, avoid recalculation
				IsEnabled:       sourceVector.IsEnabled,
			}

			targetVectors = append(targetVectors, targetVector)
		}

		// Batch insert target vector index
		if len(targetVectors) > 0 {
			if err := r.ensureGraphIndex(ctx, dimension); err != nil {
				return err
			}
			if err := r.batchInsertOnConflict(ctx, targetVectors); err != nil {
				logger.GetLogger(ctx).Errorf("[Vastbase] Failed to batch create target index: %v", err)
				return err
			}

			totalCopied += len(targetVectors)
			logger.GetLogger(ctx).Infof(
				"[Vastbase] Successfully copied batch data, batch size: %d, total copied: %d",
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

	logger.GetLogger(ctx).Infof("[Vastbase] Index copying completed, total copied: %d", totalCopied)
	return nil
}

// BatchUpdateChunkEnabledStatus updates the enabled status of chunks in batch
func (r *vastbaseRepository) BatchUpdateChunkEnabledStatus(ctx context.Context, chunkStatusMap map[string]bool) error {
	if len(chunkStatusMap) == 0 {
		logger.GetLogger(ctx).Warnf("[Vastbase] Chunk status map is empty, skipping update")
		return nil
	}

	logger.GetLogger(ctx).Infof("[Vastbase] Batch updating chunk enabled status, count: %d", len(chunkStatusMap))

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
		result := r.db.WithContext(ctx).Model(&vbVector{}).
			Where("chunk_id IN ?", enabledChunkIDs).
			Update("is_enabled", true)
		if result.Error != nil {
			logger.GetLogger(ctx).Errorf("[Vastbase] Failed to update enabled chunks: %v", result.Error)
			return result.Error
		}
		logger.GetLogger(ctx).
			Infof("[Vastbase] Updated %d chunks to enabled, rows affected: %d", len(enabledChunkIDs), result.RowsAffected)
	}

	// Batch update disabled chunks
	if len(disabledChunkIDs) > 0 {
		result := r.db.WithContext(ctx).Model(&vbVector{}).
			Where("chunk_id IN ?", disabledChunkIDs).
			Update("is_enabled", false)
		if result.Error != nil {
			logger.GetLogger(ctx).Errorf("[Vastbase] Failed to update disabled chunks: %v", result.Error)
			return result.Error
		}
		logger.GetLogger(ctx).
			Infof("[Vastbase] Updated %d chunks to disabled, rows affected: %d", len(disabledChunkIDs), result.RowsAffected)
	}

	logger.GetLogger(ctx).Infof("[Vastbase] Successfully batch updated chunk enabled status")
	return nil
}

// BatchUpdateChunkTagID updates the tag ID of chunks in batch
func (r *vastbaseRepository) BatchUpdateChunkTagID(ctx context.Context, chunkTagMap map[string]string) error {
	if len(chunkTagMap) == 0 {
		logger.GetLogger(ctx).Warnf("[Vastbase] Chunk tag map is empty, skipping update")
		return nil
	}

	logger.GetLogger(ctx).Infof("[Vastbase] Batch updating chunk tag ID, count: %d", len(chunkTagMap))

	// Group chunks by tag ID for batch updates
	tagGroups := make(map[string][]string)
	for chunkID, tagID := range chunkTagMap {
		tagGroups[tagID] = append(tagGroups[tagID], chunkID)
	}

	// Batch update chunks for each tag ID
	for tagID, chunkIDs := range tagGroups {
		result := r.db.WithContext(ctx).Model(&vbVector{}).
			Where("chunk_id IN ?", chunkIDs).
			Update("tag_id", tagID)
		if result.Error != nil {
			logger.GetLogger(ctx).Errorf("[Vastbase] Failed to update chunks with tag_id %s: %v", tagID, result.Error)
			return result.Error
		}
		logger.GetLogger(ctx).
			Infof("[Vastbase] Updated %d chunks to tag_id=%s, rows affected: %d", len(chunkIDs), tagID, result.RowsAffected)
	}

	logger.GetLogger(ctx).Infof("[Vastbase] Successfully batch updated chunk tag ID")
	return nil
}

// compile-time guard: the repository must satisfy the retrieval repository
// interface used by the shared indexing pipeline.
var _ interfaces.RetrieveEngineRepository = (*vastbaseRepository)(nil)
