package clickhouse

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/ClickHouse/clickhouse-go/v2" // 注册 database/sql 驱动 "clickhouse"（原生协议）
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

const (
	defaultTableBaseName     = "weknora_embeddings"
	envClickhouseTablePrefix = "CLICKHOUSE_TABLE_PREFIX"
)

// BuildDSN 拼出 clickhouse-go database/sql 驱动用的 DSN。
// Addr 是原生协议地址（host:9000），与 Doris 的 Addr 约定一致。
// 用户名/密码经 URL 编码后放进 DSN，避免特殊字符破坏解析。
func BuildDSN(addr, username, password, database string) string {
	u := &url.URL{
		Scheme: "clickhouse",
		Host:   addr,
		Path:   "/" + strings.TrimPrefix(database, "/"),
	}
	if username != "" {
		u.User = url.UserPassword(username, password)
	}
	return u.String()
}

// NewClickhouseRetrieveEngineRepository 创建 ClickHouse 检索引擎仓储。
//
// 参数：
//   - db：clickhouse-go 驱动的 *sql.DB 实例。调用方负责 SetMaxOpenConns 等参数。
//   - database：目标数据库名（建表、system.tables 查询都以它为准）。
//   - indexCfg：可空。为 nil 时退化为环境变量 + 默认值（env 路径）。
func NewClickhouseRetrieveEngineRepository(
	db *sql.DB,
	database string,
	indexCfg *types.IndexConfig,
) interfaces.RetrieveEngineRepository {
	log := logger.GetLogger(context.Background())
	log.Info("[ClickHouse] Initializing ClickHouse retriever engine repository")

	tableBaseName := types.ResolveCollectionName(indexCfg, envClickhouseTablePrefix, defaultTableBaseName)
	repo := &clickhouseRepository{
		db:            db,
		database:      database,
		tableBaseName: tableBaseName,
	}
	log.Infof("[ClickHouse] Repository initialized: db=%s, base=%s", database, tableBaseName)
	return repo
}

func (r *clickhouseRepository) EngineType() types.RetrieverEngineType {
	return types.ClickhouseRetrieverEngineType
}

// Support 只返回 vector：PR1 只做 dense 检索。keyword/hybrid 与
// metadata filter 在后续 PR 落地，届时再把 KeywordsRetrieverType 加回来。
func (r *clickhouseRepository) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

// EstimateStorageSize 估算给定 IndexInfo 列表的存储字节数。
//
// 参考 Doris/Qdrant 的算法：payload 字段长度 + 向量字节 + HNSW 邻居 + 元数据。
// ClickHouse 原生 HNSW 默认 M=32（bf16 量化、ef_construction=128）。
func (r *clickhouseRepository) EstimateStorageSize(_ context.Context,
	indexInfoList []*types.IndexInfo, params map[string]any,
) int64 {
	var total int64
	for _, info := range indexInfoList {
		emb := toClickhouseVectorEmbedding(info, params)
		total += calculateStorageSize(emb)
	}
	return total
}

// Save 写入单条记录到对应维度的表。空向量在 BatchSave 内部统一拒绝。
func (r *clickhouseRepository) Save(ctx context.Context,
	info *types.IndexInfo, additionalParams map[string]any,
) error {
	return r.BatchSave(ctx, []*types.IndexInfo{info}, additionalParams)
}

