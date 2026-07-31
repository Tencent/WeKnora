package interfaces

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// KnowledgeFolderScopeReadSnapshotFunc reads one folder scope snapshot.
// SQLite may replay it; callers must reset attempt-local output on entry.
type KnowledgeFolderScopeReadSnapshotFunc func(reader KnowledgeFolderScopeReader) error

// KnowledgeFolderScopeRepository provides consistent, scoped folder reads.
type KnowledgeFolderScopeRepository interface {
	RunKnowledgeFolderScopeReadSnapshot(
		ctx context.Context,
		sourceTenantID uint64,
		knowledgeBaseID string,
		fn KnowledgeFolderScopeReadSnapshotFunc,
	) error
}

// KnowledgeScopeAuthorizationRepository provides active metadata used to bind
// request selectors to knowledge bases before authorization.
type KnowledgeScopeAuthorizationRepository interface {
	ListKnowledgeScopeReferencesByIDs(
		ctx context.Context,
		knowledgeIDs []string,
	) ([]*types.Knowledge, error)
	ListKnowledgeTagScopeReferencesByIDs(
		ctx context.Context,
		tagIDs []string,
	) ([]*types.KnowledgeTag, error)
	ListKnowledgeFolderScopeReferencesByIDs(
		ctx context.Context,
		folderIDs []string,
	) ([]*types.KnowledgeFolder, error)
}

// KnowledgeFolderScopeRoot is one validated recursive subtree root.
type KnowledgeFolderScopeRoot struct {
	ID   string `json:"-"`
	Path string `json:"-"`
}

// String returns a redacted runtime summary.
func (r KnowledgeFolderScopeRoot) String() string {
	return fmt.Sprintf(
		"KnowledgeFolderScopeRoot{id_set=%t, path_set=%t}",
		r.ID != "",
		r.Path != "",
	)
}

// GoString returns a redacted runtime summary.
func (r KnowledgeFolderScopeRoot) GoString() string {
	return r.String()
}

// KnowledgeFolderScopeReader exposes only the reads needed to resolve a scope.
type KnowledgeFolderScopeReader interface {
	ListScopeFoldersByIDs(
		folderIDs []string,
	) ([]*types.KnowledgeFolder, error)
	ListScopeSubtreeCandidates(
		roots []KnowledgeFolderScopeRoot,
		limit int,
	) ([]*types.KnowledgeFolder, error)
}

// KnowledgeScopeResolver converts authorized request scope into execution scope.
type KnowledgeScopeResolver interface {
	ValidateFolderSelectorBudget(
		request *types.KnowledgeScopeRequest,
	) error
	Resolve(
		ctx context.Context,
		input types.KnowledgeScopeResolveInput,
	) (*types.KnowledgeScope, error)
}
