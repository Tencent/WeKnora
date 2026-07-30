package types

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// KnowledgeRevisionStatus describes the lifecycle of an immutable source
// document revision. Exactly one non-deleted revision per knowledge may be
// published at a time (enforced by the PostgreSQL migration).
type KnowledgeRevisionStatus string

const (
	KnowledgeRevisionStaged     KnowledgeRevisionStatus = "staged"
	KnowledgeRevisionPublished  KnowledgeRevisionStatus = "published"
	KnowledgeRevisionFailed     KnowledgeRevisionStatus = "failed"
	KnowledgeRevisionSuperseded KnowledgeRevisionStatus = "superseded"
	KnowledgeRevisionDeleted    KnowledgeRevisionStatus = "deleted"
)

func (s KnowledgeRevisionStatus) IsValid() bool {
	switch s {
	case KnowledgeRevisionStaged,
		KnowledgeRevisionPublished,
		KnowledgeRevisionFailed,
		KnowledgeRevisionSuperseded,
		KnowledgeRevisionDeleted:
		return true
	default:
		return false
	}
}

// WikiPageRevisionStatus describes the publish lifecycle of a complete Wiki
// page snapshot. Writers build staged revisions and expose them only after all
// blocks and provenance edges have been validated.
type WikiPageRevisionStatus string

const (
	WikiPageRevisionStaged     WikiPageRevisionStatus = "staged"
	WikiPageRevisionPublished  WikiPageRevisionStatus = "published"
	WikiPageRevisionFailed     WikiPageRevisionStatus = "failed"
	WikiPageRevisionSuperseded WikiPageRevisionStatus = "superseded"
	WikiPageRevisionDeleted    WikiPageRevisionStatus = "deleted"
)

func (s WikiPageRevisionStatus) IsValid() bool {
	switch s {
	case WikiPageRevisionStaged,
		WikiPageRevisionPublished,
		WikiPageRevisionFailed,
		WikiPageRevisionSuperseded,
		WikiPageRevisionDeleted:
		return true
	default:
		return false
	}
}

// WikiProvenanceStatus expresses how trustworthy a generated page or block's
// source mapping is. legacy_inferred is deliberately distinct from verified:
// old source_refs/chunk_refs cannot prove sentence-level attribution.
type WikiProvenanceStatus string

const (
	WikiProvenanceVerified       WikiProvenanceStatus = "verified"
	WikiProvenancePartial        WikiProvenanceStatus = "partial"
	WikiProvenanceUnsupported    WikiProvenanceStatus = "unsupported"
	WikiProvenanceLegacyInferred WikiProvenanceStatus = "legacy_inferred"
)

func (s WikiProvenanceStatus) IsValid() bool {
	switch s {
	case WikiProvenanceVerified,
		WikiProvenancePartial,
		WikiProvenanceUnsupported,
		WikiProvenanceLegacyInferred:
		return true
	default:
		return false
	}
}

// WikiBlockType is the smallest independently attributable rendered unit.
// Most generated prose should use fact/paragraph; list items and table rows
// remain separate so deleting one source does not force a whole section rewrite.
type WikiBlockType string

const (
	WikiBlockDocument  WikiBlockType = "document"
	WikiBlockTitle     WikiBlockType = "title"
	WikiBlockSummary   WikiBlockType = "summary"
	WikiBlockHeading   WikiBlockType = "heading"
	WikiBlockParagraph WikiBlockType = "paragraph"
	WikiBlockFact      WikiBlockType = "fact"
	WikiBlockListItem  WikiBlockType = "list_item"
	WikiBlockTableRow  WikiBlockType = "table_row"
	WikiBlockQuote     WikiBlockType = "quote"
	WikiBlockCode      WikiBlockType = "code"
	WikiBlockOther     WikiBlockType = "other"
)

func (t WikiBlockType) IsValid() bool {
	switch t {
	case WikiBlockDocument,
		WikiBlockTitle,
		WikiBlockSummary,
		WikiBlockHeading,
		WikiBlockParagraph,
		WikiBlockFact,
		WikiBlockListItem,
		WikiBlockTableRow,
		WikiBlockQuote,
		WikiBlockCode,
		WikiBlockOther:
		return true
	default:
		return false
	}
}

