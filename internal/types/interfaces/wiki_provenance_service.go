package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// WikiProvenanceService is an optional extension of WikiPageService. Keeping
// it separate preserves compatibility with the many lightweight WikiPageService
// fakes while callers that need paragraph-level sources can opt in by type
// assertion.
type WikiProvenanceService interface {
	// GetPageWithSources returns the normal page detail plus the immutable
	// blocks referenced by its current published block set. Legacy/manual pages
	// return an empty Blocks slice.
	GetPageWithSources(ctx context.Context, kbID, slug string) (*types.WikiPageDetailResponse, error)

	// SavePageWithProvenance atomically creates or advances a page to a staged
	// block set. Page.Content and the compatibility source_refs/chunk_refs are
	// derived from that same set before the current pointer is switched.
	SavePageWithProvenance(
		ctx context.Context,
		page *types.WikiPage,
		blockSet *types.WikiPageBlockSet,
	) (*types.WikiPage, error)

	// GetCurrentPageBlockSet loads the immutable source snapshot for a page.
	// The nil,nil result means the page predates block provenance.
	GetCurrentPageBlockSet(
		ctx context.Context,
		kbID, pageID string,
	) (*types.WikiPageBlockSet, error)

	// ListPageSlugsByKnowledgeSource resolves the current structured pages that
	// cite a document. Reparse/delete code unions this canonical lookup with
	// legacy wiki_pages.source_refs so a drifted compatibility cache cannot
	// leave stale sourced paragraphs behind.
	ListPageSlugsByKnowledgeSource(
		ctx context.Context,
		kbID, knowledgeID string,
	) ([]string, error)

	// RemoveKnowledgeFromPage removes one document from a structured page. A
	// block is the atomic authored unit: if that document contributed any
	// evidence to a block, the entire block is removed. This conservative rule
	// combines with claim-level coverage at publication time so unsupported
	// claims cannot survive in multi-source paragraphs. handled=false asks callers to
	// use the legacy page-level retract path. deleted=true means no sourced body
	// content remained and the page itself was deleted.
	RemoveKnowledgeFromPage(
		ctx context.Context,
		kbID, slug, knowledgeID string,
	) (handled bool, deleted bool, err error)
}
