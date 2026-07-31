package mysql

import (
	"container/heap"
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// columns 用于 INSERT 和 SELECT 的列名列表。
var columns = []string{
	"id", "content", "source_id", "source_type", "chunk_id",
	"knowledge_id", "knowledge_base_id", "tag_id", "is_enabled",
	"embedding",
}

// columnsForRetrieve 用于检索结果的列名列表（不含 embedding）。
var columnsForRetrieve = []string{
	"id", "content", "source_id", "source_type", "chunk_id",
	"knowledge_id", "knowledge_base_id", "tag_id", "is_enabled",
}

// columnsForCopy 用于 CopyIndices 的列名列表（含 embedding）。
var columnsForCopy = []string{
	"id", "content", "source_id", "source_type", "chunk_id",
	"knowledge_id", "knowledge_base_id", "tag_id", "is_enabled", "embedding",
}

// NewMysqlRetrieveEngineRepository 创建 MySQL 检索引擎仓储。
func NewMysqlRetrieveEngineRepository(
	db *sql.DB,
	host string, port int,
	username, password, database string,
	indexCfg ...*types.IndexConfig,
) interfaces.RetrieveEngineRepository {
	tablePrefix := defaultTablePrefix
	if len(indexCfg) > 0 && indexCfg[0] != nil {
		tablePrefix = normalizeTablePrefix(indexCfg[0].GetIndexNameOrDefault(types.MySQLRetrieverEngineType))
	}
	return &mysqlRepository{
		db:          db,
		host:        host,
		port:        port,
		username:    username,
		password:    password,
		database:    database,
		tablePrefix: tablePrefix,
	}
}

// EngineType 返回检索引擎类型。
func (r *mysqlRepository) EngineType() types.RetrieverEngineType {
	return types.MySQLRetrieverEngineType
}

// Support 返回支持的检索类型。
func (r *mysqlRepository) Support() []types.RetrieverType {
	return []types.RetrieverType{types.KeywordsRetrieverType, types.VectorRetrieverType}
}

// EstimateStorageSize 估算存储大小。
func (r *mysqlRepository) EstimateStorageSize(
	_ context.Context,
	indexInfoList []*types.IndexInfo,
	params map[string]any,
) int64 {
	var total int64
	for _, info := range indexInfoList {
		emb := toMysqlVectorEmbedding(info, params)
		total += calculateStorageSize(emb)
	}
	return total
}

// calculateStorageSize 估算单行存储成本。
func calculateStorageSize(emb *MysqlVectorEmbedding) int64 {
	var size int64
	size += int64(len(emb.ID))
	size += int64(len(emb.Content))
	size += int64(len(emb.SourceID))
	size += int64(len(emb.ChunkID))
	size += int64(len(emb.KnowledgeID))
	size += int64(len(emb.KnowledgeBaseID))
	size += int64(len(emb.TagID))
	size += 8 // source_type int
	// MySQL stores JSON numbers as binary-JSON doubles plus array offsets,
	// not as the float32 values held by Go. Large arrays use roughly
	// 13 bytes per element plus a 9-byte container header. This deliberately
	// uses the large-array representation so quota accounting does not
	// understate high-dimensional embeddings.
	if len(emb.Embedding) > 0 {
		size += int64(len(emb.Embedding))*13 + 9
	}
	const metaBytes int64 = 24
	return size + metaBytes
}

// Save 保存单条索引。
func (r *mysqlRepository) Save(
	ctx context.Context,
	info *types.IndexInfo,
	params map[string]any,
) error {
	return r.BatchSave(ctx, []*types.IndexInfo{info}, params)
}

// BatchSave 批量保存索引。
func (r *mysqlRepository) BatchSave(
	ctx context.Context,
	indexInfoList []*types.IndexInfo,
	params map[string]any,
) error {
	if len(indexInfoList) == 0 {
		return nil
	}

	// 按维度分组
	groups := make(map[int][]*MysqlVectorEmbedding)
	for _, info := range indexInfoList {
		emb := toMysqlVectorEmbedding(info, params)
		// 稳定主键
		if emb.ID == "" {
			emb.ID = emb.SourceID
		}
		if emb.ID == "" {
			emb.ID = uuid.New().String()
		}
		dim := len(emb.Embedding)
		if dim == 0 {
			var err error
			dim, err = keywordOnlyDimensionFromParams(params)
			if err != nil {
				return fmt.Errorf("save keyword-only row %q: %w", emb.ID, err)
			}
		}
		groups[dim] = append(groups[dim], emb)
	}

	for dim, rows := range groups {
		if err := r.ensureTable(ctx, dim); err != nil {
			return err
		}
		if err := r.insertRows(ctx, r.getTableName(dim), rows); err != nil {
			return fmt.Errorf("batch save dim=%d: %w", dim, err)
		}
	}
	return nil
}

func keywordOnlyDimensionFromParams(params map[string]any) (int, error) {
	if params == nil {
		return 0, fmt.Errorf("missing positive dimension parameter")
	}
	dimension, ok := params["dimension"].(int)
	if !ok || dimension <= 0 {
		return 0, fmt.Errorf("missing positive dimension parameter")
	}
	return dimension, nil
}

// insertRows 批量插入数据。
func (r *mysqlRepository) insertRows(
	ctx context.Context,
	table string,
	rows []*MysqlVectorEmbedding,
) error {
	if len(rows) == 0 {
		return nil
	}

	const perRowPlaceholders = "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"

	parts := make([]string, len(rows))
	args := make([]interface{}, 0, len(rows)*10)
	for i, e := range rows {
		var embeddingValue interface{}
		if len(e.Embedding) > 0 {
			embeddingJSON, err := embeddingToJSON(e.Embedding)
			if err != nil {
				return fmt.Errorf("encode embedding for row %q: %w", e.ID, err)
			}
			embeddingValue = embeddingJSON
		}
		parts[i] = perRowPlaceholders
		args = append(args,
			e.ID, e.Content, e.SourceID, e.SourceType,
			e.ChunkID, e.KnowledgeID, e.KnowledgeBaseID, e.TagID,
			e.IsEnabled, embeddingValue,
		)
	}

	stmt := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES %s ON DUPLICATE KEY UPDATE "+
			"content=VALUES(content), source_id=VALUES(source_id), source_type=VALUES(source_type), "+
			"chunk_id=VALUES(chunk_id), knowledge_id=VALUES(knowledge_id), "+
			"knowledge_base_id=VALUES(knowledge_base_id), tag_id=VALUES(tag_id), "+
			"is_enabled=VALUES(is_enabled), embedding=VALUES(embedding)",
		quoteIdentifier(table),
		strings.Join(columns, ", "),
		strings.Join(parts, ", "),
	)
	_, err := r.db.ExecContext(ctx, stmt, args...)
	return err
}

