package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type KnowledgeFolderRepository interface {
	WithinTransaction(ctx context.Context, fn func(KnowledgeFolderRepository) error) error
	Create(ctx context.Context, folder *types.KnowledgeFolder) error
	Get(ctx context.Context, tenantID uint64, kbID, folderID string) (*types.KnowledgeFolder, error)
	List(ctx context.Context, tenantID uint64, kbID string) ([]*types.KnowledgeFolder, error)
	Save(ctx context.Context, folder *types.KnowledgeFolder) error
	DeleteEmpty(ctx context.Context, tenantID uint64, kbID, folderID string) error
	MoveKnowledge(ctx context.Context, tenantID uint64, kbID string, knowledgeIDs []string, folderID *string) (int64, error)
	ListKnowledgeIDs(ctx context.Context, tenantID uint64, kbID string, folderIDs []string, includeDescendants bool) ([]string, error)
}

type KnowledgeFolderService interface {
	List(ctx context.Context, tenantID uint64, kbID string) ([]*types.KnowledgeFolder, error)
	Create(ctx context.Context, tenantID uint64, kbID string, parentID *string, name string) (*types.KnowledgeFolder, error)
	Update(
		ctx context.Context,
		tenantID uint64,
		kbID, folderID string,
		name *string,
		parentID *string,
		moveParent bool,
	) (*types.KnowledgeFolder, error)
	Delete(ctx context.Context, tenantID uint64, kbID, folderID string) error
	MoveKnowledge(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		knowledgeIDs []string,
		folderID *string,
	) (int, error)
	ResolveKnowledgeIDs(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		folderIDs []string,
	) ([]string, error)
}
