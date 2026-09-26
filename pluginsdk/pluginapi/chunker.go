package pluginapi

// ChunkPath is the split endpoint of chunker id.
func ChunkPath(id string) string { return "/v1/chunkers/" + id + "/split" }

// ChunkInput is a document (Markdown, after parsing) to cut into chunks,
// with the knowledge base's chunking settings as hints.
type ChunkInput struct {
	Text string `json:"text"`
	// ChunkSize is the target chunk length in characters (Unicode code
	// points); ChunkOverlap how many may repeat between neighbours.
	ChunkSize    int      `json:"chunkSize"`
	ChunkOverlap int      `json:"chunkOverlap,omitempty"`
	Separators   []string `json:"separators,omitempty"`
	// TokenLimit, when set, caps a chunk in approximate tokens.
	TokenLimit int      `json:"tokenLimit,omitempty"`
	Languages  []string `json:"languages,omitempty"`
}

// ChunkOutput is where to cut. WeKnora takes each chunk's text from the
// document itself, so chunks can only be spans of it.
type ChunkOutput struct {
	Chunks []ChunkSpan `json:"chunks"`
}

// ChunkSpan is one chunk: [Start, End) in characters (Unicode code points,
// not bytes) of ChunkInput.Text.
type ChunkSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
	// ContextHeader is prepended to the chunk for embedding, such as the
	// headings it sits under ("Contract > Section 4").
	ContextHeader string `json:"contextHeader,omitempty"`
}
