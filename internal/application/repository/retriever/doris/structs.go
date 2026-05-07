package doris

import (
	"database/sql"
	"net/http"
	"sync"
)

// dorisRepository 是 Apache Doris 4.1 的检索引擎仓储实现。
//
// 通信通道：
//   - 读写主链路：MySQL 协议（database/sql + go-sql-driver/mysql），FE 默认 9030 端口。
//   - Stream Load：HTTP（FE 默认 8030 端口），用于 BatchUpdate* 的 partial update。
//
// 表结构按维度分表：<tableBaseName>_<dim>，UNIQUE KEY(id)，
// 同时建立倒排索引（filter / 全文）+ ANN(HNSW) 索引。
//
// 与 Qdrant/Milvus/Weaviate 一样，initializedTables 缓存"已确保存在"的维度，
// 避免每次写入都打 SHOW TABLES。
type dorisRepository struct {
	db *sql.DB

	httpClient *http.Client
	// fe HTTP base，例如 "http://doris-fe:8030"。Stream Load 路径
	// 由 streamLoadURL(table) 拼接：<feHTTPBase>/api/<database>/<table>/_stream_load。
	feHTTPBase string

	username string
	password string
	database string

	tableBaseName  string
	bucketsNum     int // 0 -> default 10
	replicationNum int // 0 -> default 1

	// 已经确保过 ensureTable 的维度集合：dim -> true。
	initializedTables sync.Map
}

// DorisVectorEmbedding 是落到 Doris 表里的一行的领域模型。
//
// 字段顺序与 schema.go 中的 INSERT 列序保持一致，
// 调整时需要同时更新 createInsert 与 columns。
type DorisVectorEmbedding struct {
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

// DorisVectorEmbeddingWithScore 是检索结果的领域模型，
// Score 在向量检索时存储 (1 - cosine_distance_approximate)，
// 在关键词检索时统一赋 1.0（与 Qdrant 行为一致）。
type DorisVectorEmbeddingWithScore struct {
	DorisVectorEmbedding
	Score float64
}
