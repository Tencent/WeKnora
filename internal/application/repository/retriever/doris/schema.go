package doris

import (
	"context"
	"database/sql"
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

	schemaReadyTimeout = 30 * time.Second
	schemaReadyPoll    = 500 * time.Millisecond
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

	r.ensureTableMu.Lock()
	defer r.ensureTableMu.Unlock()
	if _, ok := r.initializedTables.Load(dimension); ok {
		return nil
	}

	compatMode, err := r.resolveCompatMode(ctx)
	if err != nil {
		return err
	}

	log := logger.GetLogger(ctx)
	tableName := r.getTableName(dimension)

	exists, err := r.tableExists(ctx, tableName)
	if err != nil {
		log.Errorf("[Doris] Failed to check table existence: %v", err)
		return fmt.Errorf("check table existence: %w", err)
	}

	created := false
	if !exists {
		log.Infof("[Doris] Creating table %s with dimension %d in compat mode %s", tableName, dimension, compatMode)
		if err := r.createTable(ctx, tableName, dimension, compatMode); err != nil {
			log.Errorf("[Doris] Failed to create table: %v", err)
			return fmt.Errorf("create table: %w", err)
		}
		created = true
	}

	// Always confirm the folder schema after CREATE TABLE IF NOT EXISTS.
	// During a rolling upgrade, an old replica can create the legacy table
	// between our existence check and DDL, turning our CREATE into a no-op.
	if err := r.ensureFolderSchema(ctx, tableName); err != nil {
		return fmt.Errorf("upgrade folder schema for table %s: %w", tableName, err)
	}

	if created {
		// ANN 索引在 Doris 端异步构建。这里在后台 goroutine 里轮询就绪，
		// 写入路径不阻塞——索引未就绪期间检索会退化为 brute-force（结果对、速度慢），
		// 比让首批写入卡 30s 更可接受。
		go func(tn string) {
			// 用独立 context（带 timeout），避免请求级 ctx 取消把后台轮询也带走。
			bgCtx, cancel := context.WithTimeout(context.Background(), annReadyTimeout)
			defer cancel()
			if err := r.waitANNReady(bgCtx, tn); err != nil {
				logger.GetLogger(bgCtx).Warnf(
					"[Doris] ANN index for %s not ready within %s: %v "+
						"(queries may fall back to brute force temporarily)",
					tn, annReadyTimeout, err)
				return
			}
			logger.GetLogger(bgCtx).Infof("[Doris] ANN index for %s ready", tn)
		}(tableName)
	}

	r.initializedTables.Store(dimension, true)
	r.schemaReadyTables.Store(tableName, true)
	return nil
}

func (r *dorisRepository) ensureExistingTable(ctx context.Context, tableName string) error {
	if _, ok := r.schemaReadyTables.Load(tableName); ok {
		return nil
	}

	r.ensureTableMu.Lock()
	defer r.ensureTableMu.Unlock()
	if _, ok := r.schemaReadyTables.Load(tableName); ok {
		return nil
	}
	if err := r.ensureFolderSchema(ctx, tableName); err != nil {
		return err
	}
	r.schemaReadyTables.Store(tableName, true)
	return nil
}

// ensureFolderSchema upgrades embedding tables created before folder support.
// Existing rows receive the empty-string default and therefore remain in the
// knowledge-base root. Both DDL statements are idempotent so concurrent service
// instances can safely race during a rolling upgrade.
func (r *dorisRepository) ensureFolderSchema(ctx context.Context, tableName string) error {
	hasColumn, err := r.columnExists(ctx, tableName, fieldFolderID)
	if err != nil {
		return err
	}
	if !hasColumn {
		ddl := fmt.Sprintf(
			"ALTER TABLE `%s` ADD COLUMN IF NOT EXISTS %s VARCHAR(36) NOT NULL DEFAULT '' AFTER tag_id",
			tableName,
			fieldFolderID,
		)
		if _, err := r.db.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("add %s column: %w", fieldFolderID, err)
		}
		if err := r.waitColumnReady(ctx, tableName, fieldFolderID); err != nil {
			return err
		}
	} else if err := r.validateFolderColumn(ctx, tableName); err != nil {
		return err
	}

	hasIndex, err := r.indexExists(ctx, tableName, "idx_folder")
	if err != nil {
		return err
	}
	if hasIndex {
		return nil
	}
	ddl := fmt.Sprintf(
		"CREATE INDEX IF NOT EXISTS idx_folder ON `%s` (%s) USING INVERTED",
		tableName,
		fieldFolderID,
	)
	if _, err := r.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("create idx_folder: %w", err)
	}
	return r.waitIndexReady(ctx, tableName, "idx_folder")
}

