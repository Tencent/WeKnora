package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// KnowledgeFolderService manages knowledge folder trees.
type KnowledgeFolderService interface {
	CreateFolder(
		ctx context.Context,
		kbID string,
		req *types.KnowledgeFolderCreateRequest,
	) (*types.KnowledgeFolder, error)
	GetFolder(
		ctx context.Context,
		kbID string,
		folderID string,
	) (*types.KnowledgeFolderWithStats, error)
	ListFolders(
		ctx context.Context,
		kbID string,
		parentID string,
		page *types.Pagination,
	) (*types.PageResult, error)
	UpdateFolder(
		ctx context.Context,
		kbID string,
		folderID string,
		req *types.KnowledgeFolderUpdateRequest,
	) (*types.KnowledgeFolder, error)
	DeleteFolder(ctx context.Context, kbID string, folderID string) error
	GetBreadcrumb(
		ctx context.Context,
		kbID string,
		folderID string,
	) ([]*types.KnowledgeFolder, error)
	ListSubtreeFolderIDs(
		ctx context.Context,
		kbID string,
		folderID string,
	) ([]string, error)
}

// KnowledgeFolderReader provides tenant- and knowledge-base-scoped folder reads.
type KnowledgeFolderReader interface {
	GetByID(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		folderID string,
	) (*types.KnowledgeFolder, error)
	GetByParentAndName(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		parentID string,
		name string,
	) (*types.KnowledgeFolder, error)
	ListByParent(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		parentID string,
		page *types.Pagination,
	) ([]*types.KnowledgeFolder, int64, error)
	CountKnowledgeByFolderIDs(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		folderIDs []string,
	) (map[string]int64, error)
	FindParentIDsWithChildren(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		parentIDs []string,
	) (map[string]bool, error)
	ListByIDs(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		folderIDs []string,
	) ([]*types.KnowledgeFolder, error)
	ListSubtreeFolders(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		rootID string,
		pathPrefix string,
	) ([]*types.KnowledgeFolder, error)
}

// KnowledgeFolderMoveSubtreeParams contains the expected and target tree state.
type KnowledgeFolderMoveSubtreeParams struct {
	FolderID            string
	ExpectedParentID    string
	ExpectedPath        string
	ExpectedDepth       int
	ExpectedFolderCount int64
	NewParentID         string
	NewPath             string
	NewName             string
	NewSortOrder        int
	DepthDelta          int
}

// KnowledgeFolderTreeRepository exposes writes only inside a tree transaction.
type KnowledgeFolderTreeRepository interface {
	KnowledgeFolderReader
	Create(ctx context.Context, folder *types.KnowledgeFolder) error
	UpdateFolderAttributes(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		folderID string,
		name *string,
		sortOrder *int,
	) error
	MoveSubtree(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		params KnowledgeFolderMoveSubtreeParams,
	) error
	DeleteEmpty(ctx context.Context, tenantID uint64, kbID string, folderID string) error
}

// KnowledgeFolderTreeWriteFunc runs inside a replay-safe folder tree transaction.
// SQLite may replay it; callbacks must not perform network or non-rollbackable side effects.
type KnowledgeFolderTreeWriteFunc func(repo KnowledgeFolderTreeRepository) error

// KnowledgeFolderRepository provides scoped reads and serialized tree writes.
type KnowledgeFolderRepository interface {
	KnowledgeFolderReader
	RunTreeWriteTransaction(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		fn KnowledgeFolderTreeWriteFunc,
	) error
}
