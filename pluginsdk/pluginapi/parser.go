package pluginapi

// ParsePath is the parse endpoint of parser id.
func ParsePath(id string) string { return "/v1/parsers/" + id + "/parse" }

// ParseInput is one document to parse: its bytes (file mode) or a URL (URL
// mode). The parser's own settings arrive in Config.System and Config.Tenant.
type ParseInput struct {
	FileName string `json:"fileName,omitempty"`
	// FileType is the lower-case extension without the dot ("pdf").
	FileType string `json:"fileType,omitempty"`
	// Content is the file; JSON carries it as base64.
	Content []byte `json:"content,omitempty"`
	URL     string `json:"url,omitempty"`
	Title   string `json:"title,omitempty"`
}

// ParseOutput is the document as Markdown. WeKnora chunks it; parsers do not.
type ParseOutput struct {
	Markdown string `json:"markdown"`
	// Images are the pictures the Markdown references: an image whose
	// OriginalRef equals a ![](target) in Markdown is stored and the link
	// rewritten. Inline data: URIs in the Markdown work too.
	Images []ParsedImage `json:"images,omitempty"`
	// Metadata such as "title" (URL mode) or "pages".
	Metadata map[string]string `json:"metadata,omitempty"`
}

// ParsedImage is one image of a parsed document.
type ParsedImage struct {
	OriginalRef string `json:"originalRef"`
	MimeType    string `json:"mimeType,omitempty"`
	Data        []byte `json:"data"`
}