// DeleteByChunkIDList 根据 chunk_id 删除索引。
func (r *mysqlRepository) DeleteByChunkIDList(
	ctx context.Context,
	chunkIDList []string,
	dimension int,
	_ string,
) error {
	return r.deleteByField(ctx, "chunk_id", chunkIDList, dimension)
}

// DeleteByKnowledgeIDList 根据 knowledge_id 删除索引。
func (r *mysqlRepository) DeleteByKnowledgeIDList(
	ctx context.Context,
	knowledgeIDList []string,
	dimension int,
	_ string,
) error {
	return r.deleteByField(ctx, "knowledge_id", knowledgeIDList, dimension)
}

// DeleteBySourceIDList 根据 source_id 删除索引。
func (r *mysqlRepository) DeleteBySourceIDList(
	ctx context.Context,
	sourceIDList []string,
	dimension int,
	_ string,
) error {
	return r.deleteByField(ctx, "source_id", sourceIDList, dimension)
}

// deleteByField 通用的删除实现。
func (r *mysqlRepository) deleteByField(
	ctx context.Context,
	field string,
	ids []string,
	dimension int,
) error {
	if len(ids) == 0 {
		return nil
	}

	table := r.getTableName(dimension)
	exists, err := r.tableExists(ctx, table)
	if err != nil {
		return fmt.Errorf("check table %s: %w", table, err)
	}
	if !exists {
		return nil
	}

	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	stmt := fmt.Sprintf(
		"DELETE FROM %s WHERE %s IN (%s)",
		quoteIdentifier(table), field, strings.Join(placeholders, ", "),
	)
	if _, err := r.db.ExecContext(ctx, stmt, args...); err != nil {
		return fmt.Errorf("delete by %s: %w", field, err)
	}
	return nil
}