// WikiBlockAuthorType separates source-managed content from user-authored
// content. Deletion may remove unsupported generated blocks, but must not
// silently remove manual blocks.
type WikiBlockAuthorType string

const (
	WikiBlockAuthorGenerated WikiBlockAuthorType = "generated"
	WikiBlockAuthorManual    WikiBlockAuthorType = "manual"
	WikiBlockAuthorAgent     WikiBlockAuthorType = "agent"
	WikiBlockAuthorUnknown   WikiBlockAuthorType = "unknown"
)

func (t WikiBlockAuthorType) IsValid() bool {
	switch t {
	case WikiBlockAuthorGenerated,
		WikiBlockAuthorManual,
		WikiBlockAuthorAgent,
		WikiBlockAuthorUnknown:
		return true
	default:
		return false
	}
}

// WikiSourceRole describes how a source participates in a block's meaning.
type WikiSourceRole string

const (
	WikiSourceSupporting    WikiSourceRole = "supporting"
	WikiSourceContext       WikiSourceRole = "context"
	WikiSourceContradicting WikiSourceRole = "contradicting"
	WikiSourceSupplementary WikiSourceRole = "supplementary"
)

func (r WikiSourceRole) IsValid() bool {
	switch r {
	case WikiSourceSupporting,
		WikiSourceContext,
		WikiSourceContradicting,
		WikiSourceSupplementary:
		return true
	default:
		return false
	}
}

// WikiSourceValidationStatus records whether the backend verified the model's
// citation against the immutable source revision.
type WikiSourceValidationStatus string

const (
	WikiSourceValidationPending        WikiSourceValidationStatus = "pending"
	WikiSourceValidationVerified       WikiSourceValidationStatus = "verified"
	WikiSourceValidationInvalid        WikiSourceValidationStatus = "invalid"
	WikiSourceValidationLegacyInferred WikiSourceValidationStatus = "legacy_inferred"
)

func (s WikiSourceValidationStatus) IsValid() bool {
	switch s {
	case WikiSourceValidationPending,
		WikiSourceValidationVerified,
		WikiSourceValidationInvalid,
		WikiSourceValidationLegacyInferred:
		return true
	default:
		return false
	}
}

// WikiSourceMappingGranularity states how precisely a page-to-document
// relation is known. The page-level summary table is a projection; block-level
// evidence remains the source of truth.
type WikiSourceMappingGranularity string

const (
	WikiSourceMappingPage  WikiSourceMappingGranularity = "page"
	WikiSourceMappingBlock WikiSourceMappingGranularity = "block"
	WikiSourceMappingMixed WikiSourceMappingGranularity = "mixed"
)

func (g WikiSourceMappingGranularity) IsValid() bool {
	switch g {
	case WikiSourceMappingPage, WikiSourceMappingBlock, WikiSourceMappingMixed:
		return true
	default:
		return false
	}
}

// KnowledgeRevision is an immutable parse snapshot of one source document.
type KnowledgeRevision struct {
	ID              string                  `json:"id" gorm:"type:varchar(64);primaryKey"`
	TenantID        uint64                  `json:"tenant_id" gorm:"not null;index:idx_knowledge_revisions_tenant_kb,priority:1"`
	KnowledgeBaseID string                  `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index:idx_knowledge_revisions_tenant_kb,priority:2"`
	KnowledgeID     string                  `json:"knowledge_id" gorm:"type:varchar(36);not null;index"`
	RevisionNo      int                     `json:"revision_no" gorm:"not null"`
	ParseAttempt    int                     `json:"parse_attempt" gorm:"not null;default:0"`
	Status          KnowledgeRevisionStatus `json:"status" gorm:"type:varchar(32);not null;default:staged;index"`
	ContentHash     string                  `json:"content_hash" gorm:"type:varchar(64);not null;default:''"`
	CreatedAt       time.Time               `json:"created_at"`
	PublishedAt     *time.Time              `json:"published_at,omitempty"`
	SupersededAt    *time.Time              `json:"superseded_at,omitempty"`
	DeletedAt       gorm.DeletedAt          `json:"deleted_at" gorm:"index"`
}

