package types

// SourceType represents the type of content source
type SourceType int

const (
	ChunkSourceType   SourceType = iota // Source is a text chunk
	PassageSourceType                   // Source is a passage
	SummarySourceType                   // Source is a summary
)

// MatchType represents the type of matching algorithm
type MatchType int

const (
	MatchTypeEmbedding MatchType = iota
	MatchTypeKeywords
	MatchTypeNearByChunk
	MatchTypeHistory
	MatchTypeParentChunk   // 父Chunk匹配类型
	MatchTypeRelationChunk // 关系Chunk匹配类型
	MatchTypeGraph
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
}