// BatchUpdateChunkEnabledStatus updates is_enabled for chunks across all
// dimension tables owned by this MySQL retriever.
func (r *mysqlRepository) BatchUpdateChunkEnabledStatus(ctx context.Context, chunkStatusMap map[string]bool) error {
	if len(chunkStatusMap) == 0 {
		return nil
	}
	tables, err := r.listEmbeddingTables(ctx)
	if err != nil {
		return fmt.Errorf("list embedding tables: %w", err)
	}
	for _, table := range tables {
		for enabled, chunkIDs := range groupChunkStatus(chunkStatusMap) {
			if err := r.updateChunkField(ctx, table, "is_enabled", enabled, chunkIDs); err != nil {
				return fmt.Errorf("update chunk enabled status in %s: %w", table, err)
			}
		}
	}
	return nil
}

// BatchUpdateChunkTagID updates tag_id for chunks across all dimension tables
// owned by this MySQL retriever.
func (r *mysqlRepository) BatchUpdateChunkTagID(ctx context.Context, chunkTagMap map[string]string) error {
	if len(chunkTagMap) == 0 {
		return nil
	}
	tables, err := r.listEmbeddingTables(ctx)
	if err != nil {
		return fmt.Errorf("list embedding tables: %w", err)
	}
	for _, table := range tables {
		for tagID, chunkIDs := range groupChunkTags(chunkTagMap) {
			if err := r.updateChunkField(ctx, table, "tag_id", tagID, chunkIDs); err != nil {
				return fmt.Errorf("update chunk tag in %s: %w", table, err)
			}
		}
	}
	return nil
}

func groupChunkStatus(chunkStatusMap map[string]bool) map[bool][]string {
	groups := map[bool][]string{
		true:  {},
		false: {},
	}
	for chunkID, enabled := range chunkStatusMap {
		if chunkID == "" {
			continue
		}
		groups[enabled] = append(groups[enabled], chunkID)
	}
	return groups
}

func groupChunkTags(chunkTagMap map[string]string) map[string][]string {
	groups := make(map[string][]string)
	for chunkID, tagID := range chunkTagMap {
		if chunkID == "" {
			continue
		}
		groups[tagID] = append(groups[tagID], chunkID)
	}
	return groups
}

func (r *mysqlRepository) updateChunkField(
	ctx context.Context,
	table string,
	field string,
	value interface{},
	chunkIDs []string,
) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	placeholders := make([]string, len(chunkIDs))
	args := make([]interface{}, 0, len(chunkIDs)+1)
	args = append(args, value)
	for i, id := range chunkIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	stmt := fmt.Sprintf(
		"UPDATE %s SET %s = ? WHERE chunk_id IN (%s)",
		quoteIdentifier(table),
		field,
		strings.Join(placeholders, ", "),
	)
	_, err := r.db.ExecContext(ctx, stmt, args...)
	return err
}

// Retrieve dispatches MySQL retrieval requests by retriever type.
func (r *mysqlRepository) Retrieve(
	ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	switch params.RetrieverType {
	case types.VectorRetrieverType:
		return r.VectorRetrieve(ctx, params)
	case types.KeywordsRetrieverType:
		return r.KeywordsRetrieve(ctx, params)
	}
	return nil, fmt.Errorf("invalid retriever type: %v", params.RetrieverType)
}

