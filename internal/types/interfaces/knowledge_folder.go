package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// KnowledgeFolderRepository persists knowledge folders within a tenant and knowledge base scope.
//
// Structural writes must run in Transaction and call LockKnowledgeBase before
// re-reading or validating folder state. Task 5 knowledge assignment/move writes
// must use the same KB-level lock so folder deletion cannot orphan documents.
type KnowledgeFolderRepository interface {
	Create(context.Context, *types.KnowledgeFolder) error
	CreateIfAbsent(context.Context, *types.KnowledgeFolder) (*types.KnowledgeFolder, bool, error)
	GetByID(context.Context, uint64, string, string) (*types.KnowledgeFolder, error)
	// LockKnowledgeBase acquires the transaction-scoped KB structural-write lock.
	LockKnowledgeBase(context.Context, uint64, string) error
	GetByIDForUpdate(context.Context, uint64, string, string) (*types.KnowledgeFolder, error)
	ListByParent(context.Context, uint64, string, string) ([]*types.KnowledgeFolder, error)
	ListAll(context.Context, uint64, string) ([]*types.KnowledgeFolder, error)
	ListAllForUpdate(context.Context, uint64, string) ([]*types.KnowledgeFolder, error)
	Update(context.Context, *types.KnowledgeFolder) error
	UpdateName(context.Context, uint64, string, string, string) error
	Delete(context.Context, uint64, string, string) error
	DeleteSubtree(context.Context, uint64, string, []string) error
	CreateKnowledge(context.Context, *types.Knowledge) error
	GetKnowledgeByIDForUpdate(context.Context, uint64, string) (*types.Knowledge, error)
	GetKnowledgeBatchForUpdate(context.Context, uint64, []string) ([]*types.Knowledge, error)
	MoveKnowledgeToFolder(context.Context, uint64, string, []string, string) error
	GetDescendantIDs(context.Context, uint64, string, []string) ([]string, error)
	CountKnowledgeByFolder(context.Context, uint64, string) (map[string]int64, error)
	CheckSiblingName(context.Context, uint64, string, string, string, string) (bool, error)
	// MoveSubtree uses the current transaction; callers provide atomicity and the KB lock.
	MoveSubtree(context.Context, *types.KnowledgeFolder, string, string, int) error
	Transaction(context.Context, func(KnowledgeFolderRepository) error) error
}

// KnowledgeFolderService manages the folder lifecycle within the current tenant.
type KnowledgeFolderService interface {
	CreateFolder(context.Context, string, *types.CreateFolderRequest) (*types.KnowledgeFolder, error)
	ResolveOrCreatePaths(
		context.Context, string, *types.ResolveFolderPathsRequest,
	) (*types.ResolveFolderPathsResponse, error)
	GetFolder(context.Context, string, string) (*types.KnowledgeFolder, error)
	ListByParent(context.Context, string, string) ([]*types.KnowledgeFolder, error)
	GetTree(context.Context, string) ([]*types.KnowledgeFolder, error)
	UpdateFolder(context.Context, string, string, *types.UpdateFolderRequest) (*types.KnowledgeFolder, error)
	MoveFolder(context.Context, string, string, *types.MoveFolderRequest) (*types.KnowledgeFolder, error)
	GetBreadcrumb(context.Context, string, string) ([]*types.KnowledgeFolder, error)
	ResolveKnowledgeScope(context.Context, string, []string) (*types.FolderKnowledgeScope, error)
	CreateKnowledgeInFolder(context.Context, *types.Knowledge, string) error
	MoveKnowledgeToFolder(context.Context, string, string) error
	MoveKnowledgeBatchToFolder(context.Context, []string, string) error
	MoveBatchToFolder(context.Context, string, []string, []string, string) error
	DeleteEmptySubtrees(context.Context, string, []string) error
}
