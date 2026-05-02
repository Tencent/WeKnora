package types

// ReadRequest is the unified transport-agnostic request for document reading.
// Set FileContent for file mode, URL for URL mode.
type ReadRequest struct {
	FileContent           []byte
	FileName              string
	FileType              string
	URL                   string
	Title                 string
	ParserEngine          string
	RequestID             string
	ParserEngineOverrides map[string]string
}

// ReadResult is the transport-agnostic result of document reading.
type ReadResult struct {
	MarkdownContent string
	ImageRefs       []ImageRef
	ImageDirPath    string
	Metadata        map[string]string
	Error           string
	IsAudio         bool   // true when the result contains raw audio data needing ASR transcription
	AudioData       []byte // raw audio bytes for ASR processing
}

// ImageRef represents an image reference extracted from the document.
type ImageRef struct {
	Filename    string
	OriginalRef string
	MimeType    string
	StorageKey  string
	ImageData   []byte // inline image bytes (universal fallback for cross-machine deployments)
	// IsOriginal marks references that point to the originally uploaded file
	// itself (e.g. when the user uploads a standalone image). Such references
	// must not be dropped by the icon/size filter — otherwise a small image
	// upload would be silently discarded before multimodal processing.
	IsOriginal bool
}

// ParserEngineInfo describes a registered parser engine.
type ParserEngineInfo struct {
	Name              string
	Description       string
	FileTypes         []string
	Available         bool
	UnavailableReason string
}

// --- Internal types used by chunking pipeline ---

type DocParserStorageConfig struct {
	Provider        string
	Region          string
	BucketName      string
	AccessKeyID     string
	SecretAccessKey string
	AppID           string
	PathPrefix      string
	Endpoint        string
}

type DocParserVLMConfig struct {
	ModelName     string
	BaseURL       string
	APIKey        string
	InterfaceType string
}

type ParsedChunk struct {
	Content string
	// ContextHeader is an optional context string (e.g. a Markdown heading
	// breadcrumb) that should be prepended at embedding time but is NOT
	// part of the stored Content. Lets retrieval pipelines see section
	// context without breaking End-Start == len(Content) invariants.
	ContextHeader string
	Seq           int
	Start         int
	End           int
	Images        []ParsedImage
	ChunkID       string // populated by processChunks with the actual DB UUID

	// ParentIndex is set when using parent-child chunking strategy.
	// -1 (or unset/0 for flat chunks) means this is a top-level chunk.
	// >= 0 means this is a child chunk referencing the parent at this index
	// in the ParentChunks slice of ProcessChunksOptions.
	ParentIndex int
}

// EmbeddingContent returns the text that should be sent to the embedding
// model: ContextHeader (if any) prepended to Content. Mirrors
// chunker.Chunk.EmbeddingContent so the choice is consistent across the
// chunker output and the indexing pipeline.
func (c ParsedChunk) EmbeddingContent() string {
	if c.ContextHeader == "" {
		return c.Content
	}
	return c.ContextHeader + "\n\n" + c.Content
}

// ParsedParentChunk represents a parent chunk in the parent-child strategy.
// Parent chunks are stored in DB for context retrieval but NOT vector-indexed.
type ParsedParentChunk struct {
	Content string
	Seq     int
	Start   int
	End     int
}

type ParsedImage struct {
	URL         string
	Caption     string
	OCRText     string
	OriginalURL string
	Start       int
	End         int
}
