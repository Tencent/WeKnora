package tencentvectordb

import (
	"sync"

	"github.com/tencent/vectordatabase-sdk-go/tcvdbtext/encoder"
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
)

const (
	envTencentVectorDBDatabase   = "TENCENT_VECTORDB_DATABASE"
	envTencentVectorDBCollection = "TENCENT_VECTORDB_COLLECTION"
	defaultDatabaseName          = "weknora"
	defaultCollectionName        = "weknora_embeddings"

	fieldID              = "id"
	fieldVector          = "vector"
	fieldSparseVector    = "sparse_vector"
	fieldContent         = "content"
	fieldSourceID        = "source_id"
	fieldSourceType      = "source_type"
	fieldChunkID         = "chunk_id"
	fieldKnowledgeID     = "knowledge_id"
	fieldKnowledgeBaseID = "knowledge_base_id"
	fieldTagID           = "tag_id"
	fieldIsEnabled       = "is_enabled"
)

type repository struct {
	client             *tcvectordb.RpcClient
	databaseName       string
	collectionBaseName string
	shardsNum          int
	replicasNum        int
	initialized        sync.Map
	bm25Once           sync.Once
	bm25               encoder.SparseEncoder
	bm25Err            error
}

type vectorEmbedding struct {
	ID              string
	Content         string
	SourceID        string
	SourceType      int
	ChunkID         string
	KnowledgeID     string
	KnowledgeBaseID string
	TagID           string
	Embedding       []float32
	SparseVector    []encoder.SparseVecItem
	IsEnabled       bool
	Score           float64
}