// VectorRetrieve 向量相似度检索。
// 使用 MySQL 8.0 标准 JSON 函数计算余弦相似度，避免依赖非社区版向量函数。
func (r *mysqlRepository) VectorRetrieve(
	ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	if len(params.Embedding) == 0 {
		return nil, fmt.Errorf("empty query embedding")
	}
	queryNorm, err := validateQueryEmbedding(params.Embedding)
	if err != nil {
		return nil, err
	}
	if math.IsNaN(params.Threshold) || math.IsInf(params.Threshold, 0) {
		return nil, fmt.Errorf("invalid vector threshold: %v", params.Threshold)
	}

	table := r.getTableName(len(params.Embedding))
	exists, err := r.tableExists(ctx, table)
	if err != nil {
		return nil, fmt.Errorf("check table %s: %w", table, err)
	}
	if !exists {
		return buildRetrieveResult(nil, types.VectorRetrieverType), nil
	}

	// Keep the candidate scan and metadata fetch on one consistent snapshot.
	tx, err := r.db.BeginTx(ctx, vectorReadTransactionOptions())
	if err != nil {
		return nil, fmt.Errorf("begin vector retrieve transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmt, args := buildVectorCandidateSQL(table, params)
	rows, err := tx.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("scan vector candidates: %w", err)
	}
	hits, rankErr := rankVectorCandidates(
		rows,
		params.Embedding,
		queryNorm,
		params.Threshold,
		normalizeTopK(params.TopK),
	)
	closeErr := rows.Close()
	if rankErr != nil {
		return nil, rankErr
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close vector candidate rows: %w", closeErr)
	}

	results, err := fetchVectorMetadata(ctx, tx, table, params, hits)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit vector retrieve transaction: %w", err)
	}
	return buildRetrieveResult(results, types.VectorRetrieverType), nil
}

func vectorReadTransactionOptions() *sql.TxOptions {
	return &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	}
}

func buildVectorCandidateSQL(table string, params types.RetrieveParams) (string, []interface{}) {
	whereClause, args := buildVectorWhereClause(params).build()
	return fmt.Sprintf(
		"SELECT id, embedding FROM %s WHERE %s AND embedding IS NOT NULL",
		quoteIdentifier(table),
		whereClause,
	), args
}

type rankedVectorHit struct {
	id    string
	score float64
}

// rankedVectorHeap keeps the worst retained hit at index zero. For equal
// scores, the lexicographically larger ID is worse, matching final ordering.
type rankedVectorHeap []rankedVectorHit

func (h rankedVectorHeap) Len() int { return len(h) }

func (h rankedVectorHeap) Less(i, j int) bool {
	if h[i].score == h[j].score {
		return h[i].id > h[j].id
	}
	return h[i].score < h[j].score
}

func (h rankedVectorHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *rankedVectorHeap) Push(value interface{}) {
	*h = append(*h, value.(rankedVectorHit))
}

func (h *rankedVectorHeap) Pop() interface{} {
	old := *h
	n := len(old)
	value := old[n-1]
	*h = old[:n-1]
	return value
}

func validateQueryEmbedding(query []float32) (float64, error) {
	var normSq float64
	for i, value := range query {
		v := float64(value)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("query embedding contains non-finite value at dimension %d", i)
		}
		normSq += v * v
	}
	if normSq == 0 {
		return 0, fmt.Errorf("query embedding has zero norm")
	}
	return math.Sqrt(normSq), nil
}

func cosineSimilarity(query, stored []float32, queryNorm float64) (float64, error) {
	if len(stored) != len(query) {
		return 0, fmt.Errorf(
			"stored embedding dimension %d does not match query dimension %d",
			len(stored),
			len(query),
		)
	}

	var dot, storedNormSq float64
	for i, storedValue := range stored {
		value := float64(storedValue)
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0, fmt.Errorf("stored embedding contains non-finite value at dimension %d", i)
		}
		dot += float64(query[i]) * value
		storedNormSq += value * value
	}
	if queryNorm == 0 || storedNormSq == 0 {
		return 0, nil
	}
	return dot / (queryNorm * math.Sqrt(storedNormSq)), nil
}

