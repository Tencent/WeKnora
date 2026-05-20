package doris

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

const (
	defaultTableBaseName = "weknora_embeddings"
	envDorisTablePrefix  = "DORIS_TABLE_PREFIX"
)

// NewDorisRetrieveEngineRepository 创建 Doris 检索引擎仓储。
//
// 参数：
//   - db：MySQL 协议的 *sql.DB 实例。调用方负责 SetMaxOpenConns 等参数。
//   - feHTTPBase：Stream Load 用的 FE HTTP 基地址（含 scheme），例如 "http://doris-fe:8030"。
//   - username/password：MySQL 与 Stream Load 共用的凭据。
//   - database：目标数据库名（既用于 MySQL DSN，也用于 Stream Load URL 路径）。
//   - indexCfg：可空。为 nil 时退化为环境变量 + 默认值（env 路径）。
func NewDorisRetrieveEngineRepository(
	db *sql.DB,
	feHTTPBase, username, password, database string,
	indexCfg *types.IndexConfig,
) interfaces.RetrieveEngineRepository {
	log := logger.GetLogger(context.Background())
	log.Info("[Doris] Initializing Doris retriever engine repository")

	tableBaseName := types.ResolveCollectionName(indexCfg, envDorisTablePrefix, defaultTableBaseName)
	compatMode, invalidCompatMode := resolveConfiguredDorisCompatMode()
	if invalidCompatMode != "" {
		log.Warnf("[Doris] Invalid %s=%q, defaulting to %s", envDorisCompatMode, invalidCompatMode, dorisCompatModeAuto)
	}

	repo := &dorisRepository{
		db:             db,
		httpClient:     &http.Client{},
		feHTTPBase:     strings.TrimRight(feHTTPBase, "/"),
		username:       username,
		password:       password,
		database:       database,
		tableBaseName:  tableBaseName,
		bucketsNum:     indexCfg.GetBucketsNum(0),
		replicationNum: indexCfg.GetReplicationNum(0),
		compatModeRequested: compatMode,
	}
	log.Infof("[Doris] Repository initialized: db=%s, base=%s, fe_http=%s, compat_mode=%s",
		database, tableBaseName, repo.feHTTPBase, repo.compatModeRequested)
	if os.Getenv(envDorisCompatMode) == "" {
		log.Infof("[Doris] %s not set, defaulting to %s and probing on first use", envDorisCompatMode, dorisCompatModeAuto)
	}
	return repo
}

func (r *dorisRepository) EngineType() types.RetrieverEngineType {
	return types.DorisRetrieverEngineType
}

func (r *dorisRepository) Support() []types.RetrieverType {
	return []types.RetrieverType{types.KeywordsRetrieverType, types.VectorRetrieverType}
}

// EstimateStorageSize 估算给定 IndexInfo 列表的存储字节数。
//
// 参考 Qdrant 的算法：payload 字段长度 + 向量字节 + HNSW 邻居 + 元数据。
func (r *dorisRepository) EstimateStorageSize(_ context.Context,
	indexInfoList []*types.IndexInfo, params map[string]any,
) int64 {
	var total int64
	for _, info := range indexInfoList {
		emb := toDorisVectorEmbedding(info, params, dorisCompatModeInnerProductDuplicate)
		total += calculateStorageSize(emb)
	}
	return total
}

// Save 写入单条记录到对应维度的表。空向量在 BatchSave 内部统一拒绝。
func (r *dorisRepository) Save(ctx context.Context,
	info *types.IndexInfo, additionalParams map[string]any,
) error {
	return r.BatchSave(ctx, []*types.IndexInfo{info}, additionalParams)
}

// BatchSave 把同一批 IndexInfo 按维度分组；Doris ANN 兼容表使用
// DUPLICATE KEY，因此这里显式按 id 做 delete + insert，保持原先的替换语义。
func (r *dorisRepository) BatchSave(ctx context.Context,
	indexInfoList []*types.IndexInfo, additionalParams map[string]any,
) error {
	log := logger.GetLogger(ctx)
	if len(indexInfoList) == 0 {
		return nil
	}
	compatMode, err := r.resolveCompatMode(ctx)
	if err != nil {
		return err
	}

	groups := make(map[int][]*DorisVectorEmbedding)
	for _, info := range indexInfoList {
		emb := toDorisVectorEmbedding(info, additionalParams, compatMode)
		if len(emb.Embedding) == 0 {
			log.Warnf("[Doris] Skipping empty embedding for chunk %s", info.ChunkID)
			continue
		}
		if err := validateEmbedding(emb.Embedding); err != nil {
			return fmt.Errorf("invalid embedding for chunk %s: %w", info.ChunkID, err)
		}
		// 给一个稳定的主键。SourceID 是上层最有意义的"行身份"，
		// 但同 chunk 多 question 的场景下 SourceID 已经唯一，所以直接用它。
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
		if compatMode.usesReplaceWrite() {
			err = r.replaceRows(ctx, r.getTableName(dim), rows)
		} else {
			err = r.insertRows(ctx, r.getTableName(dim), rows)
		}
		if err != nil {
			return fmt.Errorf("batch save dim=%d: %w", dim, err)
		}
		log.Infof("[Doris] Saved %d rows to %s", len(rows), r.getTableName(dim))
	}
	return nil
}

