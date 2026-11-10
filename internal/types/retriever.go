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
	// ExcludeGeneratedQuestionKBIDs drops generated-question index rows for
	// these knowledge bases before TopK is applied. Document knowledge bases
	// with question generation turned off are listed here so historical
	// question vectors stop occupying recall. FAQ similar-question rows share
	// the source-id shape and must not be listed.
	ExcludeGeneratedQuestionKBIDs []string
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
	// VectorScore is a vector hit's similarity as its engine reported it.
	// Fusion replaces Score with a rank-based one; this keeps the evidence of
	// how close the vector actually was, which reranking weighs for images.
	// Zero for keyword hits.
	VectorScore float64
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
	// Error reports a partial failure of this result set: Results holds what
	// answered, Error says what did not. It is nil for a complete retrieval,
	// including a genuine zero-hit search. A store-level failure is still
	// returned as the call's error with no result set at all.
	// CompositeRetrieveEngine surfaces a non-nil Error as an error alongside the
	// results, so a caller that reads the results must not ignore the error
	// without at least logging that the answer is incomplete.
	Error error // Retrieval error
}
