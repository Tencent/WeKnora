package types

// WikiPageProvenanceResponse is the current published page revision together
// with the smallest user-visible blocks that can be attributed independently.
// Legacy pages may legitimately return an empty block list.
type WikiPageProvenanceResponse struct {
	PageID           string                    `json:"page_id"`
	PageRevisionID   string                    `json:"page_revision_id,omitempty"`
	RevisionNo       int                       `json:"revision_no"`
	ProvenanceStatus WikiProvenanceStatus      `json:"provenance_status,omitempty"`
	Blocks           []WikiPageProvenanceBlock `json:"blocks"`
}

// WikiPageProvenanceBlock is one rendered page block plus its evidence edges.
type WikiPageProvenanceBlock struct {
	ID               string                     `json:"id"`
	LogicalBlockID   string                     `json:"logical_block_id"`
	BlockType        WikiBlockType              `json:"block_type"`
	SortOrder        int                        `json:"sort_order"`
	Content          string                     `json:"content"`
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
	EvidenceExcerpt     string                     `json:"evidence_excerpt,omitempty"`
	EvidenceHash        string                     `json:"evidence_hash,omitempty"`
	SourceRole          WikiSourceRole             `json:"source_role"`
	Confidence          float64                    `json:"confidence"`
	ValidationStatus    WikiSourceValidationStatus `json:"validation_status"`
	SourceAvailable     bool                       `json:"source_available"`
}