func (r *dorisRepository) columnExists(ctx context.Context, tableName, columnName string) (bool, error) {
	const query = `SELECT COUNT(1) FROM information_schema.columns
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND COLUMN_NAME = ?`
	var count int
	if err := r.db.QueryRowContext(ctx, query, r.database, tableName, columnName).Scan(&count); err != nil {
		return false, fmt.Errorf("check column %s: %w", columnName, err)
	}
	return count > 0, nil
}

func (r *dorisRepository) validateFolderColumn(ctx context.Context, tableName string) error {
	const query = `SELECT COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT
		FROM information_schema.columns
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND COLUMN_NAME = ?`
	var columnType string
	var nullable string
	var defaultValue sql.NullString
	if err := r.db.QueryRowContext(
		ctx,
		query,
		r.database,
		tableName,
		fieldFolderID,
	).Scan(&columnType, &nullable, &defaultValue); err != nil {
		return fmt.Errorf("inspect column %s: %w", fieldFolderID, err)
	}

	normalizedType := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(columnType), " ", ""))
	if normalizedType != "varchar(36)" {
		return fmt.Errorf(
			"incompatible %s column on table %s: type %q, want VARCHAR(36)",
			fieldFolderID,
			tableName,
			columnType,
		)
	}
	if !strings.EqualFold(strings.TrimSpace(nullable), "NO") {
		return fmt.Errorf(
			"incompatible %s column on table %s: nullable %q, want NOT NULL",
			fieldFolderID,
			tableName,
			nullable,
		)
	}
	if !defaultValue.Valid {
		return fmt.Errorf(
			"incompatible %s column on table %s: missing default, want empty string",
			fieldFolderID,
			tableName,
		)
	}
	normalizedDefault := strings.TrimSpace(defaultValue.String)
	if normalizedDefault != "" && normalizedDefault != "''" {
		return fmt.Errorf(
			"incompatible %s column on table %s: default %q, want empty string",
			fieldFolderID,
			tableName,
			defaultValue.String,
		)
	}
	return nil
}

