package clickhouse

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
)

// getTableName 返回某个维度对应的物理表名：<base>_<dim>。
//
// 与 Qdrant/Milvus/Weaviate/Doris 的命名约定一致，
// 这样不同 embedding 模型（不同维度）的数据互不冲突。
func (r *clickhouseRepository) getTableName(dimension int) string {
	return fmt.Sprintf("%s_%d", r.tableBaseName, dimension)
}

// ensureTable 保证目标维度对应的表已经存在；
// 不存在则用 CREATE TABLE IF NOT EXISTS 创建。
//
// 该方法在每次 Save / BatchSave 之前调用，结果缓存在 initializedTables 中，
// 同一进程内同一 dimension 只会真正打一次 system.tables + DDL。
//
// 注意：ClickHouse 的 vector_similarity 索引在建表后后台异步构建，
// 索引未就绪期间检索会退化为精确扫描（结果对、速度慢），不阻塞写入。
func (r *clickhouseRepository) ensureTable(ctx context.Context, dimension int) error {
	if _, ok := r.initializedTables.Load(dimension); ok {
		return nil
	}

	log := logger.GetLogger(ctx)
	tableName := r.getTableName(dimension)

	exists, err := r.tableExists(ctx, tableName)
	if err != nil {
		log.Errorf("[ClickHouse] Failed to check table existence: %v", err)
		return fmt.Errorf("check table existence: %w", err)
	}

	if !exists {
		log.Infof("[ClickHouse] Creating table %s with dimension %d", tableName, dimension)
		if err := r.createTable(ctx, tableName, dimension); err != nil {
			log.Errorf("[ClickHouse] Failed to create table: %v", err)
			return fmt.Errorf("create table: %w", err)
		}
	}

	r.initializedTables.Store(dimension, true)
	return nil
}

// tableExists 通过 system.tables 判断表是否存在。
func (r *clickhouseRepository) tableExists(ctx context.Context, tableName string) (bool, error) {
	const q = `SELECT count() FROM system.tables WHERE database = ? AND name = ?`
	var n int
	if err := r.db.QueryRowContext(ctx, q, r.database, tableName).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// createTable 发出 CREATE TABLE DDL。返回成功即代表表已可写
// （ANN 索引本身后台构建，不阻塞）。
func (r *clickhouseRepository) createTable(ctx context.Context, tableName string, dimension int) error {
	ddl := buildCreateTableDDL(tableName, dimension)
	_, err := r.db.ExecContext(ctx, ddl)
	return err
}

// buildCreateTableDDL 根据维度生成 CREATE TABLE DDL。
//
// 关键点：
//   - ENGINE = ReplacingMergeTree() + ORDER BY id：ClickHouse 的
//     ALTER TABLE ... DELETE/UPDATE 是异步 mutation，delete + insert 无法
//     安全模拟"按 id 覆盖"（mutation 可能在新 part 落盘后才执行，
//     把刚写入的行也删掉）；ReplacingMergeTree 按 ORDER BY key 去重，
//     后台合并时保留最新写入的行，检索侧用 FINAL 在查询时完成去重。
//   - ANN 索引使用原生 HNSW + cosineDistance：cosineDistance 返回距离
//     [0, 2]，检索时 score = 1 - cosineDistance，与其他引擎一致的
//     cosine 相似度语义（越大越相似）。vector_similarity 索引在 25.8
//     才 GA（生产可用），且必须带维度参数；量化/M/ef_construction
//     用默认值（bf16 / 32 / 128），GRANULARITY 也不指定（默认单 part
//     一张图）。25.8 之前版本建表会直接失败，见 #5722 讨论。
//
// 注意：DDL 中 dimension 是 Go 端格式化拼接的，
// 不存在 SQL 注入风险（来源是受控的 int）。
func buildCreateTableDDL(tableName string, dimension int) string {
	const tpl = `CREATE TABLE IF NOT EXISTS ` + "`%s`" + ` (
    id                String,
    chunk_id          String,
    knowledge_id      String,
    knowledge_base_id String,
    source_id         String,
    source_type       Int32,
    tag_id            String,
    is_enabled        UInt8,
    content           String,
    embedding         Array(Float32),
    INDEX idx_emb embedding TYPE vector_similarity('hnsw', 'cosineDistance', %d)
) ENGINE = ReplacingMergeTree()
ORDER BY id;`
	return fmt.Sprintf(tpl, tableName, dimension)
}

// listEmbeddingTables 返回当前 database 下所有 <base>_% 命名的表，
// 用于跨维度 BatchUpdate。
func (r *clickhouseRepository) listEmbeddingTables(ctx context.Context) ([]string, error) {
	const q = `SELECT name FROM system.tables WHERE database = ? AND name LIKE ?`
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