func rankVectorCandidates(
	rows *sql.Rows,
	query []float32,
	queryNorm float64,
	threshold float64,
	topK int,
) ([]rankedVectorHit, error) {
	if topK <= 0 {
		return nil, fmt.Errorf("vector topK must be positive, got %d", topK)
	}
	initialCapacity := topK
	if initialCapacity > vectorMetadataBatchSize {
		initialCapacity = vectorMetadataBatchSize
	}
	hits := make(rankedVectorHeap, 0, initialCapacity)
	heap.Init(&hits)

	for rows.Next() {
		var (
			id           string
			embeddingRaw []byte
		)
		if err := rows.Scan(&id, &embeddingRaw); err != nil {
			return nil, fmt.Errorf("scan vector candidate: %w", err)
		}
		stored, err := parseEmbeddingJSON(embeddingRaw)
		if err != nil {
			return nil, fmt.Errorf("parse embedding for row %q: %w", id, err)
		}
		score, err := cosineSimilarity(query, stored, queryNorm)
		if err != nil {
			return nil, fmt.Errorf("score row %q: %w", id, err)
		}
		if threshold > 0 && score < threshold {
			continue
		}

		hit := rankedVectorHit{id: id, score: score}
		if hits.Len() < topK {
			heap.Push(&hits, hit)
			continue
		}
		if betterVectorHit(hit, hits[0]) {
			hits[0] = hit
			heap.Fix(&hits, 0)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vector candidates: %w", err)
	}

	sort.Slice(hits, func(i, j int) bool {
		return betterVectorHit(hits[i], hits[j])
	})
	return hits, nil
}

func betterVectorHit(left, right rankedVectorHit) bool {
	if left.score == right.score {
		return left.id < right.id
	}
	return left.score > right.score
}

const vectorMetadataBatchSize = 1000

func fetchVectorMetadata(
	ctx context.Context,
	tx *sql.Tx,
	table string,
	params types.RetrieveParams,
	hits []rankedVectorHit,
) ([]*types.IndexWithScore, error) {
	if len(hits) == 0 {
		return nil, nil
	}

	metadata := make(map[string]*types.IndexWithScore, len(hits))
	for start := 0; start < len(hits); start += vectorMetadataBatchSize {
		end := start + vectorMetadataBatchSize
		if end > len(hits) {
			end = len(hits)
		}
		stmt, args := buildVectorMetadataSQL(table, params, hits[start:end])
		rows, err := tx.QueryContext(ctx, stmt, args...)
		if err != nil {
			return nil, fmt.Errorf("fetch vector metadata: %w", err)
		}
		batch, scanErr := scanVectorMetadataRows(rows)
		closeErr := rows.Close()
		if scanErr != nil {
			return nil, scanErr
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close vector metadata rows: %w", closeErr)
		}
		for id, result := range batch {
			metadata[id] = result
		}
	}

	results := make([]*types.IndexWithScore, 0, len(hits))
	for _, hit := range hits {
		result, ok := metadata[hit.id]
		if !ok {
			return nil, fmt.Errorf("vector metadata for selected row %q is missing", hit.id)
		}
		result.Score = hit.score
		result.MatchType = types.MatchTypeEmbedding
		results = append(results, result)
	}
	return results, nil
}

func buildVectorMetadataSQL(
	table string,
	params types.RetrieveParams,
	hits []rankedVectorHit,
) (string, []interface{}) {
	idPlaceholders := make([]string, len(hits))
	args := make([]interface{}, 0, len(hits))
	for i, hit := range hits {
		idPlaceholders[i] = "?"
		args = append(args, hit.id)
	}
	whereClause, whereArgs := buildVectorWhereClause(params).build()
	args = append(args, whereArgs...)
	return fmt.Sprintf(
		"SELECT %s FROM %s WHERE id IN (%s) AND %s AND embedding IS NOT NULL",
		strings.Join(columnsForRetrieve, ", "),
		quoteIdentifier(table),
		strings.Join(idPlaceholders, ","),
		whereClause,
	), args
}

func scanVectorMetadataRows(rows *sql.Rows) (map[string]*types.IndexWithScore, error) {
	out := make(map[string]*types.IndexWithScore)
	for rows.Next() {
		var (
			id, content, sourceID, chunkID      sql.NullString
			knowledgeID, knowledgeBaseID, tagID sql.NullString
			sourceType                          sql.NullInt64
			isEnabled                           sql.NullBool
		)
		if err := rows.Scan(
			&id, &content, &sourceID, &sourceType, &chunkID,
			&knowledgeID, &knowledgeBaseID, &tagID, &isEnabled,
		); err != nil {
			return nil, fmt.Errorf("scan vector metadata: %w", err)
		}
		out[id.String] = &types.IndexWithScore{
			ID:              id.String,
			Content:         content.String,
			SourceID:        sourceID.String,
			SourceType:      types.SourceType(sourceType.Int64),
			ChunkID:         chunkID.String,
			KnowledgeID:     knowledgeID.String,
			KnowledgeBaseID: knowledgeBaseID.String,
			TagID:           tagID.String,
			IsEnabled:       !isEnabled.Valid || isEnabled.Bool,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vector metadata: %w", err)
	}
	return out, nil
}

// buildVectorWhereClause 构建向量检索的过滤条件。
func buildVectorWhereClause(params types.RetrieveParams) *whereBuilder {
	wb := &whereBuilder{}

	if len(params.KnowledgeBaseIDs) > 0 {
		wb.addIN("knowledge_base_id", params.KnowledgeBaseIDs)
	}
	if len(params.KnowledgeIDs) > 0 {
		wb.addIN("knowledge_id", params.KnowledgeIDs)
	}
	if len(params.TagIDs) > 0 {
		wb.addIN("tag_id", params.TagIDs)
	}
	if len(params.ExcludeKnowledgeIDs) > 0 {
		wb.addNotIN("knowledge_id", params.ExcludeKnowledgeIDs)
	}
	if len(params.ExcludeChunkIDs) > 0 {
		wb.addNotIN("chunk_id", params.ExcludeChunkIDs)
	}
	wb.add("(is_enabled IS NULL OR is_enabled = TRUE)")

	return wb
}

// whereBuilder 辅助构建 WHERE 子句。
type whereBuilder struct {
	conditions []string
	args       []interface{}
}

func (wb *whereBuilder) add(cond string) {
	wb.conditions = append(wb.conditions, cond)
}

func (wb *whereBuilder) addIN(field string, values []string) {
	if len(values) == 0 {
		return
	}
	placeholders := make([]string, len(values))
	for i, v := range values {
		placeholders[i] = "?"
		wb.args = append(wb.args, v)
	}
	wb.conditions = append(wb.conditions, fmt.Sprintf("%s IN (%s)", field, strings.Join(placeholders, ",")))
}

func (wb *whereBuilder) addNotIN(field string, values []string) {
	if len(values) == 0 {
		return
	}
	placeholders := make([]string, len(values))
	for i, v := range values {
		placeholders[i] = "?"
		wb.args = append(wb.args, v)
	}
	wb.conditions = append(wb.conditions, fmt.Sprintf("%s NOT IN (%s)", field, strings.Join(placeholders, ",")))
}

func (wb *whereBuilder) build() (string, []interface{}) {
	if len(wb.conditions) == 0 {
		return "1=1", nil
	}
	return strings.Join(wb.conditions, " AND "), wb.args
}

// KeywordsRetrieve 关键词检索。
// 使用 MySQL FULLTEXT INDEX。
func (r *mysqlRepository) KeywordsRetrieve(
	ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	query := strings.TrimSpace(params.Query)
	if query == "" {
		return buildRetrieveResult(nil, types.KeywordsRetrieverType), nil
	}
	if math.IsNaN(params.Threshold) || math.IsInf(params.Threshold, 0) {
		return nil, fmt.Errorf("invalid keyword threshold: %v", params.Threshold)
	}

	tables, err := r.listEmbeddingTables(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	if len(tables) == 0 {
		return buildRetrieveResult(nil, types.KeywordsRetrieverType), nil
	}

	var all []*types.IndexWithScore
	for _, table := range tables {
		params.Query = query
		stmt, args := buildKeywordRetrieveSQL(table, params)
		rows, err := r.db.QueryContext(ctx, stmt, args...)
		if err != nil {
			return nil, fmt.Errorf("keyword retrieve table %s: %w", table, err)
		}

		batch, err := scanRetrieveRows(rows, types.MatchTypeKeywords)
		rows.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
	}

	return buildRetrieveResult(limitTopKByScore(all, params.TopK), types.KeywordsRetrieverType), nil
}

func buildKeywordRetrieveSQL(table string, params types.RetrieveParams) (string, []interface{}) {
	wb := buildVectorWhereClause(params)
	whereClause, whereArgs := wb.build()
	query := strings.TrimSpace(params.Query)

	stmt := fmt.Sprintf(
		"SELECT %s, MATCH(content) AGAINST(? IN NATURAL LANGUAGE MODE) AS score "+
			"FROM %s "+
			"WHERE %s AND MATCH(content) AGAINST(? IN NATURAL LANGUAGE MODE) "+
			"HAVING score >= ? "+
			"ORDER BY score DESC, id COLLATE utf8mb4_bin ASC "+
			"LIMIT %d",
		strings.Join(columnsForRetrieve, ", "),
		quoteIdentifier(table),
		whereClause,
		normalizeTopK(params.TopK),
	)

	args := make([]interface{}, 0, len(whereArgs)+2)
	args = append(args, query)
	args = append(args, whereArgs...)
	args = append(args, query)
	args = append(args, params.Threshold)
	return stmt, args
}

func limitTopKByScore(rows []*types.IndexWithScore, topK int) []*types.IndexWithScore {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Score != rows[j].Score {
			return rows[i].Score > rows[j].Score
		}
		return rows[i].ID < rows[j].ID
	})
	limit := normalizeTopK(topK)
	if len(rows) > limit {
		return rows[:limit]
	}
	return rows
}

func normalizeTopK(topK int) int {
	if topK <= 0 {
		return 10
	}
	return topK
}

// CopyIndices 复制索引数据。
func (r *mysqlRepository) CopyIndices(
	ctx context.Context,
	sourceKnowledgeBaseID string,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
	dimension int,
	_ string,
) error {
	if len(sourceToTargetChunkIDMap) == 0 {
		return nil
	}

	if err := r.ensureTable(ctx, dimension); err != nil {
		return err
	}

	table := r.getTableName(dimension)
	const pageSize = 64
	offset := 0

	for {
		stmt := fmt.Sprintf(
			"SELECT %s FROM %s "+
				"WHERE knowledge_base_id = ? "+
				"ORDER BY id LIMIT %d OFFSET %d",
			strings.Join(columnsForCopy, ", "),
			quoteIdentifier(table),
			pageSize,
			offset,
		)

		rows, err := r.db.QueryContext(ctx, stmt, sourceKnowledgeBaseID)
		if err != nil {
			return fmt.Errorf("copy indices scan: %w", err)
		}

		batch, err := scanCopyRows(rows)
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}

		var targets []*MysqlVectorEmbedding
		for _, src := range batch {
			targetChunkID, ok := sourceToTargetChunkIDMap[src.ChunkID]
			if !ok {
				continue
			}
			targetKnowledgeID, ok := sourceToTargetKBIDMap[src.KnowledgeID]
			if !ok {
				continue
			}

			targetSourceID := translateSourceID(src.SourceID, src.ChunkID, targetChunkID)
			targets = append(targets, &MysqlVectorEmbedding{
				ID:              uuid.New().String(),
				Content:         src.Content,
				SourceID:        targetSourceID,
				SourceType:      src.SourceType,
				ChunkID:         targetChunkID,
				KnowledgeID:     targetKnowledgeID,
				KnowledgeBaseID: targetKnowledgeBaseID,
				TagID:           src.TagID,
				IsEnabled:       src.IsEnabled,
				Embedding:       src.Embedding,
			})
		}

		if len(targets) > 0 {
			if err := r.insertRows(ctx, table, targets); err != nil {
				return fmt.Errorf("copy indices insert: %w", err)
			}
		}

		if len(batch) < pageSize {
			break
		}
		offset += pageSize
	}

	return nil
}

