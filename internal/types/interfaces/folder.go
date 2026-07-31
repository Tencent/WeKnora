package interfaces

import (
	"context"
	"github.com/Tencent/WeKnora/internal/types"
)

type KnowledgeFolderRepository interface {
	Create(ctx context.Context, folder *types.KnowledgeFolder) error
	Update(ctx context.Context, folder *types.KnowledgeFolder) error
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.KnowledgeFolder, error)
	GetByName(ctx context.Context, tenantID uint64, kbID string, parentID string, name string) (*types.KnowledgeFolder, error)
	ListByParent(ctx context.Context, tenantID uint64, kbID string, parentID string) ([]*types.KnowledgeFolder, error)
	ListByKB(ctx context.Context, tenantID uint64, kbID string) ([]*types.KnowledgeFolder, error)
	Delete(ctx context.Context, tenantID uint64, id string) error
	GetDescendantIDs(ctx context.Context, tenantID uint64, kbID string, folderID string) ([]string, error)
	CountKnowledgeInFolder(ctx context.Context, tenantID uint64, kbID string, folderID string) (int64, error)
	CountChildFolders(ctx context.Context, tenantID uint64, kbID string, parentID string) (int64, error)
}

type KnowledgeFolderService interface {
	CreateFolder(ctx context.Context, kbID string, parentID string, name string) (*types.KnowledgeFolder, error)
	UpdateFolder(ctx context.Context, folderID string, name string) (*types.KnowledgeFolder, error)
	DeleteFolder(ctx context.Context, folderID string) error
	ListFolders(ctx context.Context, kbID string) ([]*types.KnowledgeFolderWithStats, error)
	ListChildFolders(ctx context.Context, kbID string, parentID string) ([]*types.KnowledgeFolderWithStats, error)
	GetFolderTree(ctx context.Context, kbID string) ([]*types.FolderTreeNode, error)
	MoveKnowledgeToFolder(ctx context.Context, knowledgeID string, folderID string) error
	GetFolderDescendantIDs(ctx context.Context, kbID string, folderID string) ([]string, error)
}