// BatchSave 把同一批 IndexInfo 按维度分组写入。
// ReplacingMergeTree 按 id 去重，因此直接 INSERT 即有替换语义，
// 不需要像 Doris 那样先 delete 再 insert。
func (r *clickhouseRepository) BatchSave(ctx context.Context,
	indexInfoList []*types.IndexInfo, additionalParams map[string]any,
) error {
	log := logger.GetLogger(ctx)
	if len(indexInfoList) == 0 {
		return nil
	}

	groups := make(map[int][]*ClickhouseVectorEmbedding)
	for _, info := range indexInfoList {
		emb := toClickhouseVectorEmbedding(info, additionalParams)
		if len(emb.Embedding) == 0 {
			log.Warnf("[ClickHouse] Skipping empty embedding for chunk %s", info.ChunkID)
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
		if err := r.insertRows(ctx, r.getTableName(dim), rows); err != nil {
			return fmt.Errorf("batch save dim=%d: %w", dim, err)
		}
		log.Infof("[ClickHouse] Saved %d rows to %s", len(rows), r.getTableName(dim))
	}
	return nil
}

// insertRows 按列序拼一条多 VALUES 的 INSERT。embedding 列以字面量形式
// 拼到 SQL 文本中（见 embeddingLiteral 的说明）。
func (r *clickhouseRepository) insertRows(ctx context.Context,
	table string, rows []*ClickhouseVectorEmbedding,
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
			boolToUInt8(e.IsEnabled),
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

// DeleteByChunkIDList 用 chunk_id 列删除。dimension 用于定位具体表。
//
// 注意：ClickHouse 的 ALTER TABLE ... DELETE 是异步 mutation，
// 删除在后台合并时才生效；刚删除的 chunk 在 mutation 完成前仍可能
// 被检索返回。一致性硬化（读时过滤已删行等）在后续 PR 做。
func (r *clickhouseRepository) DeleteByChunkIDList(ctx context.Context,
	chunkIDList []string, dimension int, _ string,
) error {
	return r.deleteByField(ctx, fieldChunkID, chunkIDList, dimension)
}

// DeleteByKnowledgeIDList 用 knowledge_id 列删除。
func (r *clickhouseRepository) DeleteByKnowledgeIDList(ctx context.Context,
	knowledgeIDList []string, dimension int, _ string,
) error {
	return r.deleteByField(ctx, fieldKnowledgeID, knowledgeIDList, dimension)
}

// DeleteBySourceIDList 用 source_id 列删除。
func (r *clickhouseRepository) DeleteBySourceIDList(ctx context.Context,
	sourceIDList []string, dimension int, _ string,
) error {
	return r.deleteByField(ctx, fieldSourceID, sourceIDList, dimension)
}

// deleteByField 是三个 Delete* 方法的统一实现：
// ALTER TABLE <table> DELETE WHERE <field> IN (?, ?, ...)。
// ClickHouse 不支持 DELETE FROM 语法，删除只能走 ALTER TABLE mutation。
func (r *clickhouseRepository) deleteByField(ctx context.Context,
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
	stmt := fmt.Sprintf("ALTER TABLE `%s` DELETE WHERE %s IN (%s)",
		table, field, strings.Join(placeholders, ", "))

	if _, err := r.db.ExecContext(ctx, stmt, args...); err != nil {
		log.Errorf("[ClickHouse] Delete by %s failed: %v", field, err)
		return fmt.Errorf("delete by %s: %w", field, err)
	}
	log.Infof("[ClickHouse] Deleted %d rows from %s by %s", len(ids), table, field)
	return nil
}

// Retrieve 根据 RetrieverType 分发。PR1 只实现向量检索；
// 关键词检索在后续 PR 落地，此处直接报错而不是静默返回空结果。
func (r *clickhouseRepository) Retrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	switch params.RetrieverType {
	case types.VectorRetrieverType:
		return r.VectorRetrieve(ctx, params)
	default:
		return nil, fmt.Errorf("clickhouse retriever does not support retriever type %v in PR1 (keywords/hybrid land in follow-up PRs, see #5722)", params.RetrieverType)
	}
}

// VectorRetrieve 对查询向量用 cosineDistance 做 ANN 搜索；
// score = 1 - cosineDistance，与其他引擎一致的 cosine 相似度语义
// （越大越相似）。表不存在时返回空结果，与 Doris 行为一致。
func (r *clickhouseRepository) VectorRetrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	if err := validateEmbedding(params.Embedding); err != nil {
		return nil, fmt.Errorf("invalid query embedding: %w", err)
	}
	queryLiteral := embeddingLiteral(params.Embedding)
	dim := len(params.Embedding)
	table := r.getTableName(dim)

	exists, err := r.tableExists(ctx, table)
	if err != nil {
		return nil, fmt.Errorf("check table %s: %w", table, err)
	}
	if !exists {
		log.Warnf("[ClickHouse] Table %s does not exist, returning empty results", table)
		return buildRetrieveResult(nil, types.VectorRetrieverType), nil
	}

	wb := buildBaseFilter(params)
	whereClause, whereArgs := wb.build()

	// FINAL：ReplacingMergeTree 的去重是后台异步的，FINAL 让查询时
	// 同一 id 只保留最新写入的一行。ORDER BY 直接用 cosineDistance
	// 升序（等价于 score 降序），这是 ClickHouse 命中 vector_similarity
	// 索引的写法；HAVING 在 LIMIT 之前按相似度阈值过滤。
	// TopK 直接内联为字面量（int，来源受控），与 Doris 的做法一致。
	scoreExpr := fmt.Sprintf("1 - cosineDistance(`%s`, %s)", fieldEmbedding, queryLiteral)
	stmt := fmt.Sprintf(
		"SELECT %s, %s AS score "+
			"FROM `%s` FINAL WHERE %s "+
			"HAVING score >= ? "+
			"ORDER BY cosineDistance(`%s`, %s) ASC LIMIT %d",
		strings.Join(columnsForRetrieve, ", "),
		scoreExpr,
		table,
		whereClause,
		fieldEmbedding, queryLiteral,
		params.TopK,
	)
	args := append(whereArgs, params.Threshold)

	rows, err := r.db.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("vector retrieve %s: %w", table, err)
	}
	defer rows.Close()

	results, err := scanRetrieveRows(rows, types.MatchTypeEmbedding)
	if err != nil {
		return nil, err
	}
	log.Infof("[ClickHouse] Vector retrieval found %d results in %s", len(results), table)
	return buildRetrieveResult(results, types.VectorRetrieverType), nil
}

