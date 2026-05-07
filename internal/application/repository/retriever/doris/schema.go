package doris

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

// 默认的桶数 / 副本数。Doris 在 PROPERTIES 不指定时会用集群默认值，
// 这里给一个对单机/小集群更友好的保守值。
const (
	defaultBucketsNum     = 10
	defaultReplicationNum = 1

	// ANN 索引就绪轮询的最大等待时间。索引未就绪不阻塞写入路径，
	// 只阻塞 ensureTable 自身（首次建表场景），所以 30s 是可接受的。
	annReadyTimeout = 30 * time.Second
	annReadyPoll    = 1 * time.Second
)

// getTableName 返回某个维度对应的物理表名：<base>_<dim>。
//
// 与 Qdrant/Milvus/Weaviate 的 collection 命名约定一致，
// 这样不同 embedding 模型（不同维度）的数据互不冲突。
func (r *dorisRepository) getTableName(dimension int) string {
	return fmt.Sprintf("%s_%d", r.tableBaseName, dimension)
}

// ensureTable 保证目标维度对应的表已经存在；
// 不存在则用 CREATE TABLE IF NOT EXISTS 创建，并在创建后轮询 ANN 索引就绪。
//
// 该方法在每次 Save / BatchSave 之前调用，结果缓存在 initializedTables 中，
// 同一进程内同一 dimension 只会真正打一次 SHOW TABLES + DDL。
func (r *dorisRepository) ensureTable(ctx context.Context, dimension int) error {
	if _, ok := r.initializedTables.Load(dimension); ok {
		return nil
	}

	log := logger.GetLogger(ctx)
	tableName := r.getTableName(dimension)

	exists, err := r.tableExists(ctx, tableName)
	if err != nil {
		log.Errorf("[Doris] Failed to check table existence: %v", err)
		return fmt.Errorf("check table existence: %w", err)
	}

	if !exists {
		log.Infof("[Doris] Creating table %s with dimension %d", tableName, dimension)
		if err := r.createTable(ctx, tableName, dimension); err != nil {
			log.Errorf("[Doris] Failed to create table: %v", err)
			return fmt.Errorf("create table: %w", err)
		}

		// 创建表后等一会让 ANN 索引就绪，超时不致命。
		if err := r.waitANNReady(ctx, tableName); err != nil {
			log.Warnf("[Doris] ANN index for %s not ready within %s: %v "+
				"(writes will still proceed; queries may fall back to brute force temporarily)",
				tableName, annReadyTimeout, err)
		}
	}

	r.initializedTables.Store(dimension, true)
	return nil
}

