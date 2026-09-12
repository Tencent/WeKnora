package interfaces

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
)

// ErrWikiRenameUnsupported indicates that identity-preserving rename is unavailable.
var ErrWikiRenameUnsupported = errors.New("identity-preserving wiki rename is not supported")

// WikiPageRenameRequest binds a rename to the UUID resolved by the caller.
// Reusing the old slug for another page must not redirect an in-flight rename.
type WikiPageRenameRequest struct {
	KnowledgeBaseID string
	PageID          string
	OldSlug         string
	NewSlug         string
}

// WikiPageRenameResult contains the renamed page and every page updated by link repair.
type WikiPageRenameResult struct {
	Page *types.WikiPage
	// AffectedPages includes Page and every live page whose links or parent
	// changed, including archived pages. Historical snapshots are untouched.
	AffectedPages []*types.WikiPage
}

// WikiPageRenamer is an optional repository/service capability. Implementations
// must rename the existing UUID and its references in one transaction, retaining
// version, history and source identity. There is no create/delete fallback.
type WikiPageRenamer interface {
	RenamePage(context.Context, WikiPageRenameRequest) (*WikiPageRenameResult, error)
}
