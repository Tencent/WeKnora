package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type KnowledgeFolderRepository interface {
	List(ctx context.Context, tenantID uint64, kbID, parentID, keyword string, page *types.Pagination) ([]*types.KnowledgeFolderView, int64, error)
	Get(ctx context.Context, tenantID uint64, kbID, folderID string) (*types.KnowledgeFolderView, error)
	Create(ctx context.Context, tenantID uint64, kbID, parentID, name string) (*types.KnowledgeFolder, error)
	Update(ctx context.Context, tenantID uint64, kbID, folderID string, name, parentID *string) (*types.KnowledgeFolder, error)
	Delete(ctx context.Context, tenantID uint64, kbID, folderID string) error
	EnsurePaths(ctx context.Context, tenantID uint64, kbID, parentID string, paths []types.EnsureFolderPath) ([]types.EnsureFolderPathResult, error)
	MoveKnowledge(ctx context.Context, tenantID uint64, kbID string, knowledgeIDs []string, folderID string) error
	ListKnowledgeIDsRecursive(ctx context.Context, tenantID uint64, kbID string, folderIDs []string) ([]string, error)
}

type KnowledgeFolderService interface {
	List(ctx context.Context, kbID, parentID, keyword string, page *types.Pagination) (*types.PageResult, error)
	Get(ctx context.Context, kbID, folderID string) (*types.KnowledgeFolderView, error)
	Create(ctx context.Context, kbID, parentID, name string) (*types.KnowledgeFolder, error)
	Update(ctx context.Context, kbID, folderID string, name, parentID *string) (*types.KnowledgeFolder, error)
	Delete(ctx context.Context, kbID, folderID string) error
	EnsurePaths(ctx context.Context, kbID, parentID string, paths []types.EnsureFolderPath) ([]types.EnsureFolderPathResult, error)
	MoveKnowledge(ctx context.Context, kbID string, knowledgeIDs []string, folderID string) error
}
