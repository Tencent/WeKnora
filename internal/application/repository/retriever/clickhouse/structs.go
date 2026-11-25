package clickhouse

import (
	"database/sql"
	"sync"
)

// clickhouseRepository 是 ClickHouse 原生向量检索引擎的仓储实现（#5722 PR1）。
//
// 通信通道：database/sql + github.com/ClickHouse/clickhouse-go/v2 注册的
// "clickhouse" 驱动，走原生协议（默认 9000 端口）。与 Doris 一样，
// initializedTables 缓存"已确保存在"的维度，避免每次写入都查 system.tables。
//
// 表结构按维度分表：<tableBaseName>_<dim>，引擎为 ReplacingMergeTree：
// ClickHouse 的 ALTER TABLE ... DELETE/UPDATE 是异步 mutation，写入后
// 立刻再发 DELETE 可能把刚写入的新 part 也圈进来，因此不像 Doris 那样
// 用 delete + insert 模拟替换语义，而是依赖 ReplacingMergeTree 按
// ORDER BY id 的去重（后台合并时保留最新写入的行），检索侧用 FINAL
// 在查询时完成去重，保证同一 id 只返回一行。关键词检索、hybrid RRF、
// metadata filter 与 update/delete 一致性硬化在后续 PR 做。
type clickhouseRepository struct {
	db *sql.DB

	database string

	tableBaseName string

	// 已经确保过 ensureTable 的维度集合：dim -> true。
	initializedTables sync.Map
}

// ClickhouseVectorEmbedding 是落到 ClickHouse 表里的一行的领域模型。
//
// 字段顺序与 query.go 中的 INSERT 列序保持一致，
// 调整时需要同时更新 insertRows 与 columns。
type ClickhouseVectorEmbedding struct {
	ID              string
	Content         string
	SourceID        string
	SourceType      int
	ChunkID         string
	KnowledgeID     string
	KnowledgeBaseID string
	TagID           string
	IsEnabled       bool
	Embedding       []float32
}