// translateSourceID 翻译 SourceID。
func translateSourceID(originalSourceID, sourceChunkID, targetChunkID string) string {
	switch {
	case originalSourceID == sourceChunkID:
		return targetChunkID
	case strings.HasPrefix(originalSourceID, sourceChunkID+"-"):
		questionID := strings.TrimPrefix(originalSourceID, sourceChunkID+"-")
		return fmt.Sprintf("%s-%s", targetChunkID, questionID)
	default:
		return uuid.New().String()
	}
}

// toMysqlVectorEmbedding 将 IndexInfo 转换为 MySQL 行模型。
func toMysqlVectorEmbedding(info *types.IndexInfo, params map[string]any) *MysqlVectorEmbedding {
	emb := &MysqlVectorEmbedding{
		ID:              info.ID,
		Content:         info.Content,
		SourceID:        info.SourceID,
		SourceType:      int(info.SourceType),
		ChunkID:         info.ChunkID,
		KnowledgeID:     info.KnowledgeID,
		KnowledgeBaseID: info.KnowledgeBaseID,
		TagID:           info.TagID,
		IsEnabled:       info.IsEnabled,
	}
	if params != nil {
		if v, ok := params["embedding"]; ok {
			if m, ok := v.(map[string][]float32); ok {
				emb.Embedding = m[info.SourceID]
			}
		}
	}
	return emb
}

