package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strconv"
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
	size += int64(len(emb.Content))
	size += int64(len(emb.SourceID))
	size += int64(len(emb.ChunkID))
	size += int64(len(emb.KnowledgeID))
	size += int64(len(emb.KnowledgeBaseID))
	size += int64(len(emb.TagID))
	size += 8 // source_type int
	// 向量: dim * 4 bytes
	if len(emb.Embedding) > 0 {
		size += int64(len(emb.Embedding) * 4)
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
		if len(emb.Embedding) == 0 {
			continue
		}
		// 稳定主键
		if emb.ID == "" {
			emb.ID = emb.SourceID
		}
		if emb.ID == "" {
			emb.ID = uuid.New().String()
		}
		dim := len(emb.Embedding)
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

// insertRows 批量插入数据。
func (r *mysqlRepository) insertRows(
	ctx context.Context,
	table string,
	rows []*MysqlVectorEmbedding,
) error {
	if len(rows) == 0 {
		return nil
	}

	// 9 个占位符 + 1 个 embedding JSON 字面量
	const perRowPlaceholders = "(?, ?, ?, ?, ?, ?, ?, ?, ?, %s)"

	parts := make([]string, len(rows))
	args := make([]interface{}, 0, len(rows)*9)
	for i, e := range rows {
		parts[i] = fmt.Sprintf(perRowPlaceholders, embeddingLiteral(e.Embedding))
		args = append(args,
			e.ID, e.Content, e.SourceID, e.SourceType,
			e.ChunkID, e.KnowledgeID, e.KnowledgeBaseID, e.TagID,
			e.IsEnabled,
		)
	}

	stmt := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES %s ON DUPLICATE KEY UPDATE id=id",
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

	dim := len(params.Embedding)
	table := r.getTableName(dim)

	exists, err := r.tableExists(ctx, table)
	if err != nil {
		return nil, fmt.Errorf("check table %s: %w", table, err)
	}
	if !exists {
		return buildRetrieveResult(nil, types.VectorRetrieverType), nil
	}

	// 构建过滤条件
	wb := buildVectorWhereClause(params)
	whereClause, whereArgs := wb.build()

	scoreExpr := cosineSimilarityExpr("embedding", params.Embedding)
	stmt := fmt.Sprintf(
		"SELECT %s, %s AS score "+
			"FROM %s "+
			"WHERE %s AND embedding IS NOT NULL "+
			"HAVING score >= ? AND score IS NOT NULL "+
			"ORDER BY score DESC "+
			"LIMIT %d",
		strings.Join(columnsForRetrieve, ", "),
		scoreExpr,
		quoteIdentifier(table),
		whereClause,
		normalizeTopK(params.TopK),
	)

	args := append(whereArgs, params.Threshold)

	rows, err := r.db.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("vector retrieve: %w", err)
	}
	defer rows.Close()

	results, err := scanRetrieveRows(rows, types.MatchTypeEmbedding)
	if err != nil {
		return nil, err
	}

	return buildRetrieveResult(results, types.VectorRetrieverType), nil
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
	wb.add("(is_enabled IS NULL OR is_enabled = TRUE)")

	return wb
}

func cosineSimilarityExpr(column string, embedding []float32) string {
	if len(embedding) == 0 {
		return "0"
	}

	dotTerms := make([]string, 0, len(embedding))
	storedNormTerms := make([]string, 0, len(embedding))
	var queryNormSq float64
	for i, v := range embedding {
		value := strconv.FormatFloat(float64(v), 'g', -1, 32)
		component := fmt.Sprintf(
			"COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(%s, '$[%d]')) AS DECIMAL(30,15)), 0)",
			column,
			i,
		)
		dotTerms = append(dotTerms, fmt.Sprintf("(%s * %s)", component, value))
		storedNormTerms = append(storedNormTerms, fmt.Sprintf("POW(%s, 2)", component))
		queryNormSq += float64(v) * float64(v)
	}

	queryNorm := math.Sqrt(queryNormSq)
	if queryNorm == 0 {
		return "0"
	}

	dot := strings.Join(dotTerms, " + ")
	storedNorm := strings.Join(storedNormTerms, " + ")
	queryNormLiteral := strconv.FormatFloat(queryNorm, 'g', -1, 64)
	return fmt.Sprintf(
		"(CASE WHEN SQRT(%s) = 0 THEN 0 ELSE (%s) / (SQRT(%s) * %s) END)",
		storedNorm,
		dot,
		storedNorm,
		queryNormLiteral,
	)
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
			"ORDER BY score DESC "+
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
	return stmt, args
}

func limitTopKByScore(rows []*types.IndexWithScore, topK int) []*types.IndexWithScore {
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].Score > rows[j].Score
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
			id, content, sourceID, chunkID      string
			knowledgeID, knowledgeBaseID, tagID string
			sourceType                          int
			isEnabled                           bool
			score                               float64
		)
		err := rows.Scan(&id, &content, &sourceID, &sourceType,
			&chunkID, &knowledgeID, &knowledgeBaseID, &tagID, &isEnabled, &score)
		if err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		out = append(out, &types.IndexWithScore{
			ID:              id,
			Content:         content,
			SourceID:        sourceID,
			SourceType:      types.SourceType(sourceType),
			ChunkID:         chunkID,
			KnowledgeID:     knowledgeID,
			KnowledgeBaseID: knowledgeBaseID,
			TagID:           tagID,
			Score:           score,
			MatchType:       matchType,
			IsEnabled:       isEnabled,
		})
	}
	return out, rows.Err()
}

// scanCopyRows 扫描 CopyIndices 的行。
func scanCopyRows(rows *sql.Rows) ([]*MysqlVectorEmbedding, error) {
	var out []*MysqlVectorEmbedding
	for rows.Next() {
		var (
			id, content, sourceID, chunkID      string
			knowledgeID, knowledgeBaseID, tagID string
			sourceType                          int
			isEnabled                           bool
			embeddingRaw                        []byte
		)
		if err := rows.Scan(&id, &content, &sourceID, &sourceType,
			&chunkID, &knowledgeID, &knowledgeBaseID, &tagID, &isEnabled, &embeddingRaw); err != nil {
			return nil, fmt.Errorf("scan copy row: %w", err)
		}
		vec, err := parseEmbeddingJSON(embeddingRaw)
		if err != nil {
			return nil, fmt.Errorf("parse embedding: %w", err)
		}
		out = append(out, &MysqlVectorEmbedding{
			ID:              id,
			Content:         content,
			SourceID:        sourceID,
			SourceType:      sourceType,
			ChunkID:         chunkID,
			KnowledgeID:     knowledgeID,
			KnowledgeBaseID: knowledgeBaseID,
			TagID:           tagID,
			IsEnabled:       isEnabled,
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