// insertRows 按列序拼一条多 VALUES 的 INSERT。embedding 列由于
// go-sql-driver/mysql 不支持 ARRAY 占位符，必须以字面量形式拼到 SQL 文本中。
func (r *dorisRepository) insertRows(ctx context.Context,
	table string, rows []*DorisVectorEmbedding,
) error {
	if len(rows) == 0 {
		return nil
	}

	// 9 个普通占位符 + 1 个 embedding 字面量。
	const perRowPlaceholders = "(?, ?, ?, ?, ?, ?, ?, ?, ?, %s)"

	parts := make([]string, len(rows))
	args := make([]any, 0, len(rows)*9)
	for i, e := range rows {
		parts[i] = fmt.Sprintf(perRowPlaceholders, embeddingLiteral(e.Embedding))
		args = append(args,
			e.ID, e.Content, e.SourceID, e.SourceType,
			e.ChunkID, e.KnowledgeID, e.KnowledgeBaseID, e.TagID,
			e.IsEnabled,
		)
	}

	stmt := fmt.Sprintf("INSERT INTO `%s` (%s) VALUES %s",
		table,
		strings.Join(columns, ", "),
		strings.Join(parts, ", "),
	)
	_, err := r.db.ExecContext(ctx, stmt, args...)
	return err
}

// replaceRows 在 DUPLICATE KEY 表上显式模拟“按 id 覆盖”的语义。
func (r *dorisRepository) replaceRows(ctx context.Context,
	table string, rows []*DorisVectorEmbedding,
) error {
	rows = dedupeRowsByID(rows)
	if len(rows) == 0 {
		return nil
	}

	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	if err := r.deleteRowsByID(ctx, table, ids); err != nil {
		return err
	}
	return r.insertRows(ctx, table, rows)
}

