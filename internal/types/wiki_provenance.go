package types

import "time"

const (
	WikiBlockSetStatusStaged     = "staged"
	WikiBlockSetStatusPublished  = "published"
	WikiBlockSetStatusSuperseded = "superseded"
	WikiBlockSetStatusFailed     = "failed"
)

const (
	WikiBlockTypeSummary   = "summary"
	WikiBlockTypeHeading   = "heading"
	WikiBlockTypeParagraph = "paragraph"
	WikiBlockTypeListItem  = "list_item"
	WikiBlockTypeTableRow  = "table_row"
	WikiBlockTypeQuote     = "quote"
)

const (
	WikiBlockProvenanceVerified       = "verified"
	WikiBlockProvenancePartial        = "partial"
	WikiBlockProvenanceUnsupported    = "unsupported"
	WikiBlockProvenanceLegacyInferred = "legacy_inferred"
)

const (
	WikiSourceValidationLocated = "located"
	WikiSourceValidationInvalid = "invalid"
)

// IsWikiProvenanceChunkType reports whether a chunk contains text that Wiki
// generation is allowed to quote as evidence. Empty is the legacy text value;
// OCR and image captions are first-class textual sources for scanned/image
// documents and must be accepted consistently from alignment through publish.
func IsWikiProvenanceChunkType(chunkType string) bool {
	switch chunkType {
	case "", ChunkTypeText, ChunkTypeImageOCR, ChunkTypeImageCaption:
		return true
	default:
		return false
	}
}

