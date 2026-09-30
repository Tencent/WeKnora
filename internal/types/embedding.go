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
	MatchTypeWebSearch    // 网络搜索匹配类型
	MatchTypeDirectLoad   // Deprecated: reserved to preserve serialized enum values
	MatchTypeDataAnalysis // 数据分析匹配类型
)

// EmbeddingModality describes what an indexed entry is, and therefore how its
// vector has to be produced. It lives in types so IndexInfo can carry it
// without types importing internal/models/embedding (an import cycle).
type EmbeddingModality string

const (
	// EmbeddingModalityText is the historical behavior: Content is embedded as
	// text. The zero value, so every existing IndexInfo keeps working.
	EmbeddingModalityText EmbeddingModality = "text"
	// EmbeddingModalityImage asks the pipeline to embed the image itself, so a
	// text query can recall it without going through OCR or a caption.
	EmbeddingModalityImage EmbeddingModality = "image"
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

	// Modality selects how the vector is produced. Empty means text, which is
	// the only behavior that existed before multimodal support.
	Modality EmbeddingModality
	// ImageBytes is the raw encoded image for EmbeddingModalityImage entries.
	// Request-scoped: never persisted, because the chunk's ImageInfo already
	// carries the URL needed to render the image at retrieval time.
	ImageBytes []byte
	// ImageMIME is optional and sniffs from ImageBytes when empty.
	ImageMIME string

	// MultimodalEnvelope requests that this entry be encoded through the
	// model's unified multimodal envelope (the chat-style `messages` request)
	// even when it carries only text.
	//
	// WHY: multimodal servers render `messages` through a chat template the
	// plain `input` array has no equivalent of (vLLM prepends a system
	// instruction and a trailing assistant turn), so the same sentence lands in
	// a different place through each envelope. Images can only travel through
	// `messages`, so a KB that stores image vectors is committed to that space
	// and every text chunk in the collection must be encoded the same way or a
	// query cannot reach it — retrieval still returns k results, they are just
	// noise, and nothing errors.
	//
	// Per-entry rather than a KB-level flag on the engine because document
	// indexing runs inside serialized background tasks where request context
	// does not survive, and an explicit stamp is auditable at each call site.
	MultimodalEnvelope bool
}

// IsImage reports whether this entry must be embedded from image bytes rather
// than from Content.
func (i *IndexInfo) IsImage() bool { return i != nil && i.Modality == EmbeddingModalityImage }

// MarkMultimodalEnvelope stamps every entry in the batch so its vector is
// produced in the unified multimodal space. Callers must apply it to ALL
// entries of a KB whose IndexingStrategy.NeedsImageVector() is true, including
// plain text chunks: mixing splits the collection into two unreachable spaces.
func MarkMultimodalEnvelope(infos []*IndexInfo) {
	for _, info := range infos {
		if info != nil {
			info.MultimodalEnvelope = true
		}
	}
}