func (r *dorisRepository) deleteRowsByID(ctx context.Context,
	table string, ids []string,
) error {
	if len(ids) == 0 {
		return nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	stmt := fmt.Sprintf("DELETE FROM `%s` WHERE %s IN (%s)",
		table, fieldID, strings.Join(placeholders, ", "))
	_, err := r.db.ExecContext(ctx, stmt, args...)
	return err
}

func dedupeRowsByID(rows []*DorisVectorEmbedding) []*DorisVectorEmbedding {
	if len(rows) < 2 {
		return rows
	}

	out := make([]*DorisVectorEmbedding, 0, len(rows))
	positions := make(map[string]int, len(rows))
	for _, row := range rows {
		if idx, ok := positions[row.ID]; ok {
			out[idx] = row
			continue
		}
		positions[row.ID] = len(out)
		out = append(out, row)
	}
	return out
}

// DeleteByChunkIDList 用 chunk_id 列删除。dimension 用于定位具体表。
func (r *dorisRepository) DeleteByChunkIDList(ctx context.Context,
	chunkIDList []string, dimension int, _ string,
) error {
	return r.deleteByField(ctx, fieldChunkID, chunkIDList, dimension)
}

// DeleteByKnowledgeIDList 用 knowledge_id 列删除。
func (r *dorisRepository) DeleteByKnowledgeIDList(ctx context.Context,
	knowledgeIDList []string, dimension int, _ string,
) error {
	return r.deleteByField(ctx, fieldKnowledgeID, knowledgeIDList, dimension)
}

// DeleteBySourceIDList 用 source_id 列删除。
func (r *dorisRepository) DeleteBySourceIDList(ctx context.Context,
	sourceIDList []string, dimension int, _ string,
) error {
	return r.deleteByField(ctx, fieldSourceID, sourceIDList, dimension)
}

// deleteByField 是三个 Delete* 方法的统一实现：
// DELETE FROM <table> WHERE <field> IN (?, ?, ...)。
func (r *dorisRepository) deleteByField(ctx context.Context,
	field string, ids []string, dimension int,
) error {
	log := logger.GetLogger(ctx)
	if len(ids) == 0 {
		return nil
	}

	table := r.getTableName(dimension)
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, v := range ids {
		placeholders[i] = "?"
		args[i] = v
	}
	stmt := fmt.Sprintf("DELETE FROM `%s` WHERE %s IN (%s)",
		table, field, strings.Join(placeholders, ", "))

	if _, err := r.db.ExecContext(ctx, stmt, args...); err != nil {
		log.Errorf("[Doris] Delete by %s failed: %v", field, err)
		return fmt.Errorf("delete by %s: %w", field, err)
	}
	log.Infof("[Doris] Deleted %d rows from %s by %s", len(ids), table, field)
	return nil
}

// Retrieve 根据 RetrieverType 分发到向量检索或关键词检索。
func (r *dorisRepository) Retrieve(ctx context.Context,
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

// VectorRetrieve 对查询向量先做单位化，再调用 inner_product_approximate 做
// ANN 搜索；对单位向量而言，inner product 与 cosine similarity 等价，
// 因此 score 仍然保持“越大越相似”的语义。
func (r *dorisRepository) VectorRetrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	if err := validateEmbedding(params.Embedding); err != nil {
		return nil, fmt.Errorf("invalid query embedding: %w", err)
	}
	compatMode, err := r.resolveCompatMode(ctx)
	if err != nil {
		return nil, err
	}
	queryEmbedding := append([]float32(nil), params.Embedding...)
	if compatMode.normalizeEmbeddings() {
		queryEmbedding = normalizeEmbedding(queryEmbedding)
	}
	dim := len(params.Embedding)
	table := r.getTableName(dim)

	exists, err := r.tableExists(ctx, table)
	if err != nil {
		return nil, fmt.Errorf("check table %s: %w", table, err)
	}
	if !exists {
		log.Warnf("[Doris] Table %s does not exist, returning empty results", table)
		return buildRetrieveResult(nil, types.VectorRetrieverType), nil
	}

	wb := buildBaseFilter(params)
	whereClause, whereArgs := wb.build()

	// embedding 必须用字面量，threshold/topK 用占位符。
	// 使用 HAVING 是因为 score 是 SELECT 列别名，WHERE 阶段还看不到。
	scoreExpr := fmt.Sprintf("inner_product_approximate(`%s`, %s)", fieldEmbedding, embeddingLiteral(queryEmbedding))
	if compatMode == dorisCompatModeLegacy {
		scoreExpr = fmt.Sprintf("(1 - cosine_distance_approximate(`%s`, %s))", fieldEmbedding, embeddingLiteral(queryEmbedding))
	}
	stmt := fmt.Sprintf(
		"SELECT %s, %s AS score "+
			"FROM `%s` WHERE %s "+
			"HAVING score >= ? "+
			"ORDER BY score DESC LIMIT ?",
		strings.Join(columnsForRetrieve, ", "),
		scoreExpr,
		table,
		whereClause,
	)
	args := append(whereArgs, params.Threshold, params.TopK)

	rows, err := r.db.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, r.wrapVectorRetrieveError(table, compatMode, err)
	}
	defer rows.Close()

	results, err := scanRetrieveRows(rows, types.MatchTypeEmbedding)
	if err != nil {
		return nil, err
	}
	log.Infof("[Doris] Vector retrieval found %d results in %s", len(results), table)
	return buildRetrieveResult(results, types.VectorRetrieverType), nil
}

// KeywordsRetrieve 用 Doris 倒排索引 + MATCH_ANY 做关键词匹配。
//
// 不需要 jieba 客户端分词：CREATE TABLE 时 idx_content 已经声明了 chinese parser。
// 不同维度的表跨表合并取 topK，与 Milvus/Weaviate 现状一致。
func (r *dorisRepository) KeywordsRetrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
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

	wb := buildBaseFilter(params)
	whereClause, whereArgs := wb.build()

	var all []*types.IndexWithScore
	for _, table := range tables {
		stmt := fmt.Sprintf(
			"SELECT %s FROM `%s` WHERE %s AND %s MATCH_ANY ? LIMIT ?",
			strings.Join(columnsForRetrieve, ", "),
			table, whereClause, fieldContent,
		)
		args := append(append([]any{}, whereArgs...), query, params.TopK)

		rows, err := r.db.QueryContext(ctx, stmt, args...)
		if err != nil {
			log.Warnf("[Doris] Keyword retrieve in %s failed: %v", table, err)
			continue
		}
		// score 在 KeywordsRetrieve 中固定 1.0，与 Qdrant 行为一致。
		batch, scanErr := scanRetrieveRows(rows, types.MatchTypeKeywords)
		_ = rows.Close()
		if scanErr != nil {
			return nil, scanErr
		}
		all = append(all, batch...)
	}
	if len(all) > params.TopK {
		all = all[:params.TopK]
	}
	log.Infof("[Doris] Keywords retrieval found %d results across %d tables", len(all), len(tables))
	return buildRetrieveResult(all, types.KeywordsRetrieverType), nil
}

