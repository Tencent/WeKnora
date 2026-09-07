package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// pgRepository implements PostgreSQL-based retrieval operations
type pgRepository struct {
	db *gorm.DB // Database connection
	// rdb is the optional Redis client used to cache keyword (BM25) results.
	// May be nil (Lite mode without REDIS_ADDR) — caching is then disabled
	// and every query hits PostgreSQL directly.
	rdb *redis.Client
}

// NewPostgresRetrieveEngineRepository creates a new PostgreSQL retriever repository
// rdb may be nil to disable result caching.
func NewPostgresRetrieveEngineRepository(db *gorm.DB, rdb *redis.Client) interfaces.RetrieveEngineRepository {
	logger.GetLogger(context.Background()).Info("[Postgres] Initializing PostgreSQL retriever engine repository")
	return &pgRepository{db: db, rdb: rdb}
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
	g.bumpCorpusVersion(ctx)
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
	g.bumpCorpusVersion(ctx)
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
	g.bumpCorpusVersion(ctx)
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
	g.bumpCorpusVersion(ctx)
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
	g.bumpCorpusVersion(ctx)
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

	// Redis result cache (best-effort, fail-open). The cache key binds the
	// full retrieval context (tenant + query + filters + limits) plus a
	// corpus version that is bumped on every index write, so a stale row is
	// only ever returned until the next write. See bumpCorpusVersion.
	if cached := g.keywordsCacheGet(ctx, params); cached != nil {
		logger.GetLogger(ctx).Infof("[Postgres] Keywords cache hit for query=%s", params.Query)
		return cached, nil
	}

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

	// Use ParadeDB's ||| operator for matching any token
	conds = append(conds, clause.Expr{
		SQL:  "content ||| ?",
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
	const maxKeywordResultLog = 8
	for i := range embeddingDBList {
		results[i] = fromDBVectorEmbeddingWithScore(&embeddingDBList[i], types.MatchTypeKeywords)
		if i < maxKeywordResultLog {
			logger.GetLogger(ctx).Debugf("[Postgres] Keywords result %d: chunk=%s, score=%f",
				i, results[i].ChunkID, results[i].Score)
		}
	}
	if len(results) > maxKeywordResultLog {
		logger.GetLogger(ctx).Debugf(
			"[Postgres] Keywords result summary: total=%d logged=%d truncated=%d",
			len(results), maxKeywordResultLog, len(results)-maxKeywordResultLog,
		)
	}
	resp := &types.RetrieveResult{
		Results:             results,
		RetrieverEngineType: types.PostgresRetrieverEngineType,
		RetrieverType:       types.KeywordsRetrieverType,
		Error:               nil,
	}
	g.keywordsCacheSet(ctx, params, resp)
	return []*types.RetrieveResult{resp}, nil
}

// keywordsCacheKey builds the Redis key for a keyword retrieval.
// Includes the tenant and a hash of every filter that affects results so
// distinct queries never share a cache row.
func (g *pgRepository) keywordsCacheKey(ctx context.Context, params types.RetrieveParams) string {
	h := sha256.New()
	// Sort the IDs so equivalent filter sets map to the same key.
	sortedKB := append([]string(nil), params.KnowledgeBaseIDs...)
	sort.Strings(sortedKB)
	sortedKI := append([]string(nil), params.KnowledgeIDs...)
	sort.Strings(sortedKI)
	sortedTags := append([]string(nil), params.TagIDs...)
	sort.Strings(sortedTags)
	fmt.Fprintf(h, "%s|%s|%v|%v|%v|%d|%.6f", params.Query, params.KnowledgeType,
		sortedKB, sortedKI, sortedTags, params.TopK, params.Threshold)
	tid, _ := types.TenantIDFromContext(ctx)
	corpusVer := g.corpusVersion(ctx)
	return fmt.Sprintf("weknora:kw:%d:%016x:ver%d", tid, h.Sum(nil), corpusVer)
}

// corpusVersion returns the current corpus write version (0 when Redis is
// unavailable). Every successful index write bumps it via bumpCorpusVersion.
func (g *pgRepository) corpusVersion(ctx context.Context) uint64 {
	if g.rdb == nil {
		return 0
	}
	v, err := g.rdb.Get(ctx, "weknora:corpus_ver").Uint64()
	if err != nil {
		return 0
	}
	return v
}

// bumpCorpusVersion invalidates all cached keyword results after a write.
// Best-effort: a Redis failure only costs cache freshness (TTL is the
// backstop), never correctness of the write itself.
func (g *pgRepository) bumpCorpusVersion(ctx context.Context) {
	if g.rdb == nil {
		return
	}
	if err := g.rdb.Incr(ctx, "weknora:corpus_ver").Err(); err != nil {
		logger.GetLogger(ctx).Warnf("[Postgres] Failed to bump corpus version: %v", err)
	}
}

// keywordsCacheGet returns a cached keyword result, or nil on miss/error.
// Always fail-open: any Redis error falls through to PostgreSQL.
func (g *pgRepository) keywordsCacheGet(ctx context.Context, params types.RetrieveParams) []*types.RetrieveResult {
	if g.rdb == nil {
		return nil
	}
	raw, err := g.rdb.Get(ctx, g.keywordsCacheKey(ctx, params)).Bytes()
	if err != nil {
		return nil
	}
	var resp types.RetrieveResult
	if err := json.Unmarshal(raw, &resp); err != nil {
		logger.GetLogger(ctx).Warnf("[Postgres] Keywords cache unmarshal failed: %v", err)
		return nil
	}
	if len(resp.Results) == 0 {
		return nil
	}
	return []*types.RetrieveResult{&resp}
}

// keywordsCacheSet stores a keyword result with a short TTL. Rows larger
// than 1000 hits are not cached (BM25 scores dominate there anyway and the
// scan cost is what we are trying to avoid for the common small-result case).
// The 1000 cap covers the app's topK=250 default with headroom; a 250-row
// JSON payload is ~50KB, well within Redis limits.
func (g *pgRepository) keywordsCacheSet(ctx context.Context, params types.RetrieveParams, resp *types.RetrieveResult) {
	if g.rdb == nil || resp == nil || len(resp.Results) > 1000 {
		return
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return
	}
	if err := g.rdb.Set(ctx, g.keywordsCacheKey(ctx, params), raw, 600*time.Second).Err(); err != nil {
		logger.GetLogger(ctx).Debugf("[Postgres] Keywords cache set failed: %v", err)
	}
}

// VectorRetrieve performs vector similarity search using pgvector
// Optimized to use HNSW index efficiently and avoid recalculating vector distance
//
// pgvector HNSW/ivfflat cap indexable dimensions at 4000 (halfvec). For
// higher-dimensional embeddings (e.g. Qwen3-Embedding-8B at 4096) we query
// with the leading 3584-dim sub-vector instead: those models use Matryoshka
// Representation Learning, so the truncated vector preserves most semantics,
// and the expression below matches the partial HNSW index built on
// subvector(embedding, 1, 3584) for dimension=4096 rows.
const maxPgvectorIndexableDim = 4000
const pgFallbackIndexDim = 3584

func (g *pgRepository) VectorRetrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	logger.GetLogger(ctx).Infof("[Postgres] Vector retrieval: dim=%d, topK=%d, threshold=%.4f",
		len(params.Embedding), params.TopK, params.Threshold)

	dimension := len(params.Embedding)
	indexDim := dimension
	indexExpr := fmt.Sprintf("embedding::halfvec(%d)", indexDim)
	if dimension > maxPgvectorIndexableDim {
		indexDim = pgFallbackIndexDim
		indexExpr = "embedding_3584"
	}
	queryVector := pgvector.NewHalfVector(params.Embedding[:indexDim])

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

	// Expand TopK to get more candidates before threshold filtering.
	//
	// HNSW requires `ef_search >= LIMIT`, and a very large LIMIT (e.g. 1000)
	// forces HNSW to walk a near-exhaustive portion of the graph, often making
	// it slower than a sequential scan and pushing the planner to pick Seq Scan
	// even when an index exists. 200 is a good sweet spot: it gives enough
	// headroom for threshold/filter post-processing without ballooning ef_search.
	expandedTopK := params.TopK * 2
	if expandedTopK < 100 {
		expandedTopK = 100 // Minimum 100 candidates
	}
	if expandedTopK > 200 {
		expandedTopK = 200 // Maximum 200 candidates (keeps HNSW efficient)
	}
	if expandedTopK < params.TopK {
		expandedTopK = params.TopK // Ensure subquery limit is at least final limit
	}

	// Optimized query: Use subquery to calculate distance once.
	//
	// IMPORTANT: The HNSW index in this project is built on the EXPRESSION
	//   (embedding::halfvec(<dim>)) halfvec_cosine_ops
	// because the `embedding` column itself is `halfvec` without a fixed dimension
	// (so the table can store multiple embedding sizes such as 798 / 3584 / ...).
	//
	// pgvector requires the ORDER BY expression to match the indexed expression
	// EXACTLY, otherwise the planner falls back to a sequential scan. The
	// `embedding::halfvec(%d)` cast on both sides of `<=>` is therefore NOT
	// redundant — it is the only way to make the HNSW index get used at all.
	// See: pgvector issues #702, #835 and ParadeDB "indexing-expressions" docs.
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
				%[6]s <=> $1::halfvec(%[1]d) as distance
			FROM embeddings
			%[2]s
			ORDER BY %[6]s <=> $1::halfvec(%[1]d)
			LIMIT $%[3]d
		) AS candidates
		WHERE distance <= $%[4]d
		ORDER BY distance ASC
		LIMIT $%[5]d
	`, indexDim, whereClause, subqueryLimitParam, thresholdParam, finalLimitParam, indexExpr)

	allVars = append(allVars, expandedTopK)       // LIMIT in subquery
	allVars = append(allVars, 1-params.Threshold) // Distance threshold
	allVars = append(allVars, params.TopK)        // Final LIMIT

	// HNSW's `ef_search` defaults to 40, which is much smaller than our
	// `expandedTopK` budget (up to 1000). Without raising it, HNSW would only
	// return ~40 candidates per scan and our outer threshold/filter step would
	// silently lose recall. `SET LOCAL` requires a transaction so we wrap the
	// query in one. We never write inside this transaction, so the cost is
	// negligible.
	//
	// The index used depends on the branch in indexExpr above:
	//   - dimension > maxPgvectorIndexableDim: ivfflat partial index on
	//     embedding_3584 — tune ivfflat.probes (hnsw.* GUCs are no-ops here).
	//   - otherwise: HNSW expression index — tune hnsw.ef_search.
	efSearch := expandedTopK
	if efSearch < 40 {
		efSearch = 40
	}
	// ivfflat probes default: 1 (recall ~60-75% for lists=1000). pgvector
	// suggests probes >= sqrt(lists) ≈ 32 for near-HNSW recall; 8 is a
	// cost/benefit sweet spot (recall 80-87%, +15-25ms over the 35ms base).
	const ivfflatProbes = 8
	usesIVFFlat := indexExpr == "embedding_3584"

	var embeddingDBList []pgVectorWithScore

	err := g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if usesIVFFlat {
			if err := tx.Exec(fmt.Sprintf("SET LOCAL ivfflat.probes = %d", ivfflatProbes)).Error; err != nil {
				logger.GetLogger(ctx).Warnf("[Postgres] Failed to set ivfflat.probes=%d: %v", ivfflatProbes, err)
				return err
			}
		} else {
			if err := tx.Exec(fmt.Sprintf("SET LOCAL hnsw.ef_search = %d", efSearch)).Error; err != nil {
				// Treat as non-fatal: pgvector should always expose this GUC, but if
				// for any reason it does not we still want the query to run (just
				// with default recall). We must rollback first because a failed
				// statement aborts the transaction in PostgreSQL.
				logger.GetLogger(ctx).Warnf("[Postgres] Failed to set hnsw.ef_search=%d: %v", efSearch, err)
				return err
			}
			// pgvector >= 0.8 supports iterative scan, which keeps pulling more
			// candidates from HNSW until the post-filter (knowledge_base_id /
			// knowledge_id / tag_id / is_enabled) yields enough rows. Without it,
			// HNSW returns at most ef_search candidates and the outer filter may
			// silently lose recall when the filter is selective.
			// Best-effort: ignore failure on older pgvector versions.
			if err := tx.Exec("SET LOCAL hnsw.iterative_scan = strict_order").Error; err != nil {
				logger.GetLogger(ctx).Debugf("[Postgres] hnsw.iterative_scan not available: %v", err)
				// abort transaction and let the fallback path below handle it.
				return err
			}
		}
		return tx.Raw(querySQL, allVars...).Scan(&embeddingDBList).Error
	})

	// Fallback: if the transaction failed because of an unsupported GUC (e.g.
	// older pgvector that doesn't have hnsw.ef_search / hnsw.iterative_scan /
	// ivfflat.probes), retry the query without the SETs so we still return
	// results.
	if err != nil && len(embeddingDBList) == 0 &&
		(strings.Contains(err.Error(), "hnsw.ef_search") ||
			strings.Contains(err.Error(), "hnsw.iterative_scan") ||
			strings.Contains(err.Error(), "ivfflat.probes")) {
		logger.GetLogger(ctx).Warnf("[Postgres] Retrying vector query without GUC overrides: %v", err)
		err = g.db.WithContext(ctx).Raw(querySQL, allVars...).Scan(&embeddingDBList).Error
	}

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
	const maxVectorResultLog = 8
	for i := range embeddingDBList {
		results[i] = fromDBVectorEmbeddingWithScore(&embeddingDBList[i], types.MatchTypeEmbedding)
		if i < maxVectorResultLog {
			logger.GetLogger(ctx).Debugf("[Postgres] Vector search result %d: chunk_id %s, score %.4f",
				i, results[i].ChunkID, results[i].Score)
		}
	}
	if len(results) > maxVectorResultLog {
		logger.GetLogger(ctx).Debugf(
			"[Postgres] Vector search result summary: total=%d logged=%d truncated=%d",
			len(results), maxVectorResultLog, len(results)-maxVectorResultLog,
		)
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
				SourceID:        targetSourceID, // Handle SourceID transformation properly
				SourceType:      sourceVector.SourceType,
				ChunkID:         targetChunkID,         // Update to target chunk ID
				KnowledgeID:     targetKnowledgeID,     // Update to target knowledge ID
				KnowledgeBaseID: targetKnowledgeBaseID, // Update to target knowledge base ID
				Dimension:       sourceVector.Dimension,
				Embedding:       sourceVector.Embedding, // Copy the vector embedding directly, avoid recalculation
				Embedding3584:   sourceVector.Embedding3584,
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
	g.bumpCorpusVersion(ctx)
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
	g.bumpCorpusVersion(ctx)
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
	g.bumpCorpusVersion(ctx)
	return nil
}
