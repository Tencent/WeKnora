package types

import "errors"

var (
	// ErrWikiPublishIdempotencyConflict is returned when one key is reused
	// with a different page or provenance payload.
	ErrWikiPublishIdempotencyConflict = errors.New("wiki provenance publish idempotency conflict")
	// ErrWikiPublishScopeNotFound covers missing rows and ownership mismatches
	// without revealing whether another tenant owns the requested object.
	ErrWikiPublishScopeNotFound = errors.New("wiki provenance publish scope not found")
)

// WikiProvenancePublishRequest is one atomic page/source publication.
type WikiProvenancePublishRequest struct {
	TenantID           uint64                     `json:"tenant_id"`
	KnowledgeBaseID    string                     `json:"knowledge_base_id"`
	PageID             string                     `json:"page_id"`
	IdempotencyKey     string                     `json:"idempotency_key"`
	PageProjection     WikiPage                   `json:"page_projection"`
	KnowledgeRevisions []KnowledgeRevision        `json:"knowledge_revisions,omitempty"`
	PageRevision       WikiProvenancePageRevision `json:"page_revision"`
	Blocks             []WikiPageBlock            `json:"blocks"`
	Sources            []WikiBlockSource          `json:"sources,omitempty"`
}

// WikiProvenancePublishResult describes the published immutable page revision.
type WikiProvenancePublishResult struct {
	PageRevision       *WikiProvenancePageRevision `json:"page_revision"`
	KnowledgeRevisions map[string]string           `json:"knowledge_revisions,omitempty"`
	AlreadyPublished   bool                        `json:"already_published"`
}
