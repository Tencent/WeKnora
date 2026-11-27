package types

// KnowledgeSourceContent is an editable UTF-8 source snapshot. Revision is an
// opaque concurrency token; clients must send it back unchanged when saving.
type KnowledgeSourceContent struct {
	Content  string `json:"content"`
	Revision string `json:"revision"`
	FileName string `json:"file_name"`
}

// MaxKnowledgeSourceBytes bounds both source reads and editor submissions.
const MaxKnowledgeSourceBytes = 2 * 1024 * 1024
