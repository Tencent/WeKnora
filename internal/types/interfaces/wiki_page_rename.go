package interfaces

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

var ErrWikiRenameUnsupported = errors.New("identity-preserving wiki rename is not supported")

// WikiPageRenameRequest binds a rename to the UUID resolved by the caller.
// Reusing the old slug for another page must not redirect an in-flight rename.
type WikiPageRenameRequest struct {
	KnowledgeBaseID string
	PageID          string
	OldSlug         string
	NewSlug         string
}

type WikiPageRenameResult struct {
	Page *types.WikiPage
	// AffectedPages includes Page and every live page whose links or parent
	// changed, including archived pages. Historical snapshots are untouched.
	AffectedPages []*types.WikiPage
	// SyncWarnings describe post-commit retrieval failures, not rename failures.
	SyncWarnings []string
}

// WikiPageRenamer is an optional repository/service capability. Implementations
// must rename the existing UUID and its references in one transaction, retaining
// version, history and source identity. There is no create/delete fallback.
type WikiPageRenamer interface {
	RenamePage(context.Context, WikiPageRenameRequest) (*WikiPageRenameResult, error)
}

// WikiPageRenameChunkUpdater conditionally refreshes an existing projection.
// Both the page snapshot and the chunk timestamp must still match. Unlike the
// generic chunk Save operation, this must never upsert a deleted chunk or
// overwrite unrelated chunk fields. It remains optional for existing mocks.
type WikiPageRenameChunkUpdater interface {
	UpdateRenamedWikiChunk(context.Context, *types.WikiPage, *types.Chunk, time.Time) error
}