func (r *dorisRepository) waitColumnReady(ctx context.Context, tableName, columnName string) error {
	deadline := time.Now().Add(schemaReadyTimeout)
	for {
		exists, err := r.columnExists(ctx, tableName, columnName)
		if err != nil {
			return err
		}
		if exists {
			return r.validateFolderColumn(ctx, tableName)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("column %s not ready within %s", columnName, schemaReadyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(schemaReadyPoll):
		}
	}
}

func (r *dorisRepository) waitIndexReady(ctx context.Context, tableName, indexName string) error {
	deadline := time.Now().Add(schemaReadyTimeout)
	for {
		ready, err := r.indexExists(ctx, tableName, indexName)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("index %s not ready within %s", indexName, schemaReadyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(schemaReadyPoll):
		}
	}
}

func (r *dorisRepository) indexExists(ctx context.Context, tableName, indexName string) (bool, error) {
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf("SHOW INDEX FROM `%s`", tableName))
	if err != nil {
		return false, fmt.Errorf("show indexes: %w", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return false, err
	}
	keyNameIndex := -1
	columnNameIndex := -1
	indexTypeIndex := -1
	stateIndex := -1
	for i, columnName := range columns {
		if strings.EqualFold(columnName, "key_name") {
			keyNameIndex = i
		}
		if strings.EqualFold(columnName, "column_name") {
			columnNameIndex = i
		}
		if strings.EqualFold(columnName, "index_type") {
			indexTypeIndex = i
		}
		if strings.EqualFold(columnName, "state") {
			stateIndex = i
		}
	}
	if keyNameIndex < 0 {
		return false, fmt.Errorf("SHOW INDEX result has no Key_name column")
	}
	if columnNameIndex < 0 {
		return false, fmt.Errorf("SHOW INDEX result has no Column_name column")
	}
	if indexTypeIndex < 0 {
		return false, fmt.Errorf("SHOW INDEX result has no Index_type column")
	}

	found := false
	ready := true
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			return false, err
		}
		if strings.EqualFold(bytesToString(values[keyNameIndex]), indexName) {
			found = true
			actualColumn := bytesToString(values[columnNameIndex])
			if !strings.EqualFold(actualColumn, fieldFolderID) {
				return false, fmt.Errorf(
					"incompatible %s index on table %s: column %q, want %s",
					indexName,
					tableName,
					actualColumn,
					fieldFolderID,
				)
			}
			indexType := bytesToString(values[indexTypeIndex])
			if !strings.EqualFold(indexType, "INVERTED") {
				return false, fmt.Errorf(
					"incompatible %s index on table %s: type %q, want INVERTED",
					indexName,
					tableName,
					indexType,
				)
			}
			if stateIndex >= 0 {
				state := bytesToString(values[stateIndex])
				if !strings.EqualFold(state, "FINISHED") &&
					!strings.EqualFold(state, "NORMAL") {
					ready = false
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return found && ready, nil
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
func (r *dorisRepository) createTable(ctx context.Context, tableName string, dimension int, compatMode dorisCompatMode) error {
	buckets := r.bucketsNum
	if buckets <= 0 {
		buckets = defaultBucketsNum
	}
	replication := r.replicationNum
	if replication <= 0 {
		replication = defaultReplicationNum
	}

	ddl := buildCreateTableDDL(tableName, dimension, buckets, replication, compatMode)
	_, err := r.db.ExecContext(ctx, ddl)
	if err != nil && compatMode == dorisCompatModeLegacy {
		return fmt.Errorf(
			"legacy Doris table creation failed: %w. If your Doris build rejects ANN indexes on UNIQUE KEY tables, set %s=%s before creating embedding tables. %s is not interchangeable after %s_* tables are created",
			err,
			envDorisCompatMode,
			dorisCompatModeInnerProductDuplicate,
			envDorisCompatMode,
			r.tableBaseName,
		)
	}
	return err
}

// buildCreateTableDDL 根据维度生成 CREATE TABLE DDL。
//
// 关键点：
//   - DUPLICATE KEY(id)：兼容当前 Doris/SelectDB 对 ANN 索引的表模型要求。
//     WeKnora 在 Go 端用 delete + insert 保持按 id 替换的写入语义。
//   - INVERTED 索引覆盖所有过滤字段 + 中文分词的 content 全文索引。
//   - ANN 索引使用 HNSW + inner_product；Doris 写入/查询前会对向量单位化，
//     因此整体仍保持与其他向量库一致的 cosine 相似度语义。
//
// 注意：DDL 中 dimension / buckets / replication 三个数值字段是 Go 端格式化拼接的，
// 不存在 SQL 注入风险（来源都是受控的 IndexConfig int）。
func buildCreateTableDDL(tableName string, dimension, buckets, replication int, compatMode dorisCompatMode) string {
	metricType := "inner_product"
	keyMode := "DUPLICATE KEY(id)"
	properties := fmt.Sprintf("\t\"replication_num\"=\"%d\"", replication)
	if compatMode == dorisCompatModeLegacy {
		metricType = "cosine_distance"
		keyMode = "UNIQUE KEY(id)"
		properties = fmt.Sprintf("\t\"replication_num\"=\"%d\",\n\t\"enable_unique_key_merge_on_write\"=\"true\"", replication)
	}

	const tpl = `CREATE TABLE IF NOT EXISTS ` + "`%s`" + ` (
    id                VARCHAR(64)  NOT NULL,
    chunk_id          VARCHAR(64),
    knowledge_id      VARCHAR(64),
    knowledge_base_id VARCHAR(64),
    source_id         VARCHAR(255),
    source_type       INT,
    tag_id            VARCHAR(64),
    folder_id         VARCHAR(36) NOT NULL DEFAULT "",
    is_enabled        BOOLEAN,
    content           TEXT,
    embedding         ARRAY<FLOAT> NOT NULL,
    INDEX idx_chunk    (chunk_id)          USING INVERTED,
    INDEX idx_kb       (knowledge_base_id) USING INVERTED,
    INDEX idx_kid      (knowledge_id)      USING INVERTED,
    INDEX idx_src      (source_id)         USING INVERTED,
    INDEX idx_tag      (tag_id)            USING INVERTED,
    INDEX idx_folder   (folder_id)         USING INVERTED,
    INDEX idx_enabled  (is_enabled)        USING INVERTED,
    INDEX idx_content  (content)           USING INVERTED PROPERTIES("parser"="chinese","support_phrase"="true"),
    INDEX idx_emb      (embedding)         USING ANN PROPERTIES(
        "index_type"="hnsw",
		"metric_type"="%s",
        "dim"="%d",
        "max_degree"="32",
        "ef_construction"="200"
    )
) ENGINE=OLAP
%s
DISTRIBUTED BY HASH(id) BUCKETS %d
PROPERTIES(
	%s
);`
	return fmt.Sprintf(tpl, tableName, metricType, dimension, keyMode, buckets, properties)
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
	// 走到这里有两种情况：
	//   1. 找到了 idx_emb 行，且 state 已是 FINISHED/NORMAL（或 stateIdx<0 的旧版本）；
	//   2. 没找到 idx_emb 行（极旧 Doris 不暴露该索引名）；
	// 都视为已就绪，不阻塞。未就绪的分支已在循环内提前 return false。
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