// CopyIndices 把源知识库的 chunk 复制到目标知识库，避免重新生成 embedding。
//
// 与 Qdrant 的实现完全镜像：
//   - 分页扫描源表
//   - 按 sourceToTargetChunkIDMap 把 chunk_id 翻译过去
//   - 处理 source_id 翻译规则（普通 chunk / 生成型问题 / 其他）
//   - 把目标行写回同一个表
func (r *dorisRepository) CopyIndices(ctx context.Context,
	sourceKnowledgeBaseID string,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
	dimension int,
	_ string,
) error {
	log := logger.GetLogger(ctx)
	if len(sourceToTargetChunkIDMap) == 0 {
		return nil
	}
	if err := r.ensureTable(ctx, dimension); err != nil {
		return err
	}

	table := r.getTableName(dimension)
	const pageSize = 64
	offset := 0
	totalCopied := 0

	for {
		stmt := fmt.Sprintf(
			"SELECT %s FROM `%s` WHERE %s = ? ORDER BY %s LIMIT ? OFFSET ?",
			strings.Join(columnsForCopy, ", "),
			table, fieldKnowledgeBaseID, fieldID,
		)
		rows, err := r.db.QueryContext(ctx, stmt, sourceKnowledgeBaseID, pageSize, offset)
		if err != nil {
			return fmt.Errorf("copy indices scan: %w", err)
		}
		batch, err := scanCopyRows(rows)
		_ = rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}

		var targets []*DorisVectorEmbedding
		for _, src := range batch {
			targetChunkID, ok := sourceToTargetChunkIDMap[src.ChunkID]
			if !ok {
				log.Warnf("[Doris] Source chunk %s not in target mapping", src.ChunkID)
				continue
			}
			targetKnowledgeID, ok := sourceToTargetKBIDMap[src.KnowledgeID]
			if !ok {
				log.Warnf("[Doris] Source knowledge %s not in target mapping", src.KnowledgeID)
				continue
			}

			targetSourceID := translateSourceID(src.SourceID, src.ChunkID, targetChunkID)
			targets = append(targets, &DorisVectorEmbedding{
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
			totalCopied += len(targets)
		}

		if len(batch) < pageSize {
			break
		}
		offset += pageSize
	}
	log.Infof("[Doris] CopyIndices done, dim=%d, copied=%d", dimension, totalCopied)
	return nil
}

// BatchUpdateChunkEnabledStatus / BatchUpdateChunkTagID 实际实现位于 streamload.go，
// 会按 compat mode 选择 partial update 或 rewrite rows。

// ---------------------------------------------------------------------------
// 私有辅助
// ---------------------------------------------------------------------------

// toDorisVectorEmbedding 把 IndexInfo + 上层传入的 embedding 映射 转换为
// Doris 行模型。Embedding 通过 additionalParams[fieldEmbedding] 中的
// map[string][]float32 按 SourceID 取出，与 Qdrant/Milvus 完全一致。
// inner_product_duplicate 模式会先单位化，legacy 模式保留原始向量。
func toDorisVectorEmbedding(
	info *types.IndexInfo,
	additionalParams map[string]any,
	compatMode dorisCompatMode,
) *DorisVectorEmbedding {
	emb := &DorisVectorEmbedding{
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
	if additionalParams != nil {
		if v, ok := additionalParams[fieldEmbedding]; ok {
			if m, ok := v.(map[string][]float32); ok {
				emb.Embedding = append([]float32(nil), m[info.SourceID]...)
				if compatMode.normalizeEmbeddings() {
					emb.Embedding = normalizeEmbedding(emb.Embedding)
				}
			}
		}
	}
	return emb
}

func (r *dorisRepository) wrapVectorRetrieveError(table string, compatMode dorisCompatMode, err error) error {
	if compatMode == dorisCompatModeLegacy {
		return fmt.Errorf(
			"vector retrieve %s in Doris compat mode %s: %w. If your Doris build does not support cosine_distance_approximate or ANN on UNIQUE KEY tables, set %s=%s before creating embedding tables. %s is not interchangeable after %s_* tables are created",
			table,
			compatMode,
			err,
			envDorisCompatMode,
			dorisCompatModeInnerProductDuplicate,
			envDorisCompatMode,
			r.tableBaseName,
		)
	}
	return fmt.Errorf("vector retrieve %s in Doris compat mode %s: %w", table, compatMode, err)
}

// translateSourceID 把源 SourceID 翻译到目标 SourceID，与 Qdrant 实现完全镜像：
//   - 普通 chunk：SourceID == ChunkID  -> 使用 targetChunkID
//   - 生成型问题：SourceID == "<chunkID>-<questionID>" -> "<targetChunkID>-<questionID>"
//   - 其他场景：生成新的 UUID（保持唯一性）
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

// scanRetrieveRows 把 Retrieve 阶段的 rows 反序列化为 IndexWithScore 列表。
//
// 分两路：
//   - 列数 == columnsForRetrieve+1：第 N+1 列是 score（向量检索路径）
//   - 列数 == columnsForRetrieve：score 统一赋 1.0（关键词检索路径）
func scanRetrieveRows(rows *sql.Rows, matchType types.MatchType) ([]*types.IndexWithScore, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	withScore := len(cols) == len(columnsForRetrieve)+1

	var out []*types.IndexWithScore
	for rows.Next() {
		var (
			id, content, sourceID, chunkID      string
			knowledgeID, knowledgeBaseID, tagID string
			sourceType                          int
			isEnabled                           bool
			score                               float64
			err                                 error
		)
		if withScore {
			err = rows.Scan(&id, &content, &sourceID, &sourceType,
				&chunkID, &knowledgeID, &knowledgeBaseID, &tagID, &isEnabled, &score)
		} else {
			err = rows.Scan(&id, &content, &sourceID, &sourceType,
				&chunkID, &knowledgeID, &knowledgeBaseID, &tagID, &isEnabled)
			score = 1.0
		}
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
		})
	}
	return out, rows.Err()
}

