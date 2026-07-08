package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// KnowledgeFolderService manages document folders under a knowledge base.
type KnowledgeFolderService interface {
	ListFolders(ctx context.Context, kbID string, parentID string) ([]*types.KnowledgeFolder, error)
	CreateFolder(ctx context.Context, kbID string, parentID string, name string) (*types.KnowledgeFolder, error)
	RenameFolder(ctx context.Context, kbID string, folderID string, name string) (*types.KnowledgeFolder, error)
	DeleteEmptyFolder(ctx context.Context, kbID string, folderID string) error
	MoveKnowledgeToFolder(ctx context.Context, knowledgeID string, folderID string) (*types.Knowledge, error)
}

// KnowledgeFolderRepository persists document folders.
type KnowledgeFolderRepository interface {
	ListByParent(ctx context.Context, tenantID uint64, kbID string, parentID string) ([]*types.KnowledgeFolder, error)
	GetByID(ctx context.Context, tenantID uint64, kbID string, folderID string) (*types.KnowledgeFolder, error)
	GetByParentAndName(ctx context.Context, tenantID uint64, kbID string, parentID string, name string) (*types.KnowledgeFolder, error)
	Create(ctx context.Context, folder *types.KnowledgeFolder) error
	Update(ctx context.Context, folder *types.KnowledgeFolder) error
	UpdateWithDescendantPaths(ctx context.Context, folder *types.KnowledgeFolder, oldPath string) error
	Delete(ctx context.Context, tenantID uint64, kbID string, folderID string) error
	DeleteEmpty(ctx context.Context, tenantID uint64, kbID string, folderID string) error
	CountKnowledgeByParents(ctx context.Context, tenantID uint64, kbID string, folderIDs []string) (map[string]int64, error)
	MarkHasChildren(ctx context.Context, tenantID uint64, kbID string, folders []*types.KnowledgeFolder) error
}