// CopyIndices 把源知识库的 chunk 复制到目标知识库，避免重新生成 embedding。
//
// 与 Doris/Qdrant 的实现完全镜像：
//   - 分页扫描源表
//   - 按 sourceToTargetChunkIDMap 把 chunk_id 翻译过去
//   - 处理 source_id 翻译规则（普通 chunk / 生成型问题 / 其他）
//   - 把目标行写回同一个表
func (r *clickhouseRepository) CopyIndices(ctx context.Context,
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
			"SELECT %s FROM `%s` FINAL WHERE %s = ? ORDER BY %s LIMIT %d OFFSET %d",
			strings.Join(columnsForCopy, ", "),
			table, fieldKnowledgeBaseID, fieldID,
			pageSize, offset,
		)
		rows, err := r.db.QueryContext(ctx, stmt, sourceKnowledgeBaseID)
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

		var targets []*ClickhouseVectorEmbedding
		for _, src := range batch {
			targetChunkID, ok := sourceToTargetChunkIDMap[src.ChunkID]
			if !ok {
				log.Warnf("[ClickHouse] Source chunk %s not in target mapping", src.ChunkID)
				continue
			}
			targetKnowledgeID, ok := sourceToTargetKBIDMap[src.KnowledgeID]
			if !ok {
				log.Warnf("[ClickHouse] Source knowledge %s not in target mapping", src.KnowledgeID)
				continue
			}

			targetSourceID := translateSourceID(src.SourceID, src.ChunkID, targetChunkID)
			targets = append(targets, &ClickhouseVectorEmbedding{
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
	log.Infof("[ClickHouse] CopyIndices done, dim=%d, copied=%d", dimension, totalCopied)
	return nil
}

// BatchUpdateChunkEnabledStatus 批量更新 chunk 的 is_enabled 字段。
// 按目标值分组后逐表发 ALTER TABLE ... UPDATE（异步 mutation，
// 生效前旧值仍可能被检索返回；一致性硬化在后续 PR 做）。
func (r *clickhouseRepository) BatchUpdateChunkEnabledStatus(ctx context.Context,
	chunkStatusMap map[string]bool,
) error {
	if len(chunkStatusMap) == 0 {
		return nil
	}
	byValue := map[uint8][]string{}
	for chunkID, enabled := range chunkStatusMap {
		byValue[boolToUInt8(enabled)] = append(byValue[boolToUInt8(enabled)], chunkID)
	}
	return batchUpdateByChunkID(r, ctx, byValue, func(table string, value uint8, chunkIDs []string) error {
		return r.alterUpdate(ctx, table, fmt.Sprintf("%s = %d", fieldIsEnabled, value), chunkIDs)
	})
}

// BatchUpdateChunkTagID 批量更新 chunk 的 tag_id 字段。逻辑与
// BatchUpdateChunkEnabledStatus 一致，按目标 tag 分组后逐表更新。
func (r *clickhouseRepository) BatchUpdateChunkTagID(ctx context.Context,
	chunkTagMap map[string]string,
) error {
	if len(chunkTagMap) == 0 {
		return nil
	}
	byValue := map[string][]string{}
	for chunkID, tagID := range chunkTagMap {
		byValue[tagID] = append(byValue[tagID], chunkID)
	}
	return batchUpdateByChunkID(r, ctx, byValue, func(table string, value string, chunkIDs []string) error {
		return r.alterUpdateWithArg(ctx, table, fieldTagID, value, chunkIDs)
	})
}

// batchUpdateByChunkID 是两个 BatchUpdate* 方法的统一骨架：
// 先列出所有维度表，再按分组值对每张表执行一次更新。
func batchUpdateByChunkID[V comparable](r *clickhouseRepository, ctx context.Context,
	byValue map[V][]string,
	update func(table string, value V, chunkIDs []string) error,
) error {
	tables, err := r.listEmbeddingTables(ctx)
	if err != nil {
		return fmt.Errorf("list tables: %w", err)
	}
	for _, table := range tables {
		for value, chunkIDs := range byValue {
			if err := update(table, value, chunkIDs); err != nil {
				return err
			}
		}
	}
	return nil
}

// alterUpdate 发一条 ALTER TABLE ... UPDATE ... WHERE chunk_id IN (...)
// （SET 子句是服务端内联的 uint8 字面量，来源受控）。
func (r *clickhouseRepository) alterUpdate(ctx context.Context,
	table, setClause string, chunkIDs []string,
) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	placeholders := make([]string, len(chunkIDs))
	args := make([]any, len(chunkIDs))
	for i, id := range chunkIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	stmt := fmt.Sprintf("ALTER TABLE `%s` UPDATE %s WHERE %s IN (%s)",
		table, setClause, fieldChunkID, strings.Join(placeholders, ", "))
	_, err := r.db.ExecContext(ctx, stmt, args...)
	return err
}

// alterUpdateWithArg 发一条 ALTER TABLE ... UPDATE <field> = ? WHERE
// chunk_id IN (...)（SET 值走占位符，避免字符串拼接的注入风险）。
func (r *clickhouseRepository) alterUpdateWithArg(ctx context.Context,
	table, field, value string, chunkIDs []string,
) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	placeholders := make([]string, len(chunkIDs))
	args := make([]any, 0, len(chunkIDs)+1)
	args = append(args, value)
	for i, id := range chunkIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	stmt := fmt.Sprintf("ALTER TABLE `%s` UPDATE %s = ? WHERE %s IN (%s)",
		table, field, fieldChunkID, strings.Join(placeholders, ", "))
	_, err := r.db.ExecContext(ctx, stmt, args...)
	return err
}