// tableExists 通过 information_schema 判断表是否存在。
//
// 不直接用 SHOW TABLES 是因为 Doris 4.1 对 SHOW TABLES LIKE 大小写敏感，
// 而 information_schema 与 MySQL 兼容性更好。
func (r *dorisRepository) tableExists(ctx context.Context, tableName string) (bool, error) {
	const q = `SELECT COUNT(1) FROM information_schema.tables
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`
	var n int
	if err := r.db.QueryRowContext(ctx, q, r.database, tableName).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// createTable 发出 CREATE TABLE DDL。Doris DDL 是同步的（除 ANN 索引构建外），
// 返回成功即代表表已可写。
func (r *dorisRepository) createTable(ctx context.Context, tableName string, dimension int) error {
	buckets := r.bucketsNum
	if buckets <= 0 {
		buckets = defaultBucketsNum
	}
	replication := r.replicationNum
	if replication <= 0 {
		replication = defaultReplicationNum
	}

	ddl := buildCreateTableDDL(tableName, dimension, buckets, replication)
	_, err := r.db.ExecContext(ctx, ddl)
	return err
}

// buildCreateTableDDL 根据维度生成 CREATE TABLE DDL。
//
// 关键点：
//   - UNIQUE KEY(id) + enable_unique_key_merge_on_write=true：
//     这是 partial update（Stream Load） 起作用的前提。
//   - INVERTED 索引覆盖所有过滤字段 + 中文分词的 content 全文索引。
//   - ANN 索引使用 HNSW + cosine_distance，与 Qdrant/Weaviate 的 cosine 相似度方向一致。
//
// 注意：DDL 中 dimension / buckets / replication 三个数值字段是 Go 端格式化拼接的，
// 不存在 SQL 注入风险（来源都是受控的 IndexConfig int）。
func buildCreateTableDDL(tableName string, dimension, buckets, replication int) string {
	const tpl = `CREATE TABLE IF NOT EXISTS ` + "`%s`" + ` (
    id                VARCHAR(64)  NOT NULL,
    chunk_id          VARCHAR(64),
    knowledge_id      VARCHAR(64),
    knowledge_base_id VARCHAR(64),
    source_id         VARCHAR(255),
    source_type       INT,
    tag_id            VARCHAR(64),
    is_enabled        BOOLEAN,
    content           TEXT,
    embedding         ARRAY<FLOAT> NOT NULL,
    INDEX idx_chunk    (chunk_id)          USING INVERTED,
    INDEX idx_kb       (knowledge_base_id) USING INVERTED,
    INDEX idx_kid      (knowledge_id)      USING INVERTED,
    INDEX idx_src      (source_id)         USING INVERTED,
    INDEX idx_tag      (tag_id)            USING INVERTED,
    INDEX idx_enabled  (is_enabled)        USING INVERTED,
    INDEX idx_content  (content)           USING INVERTED PROPERTIES("parser"="chinese","support_phrase"="true"),
    INDEX idx_emb      (embedding)         USING ANN PROPERTIES(
        "index_type"="hnsw",
        "metric_type"="cosine_distance",
        "dim"="%d",
        "max_degree"="32",
        "ef_construction"="200"
    )
) ENGINE=OLAP
UNIQUE KEY(id)
DISTRIBUTED BY HASH(id) BUCKETS %d
PROPERTIES(
    "replication_num"="%d",
    "enable_unique_key_merge_on_write"="true"
);`
	return fmt.Sprintf(tpl, tableName, dimension, buckets, replication)
}

// waitANNReady 轮询 SHOW INDEX，等待 ANN 索引进入 FINISHED 状态。
//
// Doris 的 ANN 索引在建表后会异步构建，期间查询会退化为 brute-force（结果对，速度慢）。
// 此处仅做"尽力而为"的等待：到点未就绪只记 warning，不阻塞写入。
func (r *dorisRepository) waitANNReady(ctx context.Context, tableName string) error {
	deadline := time.Now().Add(annReadyTimeout)
	for {
		ready, err := r.annIndexReady(ctx, tableName)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("ann index not ready within %s", annReadyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(annReadyPoll):
		}
	}
}

// annIndexReady 检查 ANN 索引的 State 是否为 FINISHED。
//
// SHOW INDEX FROM <table> 在 Doris 上返回多列；不同小版本列序略有差异，
// 这里以列名匹配（来自 information_schema.statistics + 自定义 view 不可行，
// 直接用 SHOW INDEX 然后扫描即可）。
//
// 兼容策略：如果 SHOW INDEX 返回中找不到 idx_emb 行（极旧版本），视为已就绪，
// 避免因为不同 Doris 版本的输出差异把启动卡死。
func (r *dorisRepository) annIndexReady(ctx context.Context, tableName string) (bool, error) {
	rows, err := r.db.QueryContext(ctx,
		fmt.Sprintf("SHOW INDEX FROM `%s`", tableName))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return false, err
	}
	keyNameIdx, stateIdx := -1, -1
	for i, c := range cols {
		switch strings.ToLower(c) {
		case "key_name":
			keyNameIdx = i
		case "state", "index_state":
			stateIdx = i
		}
	}

	foundANN := false
	for rows.Next() {
		// 使用 sql.RawBytes 接收以兼容不同列类型。
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return false, err
		}

		var keyName, state string
		if keyNameIdx >= 0 {
			keyName = bytesToString(raw[keyNameIdx])
		}
		if stateIdx >= 0 {
			state = bytesToString(raw[stateIdx])
		}

		if keyName != "idx_emb" {
			continue
		}
		foundANN = true
		if stateIdx < 0 {
			// 旧版本不暴露 state 列，乐观认为已就绪。
			return true, nil
		}
		if !strings.EqualFold(state, "FINISHED") &&
			!strings.EqualFold(state, "NORMAL") {
			return false, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}

	if !foundANN {
		// 没找到 ANN 行（可能是 Doris 版本返回字段不同），不阻塞。
		return true, nil
	}
	return true, nil
}

// listEmbeddingTables 返回当前 database 下所有 <base>_% 命名的表，
// 用于关键词检索 / 跨维度 BatchUpdate。
func (r *dorisRepository) listEmbeddingTables(ctx context.Context) ([]string, error) {
	const q = `SELECT TABLE_NAME FROM information_schema.tables
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME LIKE ?`
	rows, err := r.db.QueryContext(ctx, q, r.database, r.tableBaseName+"\\_%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// bytesToString 把 SHOW INDEX 返回的 raw any（通常是 []byte 或 string）
// 安全转成字符串。
func bytesToString(v any) string {
	switch s := v.(type) {
	case []byte:
		return string(s)
	case string:
		return s
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", s)
	}
}
