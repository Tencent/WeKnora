package types

// WikiPageProvenanceResponse is the current published page revision together
// with the smallest user-visible blocks that can be attributed independently.
// Legacy pages may legitimately return an empty block list.
type WikiPageProvenanceResponse struct {
	PageID             string                    `json:"page_id"`
	PageRevisionID     string                    `json:"page_revision_id,omitempty"`
	RevisionNo         int                       `json:"revision_no"`
	CurrentPageVersion int                       `json:"current_page_version"`
	CurrentEditSource  string                    `json:"current_edit_source,omitempty"`
	StaleReason        string                    `json:"stale_reason,omitempty"`
	ProvenanceStatus   WikiProvenanceStatus      `json:"provenance_status,omitempty"`
	Blocks             []WikiPageProvenanceBlock `json:"blocks"`
}

const (
	// WikiProvenanceStalePageEdited means the current page was authored outside
	// the ingest pipeline. The older block ledger remains valid for history but
	// must not be attached to the edited current text.
	WikiProvenanceStalePageEdited = "page_edited"
	// WikiProvenanceStaleVersionMismatch is the defensive fallback for a
	// pipeline-authored page whose current version and ledger revision diverge.
	WikiProvenanceStaleVersionMismatch = "version_mismatch"
)

// WikiPageProvenanceBlock is one rendered page block plus its evidence edges.
type WikiPageProvenanceBlock struct {
	ID               string                     `json:"id"`
	LogicalBlockID   string                     `json:"logical_block_id"`
	BlockType        WikiBlockType              `json:"block_type"`
	SortOrder        int                        `json:"sort_order"`
	Content          string                     `json:"content"`
	AuthorType       WikiBlockAuthorType        `json:"author_type"`
	ProvenanceStatus WikiProvenanceStatus       `json:"provenance_status"`
	Sources          []WikiPageProvenanceSource `json:"sources"`
}

// WikiPageProvenanceSource contains display-safe document metadata and a
// bounded evidence excerpt. Internal tenant information is deliberately not
// exposed because the request is already scoped by the KB route.
type WikiPageProvenanceSource struct {
	KnowledgeID         string                     `json:"knowledge_id"`
	KnowledgeRevisionID string                     `json:"knowledge_revision_id"`
	KnowledgeRevisionNo int                        `json:"knowledge_revision_no"`
	ParseAttempt        int                        `json:"parse_attempt"`
	KnowledgeTitle      string                     `json:"knowledge_title"`
	FileName            string                     `json:"file_name,omitempty"`
	FileType            string                     `json:"file_type,omitempty"`
	ChunkID             *string                    `json:"chunk_id,omitempty"`
	ChunkIndex          *int                       `json:"chunk_index,omitempty"`
	SourceStart         int                        `json:"source_start"`
	SourceEnd           int                        `json:"source_end"`
	EvidenceExcerpt     string                     `json:"evidence_excerpt,omitempty"`
	EvidenceHash        string                     `json:"evidence_hash,omitempty"`
	SourceRole          WikiSourceRole             `json:"source_role"`
	Confidence          float64                    `json:"confidence"`
	ValidationStatus    WikiSourceValidationStatus `json:"validation_status"`
	SourceAvailable     bool                       `json:"source_available"`
}