func (KnowledgeRevision) TableName() string { return "knowledge_revisions" }

func (r *KnowledgeRevision) BeforeCreate(_ *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.Status == "" {
		r.Status = KnowledgeRevisionStaged
	}
	return r.Validate()
}

func (r *KnowledgeRevision) Validate() error {
	if r.TenantID == 0 || r.KnowledgeID == "" || r.KnowledgeBaseID == "" {
		return errors.New("knowledge revision requires tenant_id, knowledge_id and knowledge_base_id")
	}
	if r.RevisionNo <= 0 {
		return errors.New("knowledge revision number must be greater than zero")
	}
	if r.ParseAttempt < 0 {
		return errors.New("knowledge revision parse_attempt cannot be negative")
	}
	if !r.Status.IsValid() {
		return fmt.Errorf("invalid knowledge revision status %q", r.Status)
	}
	return nil
}

// WikiProvenancePageRevision is an immutable, renderable page snapshot.
type WikiProvenancePageRevision struct {
	ID                 string                 `json:"id" gorm:"type:varchar(64);primaryKey"`
	TenantID           uint64                 `json:"tenant_id" gorm:"not null;index:idx_wiki_provenance_page_revisions_tenant_kb,priority:1"`
	KnowledgeBaseID    string                 `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index:idx_wiki_provenance_page_revisions_tenant_kb,priority:2"`
	PageID             string                 `json:"page_id" gorm:"type:varchar(36);not null;index"`
	PublishKey         string                 `json:"publish_key,omitempty" gorm:"type:varchar(128);not null;default:'';index"`
	PublishFingerprint string                 `json:"publish_fingerprint,omitempty" gorm:"type:varchar(64);not null;default:''"`
	RevisionNo         int                    `json:"revision_no" gorm:"not null"`
	Status             WikiPageRevisionStatus `json:"status" gorm:"type:varchar(32);not null;default:staged;index"`
	Title              string                 `json:"title" gorm:"type:varchar(512);not null;default:''"`
	Summary            string                 `json:"summary" gorm:"type:text;not null;default:''"`
	RenderedContent    string                 `json:"rendered_content" gorm:"type:text;not null;default:''"`
	ContentHash        string                 `json:"content_hash" gorm:"type:varchar(64);not null;default:''"`
	ProvenanceStatus   WikiProvenanceStatus   `json:"provenance_status" gorm:"type:varchar(32);not null;default:partial;index"`
	CreatedAt          time.Time              `json:"created_at"`
	PublishedAt        *time.Time             `json:"published_at,omitempty"`
	SupersededAt       *time.Time             `json:"superseded_at,omitempty"`
	DeletedAt          gorm.DeletedAt         `json:"deleted_at" gorm:"index"`
}

func (WikiProvenancePageRevision) TableName() string { return "wiki_provenance_page_revisions" }

func (r *WikiProvenancePageRevision) BeforeCreate(_ *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.Status == "" {
		r.Status = WikiPageRevisionStaged
	}
	if r.ProvenanceStatus == "" {
		r.ProvenanceStatus = WikiProvenancePartial
	}
	return r.Validate()
}

func (r *WikiProvenancePageRevision) Validate() error {
	if r.TenantID == 0 || r.PageID == "" || r.KnowledgeBaseID == "" {
		return errors.New("wiki page revision requires tenant_id, page_id and knowledge_base_id")
	}
	if r.RevisionNo <= 0 {
		return errors.New("wiki page revision number must be greater than zero")
	}
	if !r.Status.IsValid() {
		return fmt.Errorf("invalid wiki page revision status %q", r.Status)
	}
	if !r.ProvenanceStatus.IsValid() {
		return fmt.Errorf("invalid wiki page provenance status %q", r.ProvenanceStatus)
	}
	return nil
}

