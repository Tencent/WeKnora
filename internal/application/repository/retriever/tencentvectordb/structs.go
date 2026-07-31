package tencentvectordb

import (
	"sync"

	"github.com/tencent/vectordatabase-sdk-go/tcvdbtext/encoder"
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
)

const (
	envTencentVectorDBDatabase   = "TENCENT_VECTORDB_DATABASE"
	envTencentVectorDBCollection = "TENCENT_VECTORDB_COLLECTION"
	envTencentVectorDBReplicaNum = "TENCENT_VECTORDB_REPLICA_NUMBER"
	defaultDatabaseName          = "weknora"
	defaultCollectionName        = "weknora_embeddings"
	defaultReplicaNumber         = 1

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
	fieldFolderID        = "folder_id"
	fieldIsEnabled       = "is_enabled"
)

type repository struct {
	client             *tcvectordb.RpcClient
	databaseName       string
	collectionBaseName string
	useDimensionSuffix bool
	shardsNum          int
	replicasNum        int
	// folderSchemaPresent lets ordinary reads and writes continue while the
	// external service builds the folder filter index. Folder-scoped reads use
	// folderIndexReady instead and keep failing closed until construction ends.
	folderSchemaPresent sync.Map
	folderIndexReady    sync.Map
	ensureMu            sync.Mutex
	bm25Once            sync.Once
	bm25                encoder.SparseEncoder
	bm25Err             error
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
	FolderID        string
	Embedding       []float32
	SparseVector    []encoder.SparseVecItem
	IsEnabled       bool
	Score           float64
}