// scanRetrieveRows 扫描检索结果行。
func scanRetrieveRows(rows *sql.Rows, matchType types.MatchType) ([]*types.IndexWithScore, error) {
	var out []*types.IndexWithScore
	for rows.Next() {
		var (
			id                                  string
			content, sourceID, chunkID          sql.NullString
			knowledgeID, knowledgeBaseID, tagID sql.NullString
			sourceType                          sql.NullInt64
			isEnabled                           sql.NullBool
			score                               float64
		)
		err := rows.Scan(&id, &content, &sourceID, &sourceType,
			&chunkID, &knowledgeID, &knowledgeBaseID, &tagID, &isEnabled, &score)
		if err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		out = append(out, &types.IndexWithScore{
			ID:              id,
			Content:         content.String,
			SourceID:        sourceID.String,
			SourceType:      types.SourceType(sourceType.Int64),
			ChunkID:         chunkID.String,
			KnowledgeID:     knowledgeID.String,
			KnowledgeBaseID: knowledgeBaseID.String,
			TagID:           tagID.String,
			Score:           score,
			MatchType:       matchType,
			IsEnabled:       !isEnabled.Valid || isEnabled.Bool,
		})
	}
	return out, rows.Err()
}

// scanCopyRows 扫描 CopyIndices 的行。
func scanCopyRows(rows *sql.Rows) ([]*MysqlVectorEmbedding, error) {
	var out []*MysqlVectorEmbedding
	for rows.Next() {
		var (
			id                                  string
			content, sourceID, chunkID          sql.NullString
			knowledgeID, knowledgeBaseID, tagID sql.NullString
			sourceType                          sql.NullInt64
			isEnabled                           sql.NullBool
			embeddingRaw                        []byte
		)
		if err := rows.Scan(&id, &content, &sourceID, &sourceType,
			&chunkID, &knowledgeID, &knowledgeBaseID, &tagID, &isEnabled, &embeddingRaw); err != nil {
			return nil, fmt.Errorf("scan copy row: %w", err)
		}
		var vec []float32
		if len(embeddingRaw) > 0 {
			var err error
			vec, err = parseEmbeddingJSON(embeddingRaw)
			if err != nil {
				return nil, fmt.Errorf("parse embedding: %w", err)
			}
		}
		out = append(out, &MysqlVectorEmbedding{
			ID:              id,
			Content:         content.String,
			SourceID:        sourceID.String,
			SourceType:      int(sourceType.Int64),
			ChunkID:         chunkID.String,
			KnowledgeID:     knowledgeID.String,
			KnowledgeBaseID: knowledgeBaseID.String,
			TagID:           tagID.String,
			IsEnabled:       !isEnabled.Valid || isEnabled.Bool,
			Embedding:       vec,
		})
	}
	return out, rows.Err()
}

// buildRetrieveResult 构建检索结果。
func buildRetrieveResult(results []*types.IndexWithScore, retrieverType types.RetrieverType) []*types.RetrieveResult {
	return []*types.RetrieveResult{{
		Results:             results,
		RetrieverEngineType: types.MySQLRetrieverEngineType,
		RetrieverType:       retrieverType,
		Error:               nil,
	}}
}
