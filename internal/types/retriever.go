package types

// RetrieverEngineType represents the type of retriever engine
type RetrieverEngineType string

// RetrieverEngineType constants
const (
	PostgresRetrieverEngineType        RetrieverEngineType = "postgres"
	ElasticsearchRetrieverEngineType   RetrieverEngineType = "elasticsearch"
	InfinityRetrieverEngineType        RetrieverEngineType = "infinity"
	ElasticFaissRetrieverEngineType    RetrieverEngineType = "elasticfaiss"
	QdrantRetrieverEngineType          RetrieverEngineType = "qdrant"
	MilvusRetrieverEngineType          RetrieverEngineType = "milvus"
	WeaviateRetrieverEngineType        RetrieverEngineType = "weaviate"
	DorisRetrieverEngineType           RetrieverEngineType = "doris"
	SQLiteRetrieverEngineType          RetrieverEngineType = "sqlite"
	TencentVectorDBRetrieverEngineType RetrieverEngineType = "tencent_vectordb"
	// OpenSearchRetrieverEngineType identifies the OpenSearch k-NN driver
	// introduced in Phase 3 (see issue tracker). The driver itself ships
	// in a subsequent PR; this constant exists so that
	// EngineAwareNormalizer can register the OpenSearch case ahead of the
	// driver landing and the AuditAction constants can reference it
	// without forward dependencies. Wire value "opensearch" mirrors the
	// official product name and matches the value used in
	// retrieverEngineMapping / GetVectorStoreTypes once activation lands.
	OpenSearchRetrieverEngineType RetrieverEngineType = "opensearch"
)

// RetrieverType represents the type of retriever
type RetrieverType string

// RetrieverType constants
const (
	KeywordsRetrieverType  RetrieverType = "keywords"  // Keywords retriever
	VectorRetrieverType    RetrieverType = "vector"    // Vector retriever
	WebSearchRetrieverType RetrieverType = "websearch" // Web search retriever
)

// ScoreSemantics values declared by external retriever plugins (via the
// Describe RPC) to tell the host what a raw score means so results can be
// normalized across engines. The contract is:
//
//   - similarity_higher_better: score is already a [0,1] similarity where
//     higher is better (plugins MUST normalize raw cosine / dot product into
//     [0,1] themselves). The host only clamps.
//   - distance_lower_better: score is a distance where lower is better
//     (e.g. L2 / cosine distance). The host maps it via 1-score and clamps.
//   - rank_only: score carries no meaningful magnitude; only ordering
//     matters (e.g. keyword hits without BM25 scoring). The host passes it
//     through unchanged and lets RRF rank fusion handle ordering.
const (
	ScoreSemanticsSimilarityHigherBetter = "similarity_higher_better"
	ScoreSemanticsDistanceLowerBetter    = "distance_lower_better"
	ScoreSemanticsRankOnly               = "rank_only"
)

// RetrieveParams represents the parameters for retrieval
type RetrieveParams struct {
	// Query text
	Query string
	// Query embedding (used for vector retrieval)
	Embedding []float32
	// Knowledge base IDs
	KnowledgeBaseIDs []string
	// Knowledge IDs
	KnowledgeIDs []string
	// Tag IDs for filtering (used for FAQ priority filtering)
	TagIDs []string
	// Excluded knowledge IDs
	ExcludeKnowledgeIDs []string
	// Excluded chunk IDs
	ExcludeChunkIDs []string
	// Number of results to return
	TopK int
	// Similarity threshold
	Threshold float64
	// Knowledge type (e.g., "faq", "manual") - determines which index to use
	KnowledgeType string
	// Additional parameters, different retrievers may require different parameters
	AdditionalParams map[string]interface{}
	// Retriever type
	RetrieverType RetrieverType // Retriever type
}

// RetrieverEngineParams represents the parameters for retriever engine
type RetrieverEngineParams struct {
	// Retriever engine type
	RetrieverEngineType RetrieverEngineType `yaml:"retriever_engine_type" json:"retriever_engine_type"`
	// Retriever type
	RetrieverType RetrieverType `yaml:"retriever_type"        json:"retriever_type"`
}

// IndexWithScore represents the index with score
type IndexWithScore struct {
	// ID
	ID string
	// Content
	Content string
	// Source ID
	SourceID string
	// Source type
	SourceType SourceType
	// Chunk ID
	ChunkID string
	// Knowledge ID
	KnowledgeID string
	// Knowledge base ID
	KnowledgeBaseID string
	// Tag ID
	TagID string
	// Score
	Score float64
	// Match type
	MatchType MatchType
	// IsEnabled
	IsEnabled bool
}

// GetScore returns the score for ScoreComparable interface
func (i *IndexWithScore) GetScore() float64 {
	return i.Score
}

// RetrieveResult represents the result of retrieval
type RetrieveResult struct {
	Results             []*IndexWithScore   // Retrieval results
	RetrieverEngineType RetrieverEngineType // Retrieval source type
	RetrieverType       RetrieverType       // Retrieval type
	Error               error               // Retrieval error
}
