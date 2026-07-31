package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// KnowledgeFolderService defines operations on document knowledge folders.
type KnowledgeFolderService interface {
	ListFolders(ctx context.Context, kbID string, parentID string) ([]*types.KnowledgeFolderNode, error)
	ListAllFolders(ctx context.Context, kbID string) ([]*types.KnowledgeFolder, error)
	SearchFoldersInScopes(
		ctx context.Context,
		scopes []types.KnowledgeSearchScope,
		keyword string,
		offset, limit int,
	) ([]*types.KnowledgeFolderSearchResult, bool, int64, error)
	GetFolder(ctx context.Context, kbID string, id string) (*types.KnowledgeFolder, error)
	CreateFolder(ctx context.Context, kbID string, tenantID uint64, parentID string, name string) (*types.KnowledgeFolder, error)
	RenameOrMoveFolder(ctx context.Context, kbID string, id string, newName string, newParentID string, moveParent bool) (*types.KnowledgeFolder, error)
	DeleteFolder(ctx context.Context, kbID string, id string, promote bool) error
	FindOrCreateFolderPath(ctx context.Context, kbID string, tenantID uint64, baseFolderID string, path []string) (string, error)
	MoveKnowledgeToFolder(ctx context.Context, kbID string, knowledgeIDs []string, folderID string) (int64, error)
	OrganizeByPath(ctx context.Context, kbID string, tenantID uint64) (organized int64, foldersCreated int64, err error)
	// MUST treat an empty result as "empty scope", never as "no filter".
	ListKnowledgeIDsByFolderIDs(ctx context.Context, tenantID uint64, kbID string, folderIDs []string, recursive bool) ([]string, error)
	ExpandFolderSubtrees(ctx context.Context, kbID string, folderIDs []string) ([]string, error)
	DeleteFoldersByKnowledgeBase(ctx context.Context, kbID string) error
}

// KnowledgeFolderRepository persists document knowledge folders.
type KnowledgeFolderRepository interface {
	Create(ctx context.Context, folder *types.KnowledgeFolder) error
	GetByID(ctx context.Context, kbID string, id string) (*types.KnowledgeFolder, error)
	GetChildByName(ctx context.Context, kbID string, parentID string, name string) (*types.KnowledgeFolder, error)
	ListAll(ctx context.Context, kbID string) ([]*types.KnowledgeFolder, error)
	SearchFoldersInScopes(
		ctx context.Context,
		scopes []types.KnowledgeSearchScope,
		keyword string,
		offset, limit int,
	) ([]*types.KnowledgeFolderSearchResult, bool, int64, error)
	ListChildren(ctx context.Context, kbID string, parentID string) ([]*types.KnowledgeFolderNode, error)
	Update(ctx context.Context, folder *types.KnowledgeFolder) error
	UpdateSubtree(ctx context.Context, folders []*types.KnowledgeFolder) error
	Delete(ctx context.Context, kbID string, id string) error
	DeleteByKnowledgeBase(ctx context.Context, kbID string) error
	CountKnowledgeInFolders(ctx context.Context, kbID string, folderIDs []string) (int64, error)
	ListKnowledgeIDsInFolders(ctx context.Context, tenantID uint64, kbID string, folderIDs []string) ([]string, error)
	BatchUpdateKnowledgeFolder(ctx context.Context, kbID string, knowledgeIDs []string, folderID string) (int64, error)
	MoveKnowledgeBetweenFolders(ctx context.Context, kbID string, fromFolderID string, toFolderID string) (int64, error)
	ListPathedRootKnowledge(ctx context.Context, kbID string, limit int) ([]*types.Knowledge, error)
	ResetKnowledgeFolders(ctx context.Context, kbID string) (int64, error)
}
