package types

// SourceType represents the type of content source
type SourceType int

// PassageSourceType and related constants.
const (
	// ChunkSourceType means the indexed source is a text chunk.
	ChunkSourceType   SourceType = iota
	PassageSourceType            // Source is a passage
	SummarySourceType            // Source is a summary
)

// MatchType represents the type of matching algorithm
type MatchType int

// MatchTypeKeywords and related constants.
const (
	// MatchTypeEmbedding matches via vector embedding similarity.
	MatchTypeEmbedding MatchType = iota
	MatchTypeKeywords
	MatchTypeNearByChunk
	MatchTypeHistory
	MatchTypeParentChunk   // 父Chunk匹配类型
	MatchTypeRelationChunk // 关系Chunk匹配类型
	MatchTypeGraph
	MatchTypeWebSearch    // 网络搜索匹配类型
	MatchTypeDirectLoad   // Deprecated: reserved to preserve serialized enum values
	MatchTypeDataAnalysis // 数据分析匹配类型
)

// IndexInfo contains information about indexed content
type IndexInfo struct {
	ID              string     // Unique identifier
	Content         string     // Content text
	SourceID        string     // ID of the source document
	SourceType      SourceType // Type of the source
	ChunkID         string     // ID of the text chunk
	KnowledgeID     string     // ID of the knowledge
	KnowledgeBaseID string     // ID of the knowledge base
	KnowledgeType   string     // Type of the knowledge (e.g., "faq", "manual")
	TagID           string     // Tag ID for categorization (used for FAQ priority filtering)
	IsEnabled       bool       // Whether the chunk is enabled for retrieval
	IsRecommended   bool       // Whether the chunk is recommended
}