// WikiPageBlockSet is one immutable structural representation of a wiki page
// version. The current pointer lives on WikiPage; staged sets are invisible
// until an atomic publish switches that pointer.
type WikiPageBlockSet struct {
	ID              string           `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64           `json:"tenant_id" gorm:"index"`
	KnowledgeBaseID string           `json:"knowledge_base_id" gorm:"type:varchar(36);index:idx_wiki_block_sets_kb_page_status"`
	PageID          string           `json:"page_id" gorm:"type:varchar(36);uniqueIndex:idx_wiki_block_sets_page_version,where:status = 'published' OR status = 'superseded';index:idx_wiki_block_sets_kb_page_status"`
	PageVersion     int              `json:"page_version" gorm:"uniqueIndex:idx_wiki_block_sets_page_version,where:status = 'published' OR status = 'superseded'"`
	Status          string           `json:"status" gorm:"type:varchar(16);default:'staged';index:idx_wiki_block_sets_kb_page_status"`
	RenderedContent string           `json:"rendered_content" gorm:"type:text"`
	RenderedSummary string           `json:"rendered_summary" gorm:"type:text"`
	GenerationRunID string           `json:"generation_run_id,omitempty" gorm:"type:varchar(64);default:''"`
	Blocks          []*WikiPageBlock `json:"blocks" gorm:"foreignKey:BlockSetID"`
	CreatedAt       time.Time        `json:"created_at"`
	PublishedAt     *time.Time       `json:"published_at,omitempty"`
}

func (WikiPageBlockSet) TableName() string { return "wiki_page_block_sets" }

// WikiPageBlock is the smallest independently sourced unit rendered on a wiki
// page. LogicalBlockID remains stable when an unchanged fact survives a new
// page version; ID identifies only this block-set snapshot.
type WikiPageBlock struct {
	ID               string             `json:"id" gorm:"type:varchar(36);primaryKey"`
	BlockSetID       string             `json:"block_set_id" gorm:"type:varchar(36);uniqueIndex:idx_wiki_blocks_set_order"`
	LogicalBlockID   string             `json:"logical_block_id" gorm:"type:varchar(36);index"`
	BlockType        string             `json:"block_type" gorm:"type:varchar(24)"`
	SectionPath      StringArray        `json:"section_path,omitempty" gorm:"type:json"`
	SortOrder        int                `json:"sort_order" gorm:"uniqueIndex:idx_wiki_blocks_set_order"`
	Content          string             `json:"content" gorm:"type:text"`
	ContentHash      string             `json:"content_hash" gorm:"type:varchar(64)"`
	AuthorType       string             `json:"author_type" gorm:"type:varchar(16);default:'pipeline'"`
	ProvenanceStatus string             `json:"provenance_status" gorm:"type:varchar(24);default:'unsupported'"`
	Sources          []*WikiBlockSource `json:"sources" gorm:"foreignKey:BlockID"`
	CreatedAt        time.Time          `json:"created_at"`
}

func (WikiPageBlock) TableName() string { return "wiki_page_blocks" }

// WikiBlockSource records a concrete piece of evidence supporting one block.
// DocumentTitle is copied at generation time so provenance remains displayable
// after the originating knowledge row is deleted or renamed.
type WikiBlockSource struct {
	ID               string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID         uint64    `json:"tenant_id" gorm:"index"`
	KnowledgeBaseID  string    `json:"knowledge_base_id" gorm:"type:varchar(36);index:idx_wiki_block_sources_knowledge"`
	BlockID          string    `json:"block_id" gorm:"type:varchar(36);uniqueIndex:idx_wiki_block_sources_evidence;index"`
	KnowledgeID      string    `json:"knowledge_id" gorm:"type:varchar(36);index:idx_wiki_block_sources_knowledge"`
	DocumentTitle    string    `json:"document_title,omitempty" gorm:"column:source_title;type:varchar(512);default:''"`
	KnowledgeAttempt int       `json:"knowledge_attempt" gorm:"default:0"`
	ChunkID          string    `json:"chunk_id" gorm:"type:varchar(36);uniqueIndex:idx_wiki_block_sources_evidence;index"`
	ChunkRevision    int       `json:"chunk_revision" gorm:"default:0"`
	SortOrder        int       `json:"sort_order" gorm:"default:0"`
	Evidence         string    `json:"evidence" gorm:"type:text"`
	EvidenceHash     string    `json:"evidence_hash" gorm:"type:varchar(64);uniqueIndex:idx_wiki_block_sources_evidence"`
	ChunkContentHash string    `json:"chunk_content_hash" gorm:"type:varchar(64)"`
	ValidationStatus string    `json:"validation_status" gorm:"type:varchar(16);default:'invalid'"`
	CitationKey      string    `json:"citation_key,omitempty" gorm:"-"`
	CreatedAt        time.Time `json:"created_at"`
}

func (WikiBlockSource) TableName() string { return "wiki_block_sources" }

// WikiPageDetailResponse is returned only by source-aware page reads. Regular
// endpoints continue returning WikiPage, preserving the legacy wire shape.
// Blocks intentionally lacks omitempty so include_sources=true returns [] for
// legacy pages instead of making provenance availability ambiguous.
type WikiPageDetailResponse struct {
	*WikiPage
	Blocks []*WikiPageBlock `json:"blocks"`
}

// WikiKnowledgeBlockReference is the lightweight reverse lookup used by
// document deletion/reparse to find current published blocks that cite it.
type WikiKnowledgeBlockReference struct {
	PageID           string `json:"page_id"`
	PageSlug         string `json:"page_slug"`
	BlockSetID       string `json:"block_set_id"`
	BlockID          string `json:"block_id"`
	LogicalBlockID   string `json:"logical_block_id"`
	BlockType        string `json:"block_type"`
	Content          string `json:"content"`
	AuthorType       string `json:"author_type"`
	ProvenanceStatus string `json:"provenance_status"`
	SourceID         string `json:"source_id"`
	DocumentTitle    string `json:"document_title,omitempty" gorm:"column:source_title"`
	ChunkID          string `json:"chunk_id"`
	KnowledgeAttempt int    `json:"knowledge_attempt"`
	ChunkRevision    int    `json:"chunk_revision"`
	ValidationStatus string `json:"validation_status"`
}