// WikiPageBlock is the smallest independently attributable rendered unit.
type WikiPageBlock struct {
	ID               string               `json:"id" gorm:"type:varchar(64);primaryKey"`
	TenantID         uint64               `json:"tenant_id" gorm:"not null"`
	KnowledgeBaseID  string               `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index"`
	PageID           string               `json:"page_id" gorm:"type:varchar(36);not null;index"`
	PageRevisionID   string               `json:"page_revision_id" gorm:"type:varchar(64);not null;index"`
	LogicalBlockID   string               `json:"logical_block_id" gorm:"type:varchar(64);not null;index"`
	ParentBlockID    *string              `json:"parent_block_id,omitempty" gorm:"type:varchar(64);index"`
	BlockType        WikiBlockType        `json:"block_type" gorm:"type:varchar(32);not null"`
	SortOrder        int                  `json:"sort_order" gorm:"not null;default:0"`
	Content          string               `json:"content" gorm:"type:text;not null;default:''"`
	ContentHash      string               `json:"content_hash" gorm:"type:varchar(64);not null;default:''"`
	AuthorType       WikiBlockAuthorType  `json:"author_type" gorm:"type:varchar(32);not null;default:generated"`
	ProvenanceStatus WikiProvenanceStatus `json:"provenance_status" gorm:"type:varchar(32);not null;default:partial;index"`
	Metadata         JSON                 `json:"metadata" gorm:"type:json;not null;default:'{}'"`
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
}

func (WikiPageBlock) TableName() string { return "wiki_page_blocks" }

func (b *WikiPageBlock) BeforeCreate(_ *gorm.DB) error {
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	if b.LogicalBlockID == "" {
		b.LogicalBlockID = b.ID
	}
	if b.AuthorType == "" {
		b.AuthorType = WikiBlockAuthorGenerated
	}
	if b.ProvenanceStatus == "" {
		b.ProvenanceStatus = WikiProvenancePartial
	}
	return b.Validate()
}

func (b *WikiPageBlock) Validate() error {
	if b.TenantID == 0 || b.PageID == "" || b.PageRevisionID == "" || b.KnowledgeBaseID == "" {
		return errors.New("wiki page block requires tenant_id, page_id, page_revision_id and knowledge_base_id")
	}
	if b.SortOrder < 0 {
		return errors.New("wiki page block sort_order cannot be negative")
	}
	if !b.BlockType.IsValid() {
		return fmt.Errorf("invalid wiki block type %q", b.BlockType)
	}
	if !b.AuthorType.IsValid() {
		return fmt.Errorf("invalid wiki block author type %q", b.AuthorType)
	}
	if !b.ProvenanceStatus.IsValid() {
		return fmt.Errorf("invalid wiki block provenance status %q", b.ProvenanceStatus)
	}
	return nil
}

// WikiBlockSource is one validated (or explicitly unvalidated legacy) evidence
// edge from a Wiki block to a source document revision and optional chunk.
type WikiBlockSource struct {
	ID                  string                     `json:"id" gorm:"type:varchar(64);primaryKey"`
	TenantID            uint64                     `json:"tenant_id" gorm:"not null;index:idx_wiki_block_sources_tenant_kb,priority:1"`
	KnowledgeBaseID     string                     `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index:idx_wiki_block_sources_tenant_kb,priority:2"`
	PageID              string                     `json:"page_id" gorm:"type:varchar(36);not null;index"`
	BlockID             string                     `json:"block_id" gorm:"type:varchar(64);not null;index"`
	KnowledgeID         string                     `json:"knowledge_id" gorm:"type:varchar(36);not null;index"`
	KnowledgeRevisionID string                     `json:"knowledge_revision_id" gorm:"type:varchar(64);not null;index"`
	ChunkID             *string                    `json:"chunk_id,omitempty" gorm:"type:varchar(36);index"`
	SourceStart         int                        `json:"source_start" gorm:"not null;default:-1"`
	SourceEnd           int                        `json:"source_end" gorm:"not null;default:-1"`
	EvidenceHash        string                     `json:"evidence_hash" gorm:"type:varchar(64);not null;default:''"`
	SourceRole          WikiSourceRole             `json:"source_role" gorm:"type:varchar(32);not null;default:supporting"`
	Confidence          float64                    `json:"confidence" gorm:"not null;default:0"`
	ValidationStatus    WikiSourceValidationStatus `json:"validation_status" gorm:"type:varchar(32);not null;default:pending;index"`
	Metadata            JSON                       `json:"metadata" gorm:"type:json;not null;default:'{}'"`
	CreatedAt           time.Time                  `json:"created_at"`
}

