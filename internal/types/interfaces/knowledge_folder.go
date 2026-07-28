package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// KnowledgeFolderService defines operations on the multi-level folder tree of
// a document knowledge base (issue #1311). Mirrors the wiki folder service.
type KnowledgeFolderService interface {
	// ListFolders lists the direct children of parentID ("" = KB root) as
	// enriched nodes (document count + has_children) for lazy tree loading.
	ListFolders(ctx context.Context, kbID string, parentID string) ([]*types.KnowledgeFolderNode, error)
	// ListAllFolders returns every live folder of the KB as a flat list,
	// ordered by path, for pickers that need the whole tree at once.
	ListAllFolders(ctx context.Context, kbID string) ([]*types.KnowledgeFolder, error)
	// GetFolder returns one folder by id.
	GetFolder(ctx context.Context, kbID string, id string) (*types.KnowledgeFolder, error)
	// CreateFolder creates a new (initially empty) folder under parentID and
	// returns it. Rejects duplicate sibling names, invalid names ("/" or
	// empty), and trees deeper than types.KnowledgeFolderMaxDepth.
	CreateFolder(ctx context.Context, kbID string, tenantID uint64, parentID string, name string) (*types.KnowledgeFolder, error)
	// RenameOrMoveFolder renames and/or reparents a folder (moveParent
	// distinguishes "" meaning root from "" meaning unchanged), recomputing
	// path/depth for the whole subtree. Rejects cycles (moving a folder into
	// itself or a descendant).
	RenameOrMoveFolder(ctx context.Context, kbID string, id string, newName string, newParentID string, moveParent bool) (*types.KnowledgeFolder, error)
	// DeleteFolder removes a folder. By default it fails if the folder still
	// contains documents or child folders; with promote=true the direct
	// contents are moved to the parent first. Documents are never deleted.
	DeleteFolder(ctx context.Context, kbID string, id string, promote bool) error
	// FindOrCreateFolderPath resolves a "/"-separated name chain (e.g.
	// ["reports","2026"]) under baseFolderID ("" = root) to a leaf folder id,
	// creating missing segments. Used by uploads that carry a relative path
	// and by OrganizeByPath.
	FindOrCreateFolderPath(ctx context.Context, kbID string, tenantID uint64, baseFolderID string, path []string) (string, error)
	// MoveKnowledgeToFolder places the given documents of the KB into folderID
	// ("" = root) and returns the number of rows updated.
	MoveKnowledgeToFolder(ctx context.Context, kbID string, knowledgeIDs []string, folderID string) (int64, error)
	// OrganizeByPath files root-level documents whose file_name carries a
	// relative path (e.g. "reports/2026/q1.pdf") into folders derived from
	// that path. Idempotent: already-filed documents no longer match.
	OrganizeByPath(ctx context.Context, kbID string, tenantID uint64) (organized int64, foldersCreated int64, err error)
	// ListKnowledgeIDsByFolderIDs resolves folders (expanding each subtree
	// when recursive=true) to the IDs of documents placed in them. Callers
	// MUST treat an empty result as "empty scope", never as "no filter".
	ListKnowledgeIDsByFolderIDs(ctx context.Context, tenantID uint64, kbID string, folderIDs []string, recursive bool) ([]string, error)
	// ExpandFolderSubtrees returns the given folder IDs plus all their live
	// descendants, for recursive list filtering.
	ExpandFolderSubtrees(ctx context.Context, kbID string, folderIDs []string) ([]string, error)
	// DeleteFoldersByKnowledgeBase removes every folder of a KB (cascade for
	// KB deletion / clear-contents).
	DeleteFoldersByKnowledgeBase(ctx context.Context, kbID string) error
}

// KnowledgeFolderRepository defines persistence operations for knowledge
// folders. The repository is tree-agnostic: subtree expansion happens in the
// service, so methods only ever see explicit folder ID lists.
type KnowledgeFolderRepository interface {
	Create(ctx context.Context, folder *types.KnowledgeFolder) error
	GetByID(ctx context.Context, kbID string, id string) (*types.KnowledgeFolder, error)
	// GetChildByName returns the live child of parentID with the given name.
	GetChildByName(ctx context.Context, kbID string, parentID string, name string) (*types.KnowledgeFolder, error)
	// ListAll returns every live folder of the KB ordered by path.
	ListAll(ctx context.Context, kbID string) ([]*types.KnowledgeFolder, error)
	// ListChildren returns the direct children of parentID enriched with the
	// live document count and a has-children flag, ordered by sort_order, name.
	ListChildren(ctx context.Context, kbID string, parentID string) ([]*types.KnowledgeFolderNode, error)
	Update(ctx context.Context, folder *types.KnowledgeFolder) error
	// Delete soft-deletes a folder by id.
	Delete(ctx context.Context, kbID string, id string) error
	// DeleteByKnowledgeBase soft-deletes all folders of a KB.
	DeleteByKnowledgeBase(ctx context.Context, kbID string) error
	// CountKnowledgeInFolders counts live documents placed directly in any of
	// the given folders ("" counts root-level documents).
	CountKnowledgeInFolders(ctx context.Context, kbID string, folderIDs []string) (int64, error)
	// ListKnowledgeIDsInFolders returns IDs of live documents placed directly
	// in any of the given folders.
	ListKnowledgeIDsInFolders(ctx context.Context, tenantID uint64, kbID string, folderIDs []string) ([]string, error)
	// BatchUpdateKnowledgeFolder sets folder_id on the given documents of the
	// KB and returns the number of rows updated.
	BatchUpdateKnowledgeFolder(ctx context.Context, kbID string, knowledgeIDs []string, folderID string) (int64, error)
	// MoveKnowledgeBetweenFolders reparents all documents from one folder to
	// another (used by promote-delete) and returns rows updated.
	MoveKnowledgeBetweenFolders(ctx context.Context, kbID string, fromFolderID string, toFolderID string) (int64, error)
	// ListPathedRootKnowledge returns up to limit root-level documents whose
	// file_name contains a "/" (organize-by-path work queue). Only ID and
	// FileName are populated.
	ListPathedRootKnowledge(ctx context.Context, kbID string, limit int) ([]*types.Knowledge, error)
}
