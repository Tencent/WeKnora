package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// KnowledgeFolderRepository defines the data access interface for knowledge folders.
type KnowledgeFolderRepository interface {
	// Create creates a new folder.
	Create(ctx context.Context, folder *types.KnowledgeFolder) error
	// GetByID retrieves a folder by its ID, scoped to tenant.
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.KnowledgeFolder, error)
	// ListByParent lists all folders directly under the given parent within a knowledge base.
	// When parentID is nil, returns root-level folders.
	ListByParent(ctx context.Context, tenantID uint64, kbID string, parentID *string) ([]*types.KnowledgeFolder, error)
	// GetAllInKB returns all non-deleted folders in a knowledge base (for building trees).
	GetAllInKB(ctx context.Context, tenantID uint64, kbID string) ([]*types.KnowledgeFolder, error)
	// Update updates folder properties (name, color, description, sort_order).
	Update(ctx context.Context, folder *types.KnowledgeFolder) error
	// Delete soft-deletes a folder by ID, scoped to tenant.
	Delete(ctx context.Context, tenantID uint64, id string) error
	// Move updates the parent_folder_id, path, and depth of a folder (used internally in transactions).
	Move(ctx context.Context, id string, newParentID *string, newPath string, newDepth int) error
	// GetByPath retrieves a folder by its exact path within a knowledge base.
	GetByPath(ctx context.Context, tenantID uint64, kbID string, path string) (*types.KnowledgeFolder, error)
	// GetDescendants returns all descendant folders of the given folder (any depth).
	GetDescendants(ctx context.Context, folderID string) ([]*types.KnowledgeFolder, error)
	// CountKnowledge counts knowledge entries directly in a folder.
	CountKnowledge(ctx context.Context, tenantID uint64, folderID string) (int64, error)
	// CountKnowledgeRecursive counts knowledge entries in a folder and all its descendants.
	CountKnowledgeRecursive(ctx context.Context, tenantID uint64, folderID string) (int64, error)
	// CheckNameExists checks if a folder with the given name already exists under a parent in the same KB.
	CheckNameExists(ctx context.Context, tenantID uint64, kbID string, parentID *string, name string, excludeID string) (bool, error)
	// BatchUpdateDescendantPaths updates path and depth for all descendants of a folder,
	// replacing oldPath prefix with newPath prefix and adjusting depth by the delta.
	// Must be called within a transaction.
	BatchUpdateDescendantPaths(ctx context.Context, oldPath string, newPath string, depthDelta int) error
}

// KnowledgeFolderService defines the business logic interface for knowledge folders.
type KnowledgeFolderService interface {
	// CreateFolder creates a new folder under the specified parent.
	CreateFolder(ctx context.Context, kbID string, req *types.CreateFolderRequest) (*types.KnowledgeFolder, error)
	// GetFolder retrieves a folder by its ID.
	GetFolder(ctx context.Context, id string) (*types.KnowledgeFolder, error)
	// ListByParent lists all folders directly under the given parent.
	ListByParent(ctx context.Context, kbID string, parentID *string) ([]*types.KnowledgeFolder, error)
	// GetTree returns the full folder tree for a knowledge base.
	GetTree(ctx context.Context, kbID string) ([]*types.KnowledgeFolder, error)
	// UpdateFolder updates folder properties.
	UpdateFolder(ctx context.Context, id string, req *types.UpdateFolderRequest) (*types.KnowledgeFolder, error)
	// DeleteFolder deletes a folder. When force is true, cascade-deletes all contents.
	DeleteFolder(ctx context.Context, id string, force bool) error
	// MoveFolder moves a folder to a new parent.
	MoveFolder(ctx context.Context, id string, req *types.MoveFolderRequest) (*types.KnowledgeFolder, error)
	// GetBreadcrumb returns the path of folders from root to the given folder.
	GetBreadcrumb(ctx context.Context, folderID string) ([]*types.KnowledgeFolder, error)
}