func (WikiBlockSource) TableName() string { return "wiki_block_sources" }

func (s *WikiBlockSource) BeforeCreate(_ *gorm.DB) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if s.ChunkID == nil && s.SourceStart == 0 && s.SourceEnd == 0 {
		s.SourceStart = -1
		s.SourceEnd = -1
	}
	if s.SourceRole == "" {
		s.SourceRole = WikiSourceSupporting
	}
	if s.ValidationStatus == "" {
		s.ValidationStatus = WikiSourceValidationPending
	}
	return s.Validate()
}

func (s *WikiBlockSource) Validate() error {
	if s.TenantID == 0 || s.BlockID == "" || s.PageID == "" || s.KnowledgeID == "" ||
		s.KnowledgeRevisionID == "" || s.KnowledgeBaseID == "" {
		return errors.New("wiki block source requires tenant, block, page, knowledge, revision and knowledge_base IDs")
	}
	validOffsets := (s.SourceStart == -1 && s.SourceEnd == -1) ||
		(s.SourceStart >= 0 && s.SourceEnd >= s.SourceStart)
	if !validOffsets {
		return fmt.Errorf("invalid source offsets [%d,%d]", s.SourceStart, s.SourceEnd)
	}
	if s.ChunkID == nil && (s.SourceStart != -1 || s.SourceEnd != -1) {
		return errors.New("source offsets require a chunk_id")
	}
	if s.Confidence < 0 || s.Confidence > 1 {
		return fmt.Errorf("source confidence %.4f must be between 0 and 1", s.Confidence)
	}
	if !s.SourceRole.IsValid() {
		return fmt.Errorf("invalid wiki source role %q", s.SourceRole)
	}
	if !s.ValidationStatus.IsValid() {
		return fmt.Errorf("invalid wiki source validation status %q", s.ValidationStatus)
	}
	return nil
}

// WikiPageSource is the fast page-level impact projection derived from block
// sources. It is not the authoritative evidence ledger.
type WikiPageSource struct {
	TenantID                uint64                       `json:"tenant_id" gorm:"not null;index:idx_wiki_page_sources_tenant_kb,priority:1"`
	KnowledgeBaseID         string                       `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index:idx_wiki_page_sources_tenant_kb,priority:2"`
	PageID                  string                       `json:"page_id" gorm:"type:varchar(36);primaryKey"`
	KnowledgeID             string                       `json:"knowledge_id" gorm:"type:varchar(36);primaryKey;index"`
	SupportedBlockCount     int                          `json:"supported_block_count" gorm:"not null;default:0"`
	LastKnowledgeRevisionID *string                      `json:"last_knowledge_revision_id,omitempty" gorm:"type:varchar(64);index"`
	MappingGranularity      WikiSourceMappingGranularity `json:"mapping_granularity" gorm:"type:varchar(16);not null;default:page"`
	ValidationStatus        WikiSourceValidationStatus   `json:"validation_status" gorm:"type:varchar(32);not null;default:pending"`
	CreatedAt               time.Time                    `json:"created_at"`
	UpdatedAt               time.Time                    `json:"updated_at"`
}

func (WikiPageSource) TableName() string { return "wiki_page_sources" }

func (s *WikiPageSource) BeforeCreate(_ *gorm.DB) error {
	if s.MappingGranularity == "" {
		s.MappingGranularity = WikiSourceMappingPage
	}
	if s.ValidationStatus == "" {
		s.ValidationStatus = WikiSourceValidationPending
	}
	return s.Validate()
}

func (s *WikiPageSource) Validate() error {
	if s.TenantID == 0 || s.PageID == "" || s.KnowledgeID == "" || s.KnowledgeBaseID == "" {
		return errors.New("wiki page source requires tenant_id, page_id, knowledge_id and knowledge_base_id")
	}
	if s.SupportedBlockCount < 0 {
		return errors.New("supported block count cannot be negative")
	}
	if !s.MappingGranularity.IsValid() {
		return fmt.Errorf("invalid source mapping granularity %q", s.MappingGranularity)
	}
	if !s.ValidationStatus.IsValid() {
		return fmt.Errorf("invalid wiki page source validation status %q", s.ValidationStatus)
	}
	return nil
}