// ---------------------------------------------------------------------------
// 私有辅助
// ---------------------------------------------------------------------------

// toClickhouseVectorEmbedding 把 IndexInfo + 上层传入的 embedding 映射
// 转换为 ClickHouse 行模型。Embedding 通过 additionalParams[fieldEmbedding]
// 中的 map[string][]float32 按 SourceID 取出，与 Qdrant/Milvus/Doris 完全一致。
func toClickhouseVectorEmbedding(
	info *types.IndexInfo,
	additionalParams map[string]any,
) *ClickhouseVectorEmbedding {
	emb := &ClickhouseVectorEmbedding{
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
			}
		}
	}
	return emb
}

// translateSourceID 把源 SourceID 翻译到目标 SourceID，与 Doris/Qdrant
// 实现完全镜像：
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
//   - 列数 == columnsForRetrieve：score 统一赋 1.0（关键词检索路径，PR1 未用）
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
			isEnabled                           uint8
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
			IsEnabled:       isEnabled == 1,
		})
	}
	return out, rows.Err()
}

// scanCopyRows 反序列化 CopyIndices 的分页查询结果。
//
// 与 scanRetrieveRows 不同，这里需要 embedding 字段（去复制原始向量）。
// clickhouse-go 的 database/sql 驱动对 Array(Float32) 通常直接返回
// []float32；文本形态则走 parseEmbeddingLiteral。
func scanCopyRows(rows *sql.Rows) ([]*ClickhouseVectorEmbedding, error) {
	var out []*ClickhouseVectorEmbedding
	for rows.Next() {
		var (
			id, content, sourceID, chunkID      string
			knowledgeID, knowledgeBaseID, tagID string
			sourceType                          int
			isEnabled                           uint8
			embeddingRaw                        any
		)
		if err := rows.Scan(&id, &content, &sourceID, &sourceType,
			&chunkID, &knowledgeID, &knowledgeBaseID, &tagID, &isEnabled, &embeddingRaw); err != nil {
			return nil, fmt.Errorf("scan copy row: %w", err)
		}
		vec, err := parseEmbeddingValue(embeddingRaw)
		if err != nil {
			return nil, fmt.Errorf("parse embedding: %w", err)
		}
		out = append(out, &ClickhouseVectorEmbedding{
			ID:              id,
			Content:         content,
			SourceID:        sourceID,
			SourceType:      sourceType,
			ChunkID:         chunkID,
			KnowledgeID:     knowledgeID,
			KnowledgeBaseID: knowledgeBaseID,
			TagID:           tagID,
			IsEnabled:       isEnabled == 1,
			Embedding:       vec,
		})
	}
	return out, rows.Err()
}

// buildRetrieveResult 把 IndexWithScore 列表包装成 RetrieveResult。
func buildRetrieveResult(results []*types.IndexWithScore, retrieverType types.RetrieverType) []*types.RetrieveResult {
	return []*types.RetrieveResult{{
		Results:             results,
		RetrieverEngineType: types.ClickhouseRetrieverEngineType,
		RetrieverType:       retrieverType,
		Error:               nil,
	}}
}

// calculateStorageSize 估算单行的存储成本。
//
// 与 Doris/Qdrant 一致：payload 字符串字节 + 向量 (dim*4) + HNSW 邻居 + 元数据 24。
// ClickHouse 原生 HNSW 默认参数为 bf16 量化、M=32、ef_construction=128，
// 按 M=32 估算。
func calculateStorageSize(emb *ClickhouseVectorEmbedding) int64 {
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

// boolToUInt8 把 Go bool 转成 ClickHouse UInt8 列值。
func boolToUInt8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}