// scanCopyRows 反序列化 CopyIndices 的分页查询结果。
//
// 与 scanRetrieveRows 不同，这里需要 embedding 字段（去复制原始向量）。
// Doris 的 ARRAY<FLOAT> 通过 mysql 协议返回的是字符串字面量 "[1,2,3]"。
func scanCopyRows(rows *sql.Rows) ([]*DorisVectorEmbedding, error) {
	var out []*DorisVectorEmbedding
	for rows.Next() {
		var (
			id, content, sourceID, chunkID      string
			knowledgeID, knowledgeBaseID, tagID string
			sourceType                          int
			isEnabled                           bool
			embeddingRaw                        sql.RawBytes
		)
		if err := rows.Scan(&id, &content, &sourceID, &sourceType,
			&chunkID, &knowledgeID, &knowledgeBaseID, &tagID, &isEnabled, &embeddingRaw); err != nil {
			return nil, fmt.Errorf("scan copy row: %w", err)
		}
		vec, err := parseEmbeddingLiteral(embeddingRaw)
		if err != nil {
			return nil, fmt.Errorf("parse embedding: %w", err)
		}
		out = append(out, &DorisVectorEmbedding{
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

// buildRetrieveResult 把 IndexWithScore 列表包装成 RetrieveResult。
func buildRetrieveResult(results []*types.IndexWithScore, retrieverType types.RetrieverType) []*types.RetrieveResult {
	return []*types.RetrieveResult{{
		Results:             results,
		RetrieverEngineType: types.DorisRetrieverEngineType,
		RetrieverType:       retrieverType,
		Error:               nil,
	}}
}

// calculateStorageSize 估算单行的存储成本。
//
// 与 Qdrant 一致：payload 字符串字节 + 向量 (dim*4) + HNSW M*2*8 + 元数据 24。
func calculateStorageSize(emb *DorisVectorEmbedding) int64 {
	var payload int64
	payload += int64(len(emb.Content))
	payload += int64(len(emb.SourceID))
	payload += int64(len(emb.ChunkID))
	payload += int64(len(emb.KnowledgeID))
	payload += int64(len(emb.KnowledgeBaseID))
	payload += int64(len(emb.TagID))
	payload += 8 // source_type int

	var vec int64
	var hnsw int64
	if len(emb.Embedding) > 0 {
		vec = int64(len(emb.Embedding)) * 4
		const hnswM = 32
		hnsw = hnswM * 2 * 8
	}
	const metaBytes int64 = 24
	return payload + vec + hnsw + metaBytes
}
