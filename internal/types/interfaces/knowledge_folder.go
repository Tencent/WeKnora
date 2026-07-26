package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type KnowledgeFolderRepository interface {
	Create(ctx context.Context, folder *types.KnowledgeFolder) error
	GetByID(ctx context.Context, tenantID uint64, kbID, folderID string) (*types.KnowledgeFolder, error)
	ListByKnowledgeBase(ctx context.Context, tenantID uint64, kbID string) ([]*types.KnowledgeFolder, error)
	GetChildByName(ctx context.Context, tenantID uint64, kbID string, parentID *string, name string) (*types.KnowledgeFolder, error)
	UpdateTree(ctx context.Context, folders []*types.KnowledgeFolder) error
	DeleteEmpty(ctx context.Context, tenantID uint64, kbID, folderID string) error
	DeleteTree(ctx context.Context, tenantID uint64, kbID, folderID string) error
	DeleteByKnowledgeBase(ctx context.Context, tenantID uint64, kbID string) error
	ListKnowledgeIDsByScope(ctx context.Context, tenantID uint64, kbID, folderID string, includeDescendants bool) ([]string, error)
	MoveKnowledge(ctx context.Context, tenantID uint64, kbID string, knowledgeIDs []string, folderID *string) error
	CountKnowledgeByFolder(ctx context.Context, tenantID uint64, kbID string) (map[string]int64, error)
}

type KnowledgeFolderService interface {
	Create(ctx context.Context, kbID string, req *types.KnowledgeFolderCreateRequest) (*types.KnowledgeFolder, error)
	List(ctx context.Context, kbID string) ([]types.KnowledgeFolderNode, error)
	Update(ctx context.Context, kbID, folderID string, req *types.KnowledgeFolderUpdateRequest) (*types.KnowledgeFolder, error)
	Delete(ctx context.Context, kbID, folderID string) error
	DeleteRecursive(ctx context.Context, kbID, folderID string) error
	DeleteByKnowledgeBase(ctx context.Context, kbID string) error
	MoveKnowledge(ctx context.Context, kbID string, knowledgeIDs []string, folderID *string) error
	ValidatePlacement(ctx context.Context, tenantID uint64, kbID string, folderID *string) error
	ResolveKnowledgeIDs(ctx context.Context, tenantID uint64, scope types.FolderScope) ([]string, error)
}
