package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// FolderService defines business operations for knowledge-base folders.
type FolderService interface {
	CreateFolder(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		parentID *string,
		name string,
	) (*types.Folder, error)
	GetFolder(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		folderID string,
	) (*types.Folder, error)
	ListChildren(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		parentID *string,
	) ([]*types.Folder, error)
	ListByKnowledgeBase(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
	) ([]*types.Folder, error)
	RenameFolder(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		folderID string,
		name string,
	) (*types.Folder, error)
	MoveFolder(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		folderID string,
		newParentID *string,
	) (*types.Folder, error)
	DeleteFolder(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		folderID string,
	) error
}

// FolderRepository defines scoped persistence operations for knowledge-base folders.
type FolderRepository interface {
	// WithinTransaction runs fn with a repository bound to one database transaction.
	WithinTransaction(
		ctx context.Context,
		fn func(FolderRepository) error,
	) error
	Create(ctx context.Context, folder *types.Folder) error
	GetByID(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		folderID string,
	) (*types.Folder, error)
	ListByIDs(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		folderIDs []string,
	) ([]*types.Folder, error)
	// GetByIDForUpdate applies a row lock on PostgreSQL and a scoped transactional
	// read on databases without compatible row-level locking.
	GetByIDForUpdate(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		folderID string,
	) (*types.Folder, error)
	ListChildren(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		parentID *string,
	) ([]*types.Folder, error)
	ListByKnowledgeBase(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
	) ([]*types.Folder, error)
	UpdateName(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		folderID string,
		name string,
	) error
	UpdateParent(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		folderID string,
		parentID *string,
	) error
	Delete(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		folderID string,
	) error
	CountChildren(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		folderID string,
	) (int64, error)
	CountKnowledge(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		folderID string,
	) (int64, error)
}
