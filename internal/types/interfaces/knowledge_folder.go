package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// KnowledgeFolderService defines the folder tree of a document knowledge base.
//
// Folders are purely organisational: they change which documents a listing or a
// retrieval scope selects, never how a document is parsed, chunked or indexed.
// That separation is what lets the feature ship without touching the ingestion
// pipeline or any vector store schema.
type KnowledgeFolderService interface {
	// CreateFolder creates an empty folder under parentID ("" = root).
	CreateFolder(
		ctx context.Context, tenantID uint64, kbID string, parentID string, name string,
	) (*types.KnowledgeFolder, error)

	// GetFolder loads a single folder.
	GetFolder(
		ctx context.Context, tenantID uint64, kbID string, id string,
	) (*types.KnowledgeFolder, error)

	// ListFolders returns the direct children of parentID, or — when recursive
	// is true — the whole tree of the knowledge base in one response. Nodes carry
	// direct and subtree document counts plus a child flag so a tree renders
	// without a follow-up request per node.
	ListFolders(
		ctx context.Context, tenantID uint64, kbID string, parentID string, recursive bool,
	) (*types.KnowledgeFolderListResponse, error)

	// RenameOrMoveFolder renames a folder and/or reparents it. The parent is
	// changed only when moveParent is true, so a rename cannot move a folder to
	// the root by accident. Moves are rejected when the destination lies inside
	// the folder's own subtree.
	RenameOrMoveFolder(
		ctx context.Context, tenantID uint64, kbID string, id string,
		newName string, newParentID string, moveParent bool,
	) (*types.KnowledgeFolder, error)

	// DeleteFolder removes a folder. The default strategy rejects a folder that
	// still holds documents or child folders; types.KnowledgeFolderDeleteReparent
	// deletes the subtree and lifts its documents to the parent folder. Documents
	// are never deleted by a folder operation.
	DeleteFolder(
		ctx context.Context, tenantID uint64, kbID string, id string, strategy string,
	) error

	// ResolveFolderIDs expands folder ids into the set a listing or scope should
	// match, optionally including descendants. The root sentinel "" is kept as a
	// literal bucket for documents that were never filed.
	ResolveFolderIDs(
		ctx context.Context, tenantID uint64, kbID string, folderIDs []string, recursive bool,
	) ([]string, error)

	// ListKnowledgeIDsByFolders resolves a folder scope into the document ids it
	// contains — the form the retrieval pipeline already filters on.
	ListKnowledgeIDsByFolders(
		ctx context.Context, tenantID uint64, kbID string, folderIDs []string, recursive bool,
	) ([]string, error)

	// MoveKnowledgeToFolder files documents into targetFolderID ("" = root).
	// Returns how many rows were relocated.
	MoveKnowledgeToFolder(
		ctx context.Context, tenantID uint64, kbID string, knowledgeIDs []string, targetFolderID string,
	) (int64, error)

	// FindOrCreateFolderPath walks a chain of folder names, creating what is
	// missing, and returns the id of the deepest folder. Import flows use it to
	// mirror a source directory layout.
	FindOrCreateFolderPath(
		ctx context.Context, tenantID uint64, kbID string, names []string,
	) (string, error)
}

// KnowledgeFolderRepository defines persistence for the knowledge folder tree.
type KnowledgeFolderRepository interface {
	CreateFolder(ctx context.Context, folder *types.KnowledgeFolder) error

	GetFolderByID(
		ctx context.Context, tenantID uint64, kbID string, id string,
	) (*types.KnowledgeFolder, error)

	// GetChildFolderByName resolves a folder by name within one parent, backing
	// sibling-name uniqueness and find-or-create path walks.
	GetChildFolderByName(
		ctx context.Context, tenantID uint64, kbID string, parentID string, name string,
	) (*types.KnowledgeFolder, error)

	ListChildFolders(
		ctx context.Context, tenantID uint64, kbID string, parentID string,
	) ([]*types.KnowledgeFolder, error)

	// ListAllFolders returns every folder of a knowledge base, ordered parent
	// first so callers can assemble a tree in one pass.
	ListAllFolders(
		ctx context.Context, tenantID uint64, kbID string,
	) ([]*types.KnowledgeFolder, error)

	// ListSubtreeFolders returns the folder at pathPrefix and all descendants,
	// matched on the materialized path so depth costs nothing.
	ListSubtreeFolders(
		ctx context.Context, tenantID uint64, kbID string, pathPrefix string,
	) ([]*types.KnowledgeFolder, error)

	UpdateFolder(ctx context.Context, folder *types.KnowledgeFolder) error

	// UpdateFoldersTx applies a batch of folder rows in one transaction, so a
	// subtree move cannot be left half-rewritten.
	UpdateFoldersTx(ctx context.Context, folders []*types.KnowledgeFolder) error

	// DeleteFolder soft-deletes a folder only if it holds no live documents and
	// no live child folders, evaluated atomically with the delete.
	DeleteFolder(ctx context.Context, tenantID uint64, kbID string, id string) error

	// DeleteFolderTree deletes the given folders and relocates their documents
	// to reparentTo in a single transaction.
	DeleteFolderTree(
		ctx context.Context, tenantID uint64, kbID string, folderIDs []string, reparentTo string,
	) error

	// CountDocumentsByFolder returns direct document counts keyed by folder id,
	// with the knowledge base root under the empty string.
	CountDocumentsByFolder(
		ctx context.Context, tenantID uint64, kbID string,
	) (map[string]int64, error)

	ListKnowledgeIDsByFolderIDs(
		ctx context.Context, tenantID uint64, kbID string, folderIDs []string,
	) ([]string, error)

	MoveKnowledgeToFolder(
		ctx context.Context, tenantID uint64, kbID string, knowledgeIDs []string, targetFolderID string,
	) (int64, error)

	// ClearFolderAssignments resets every document of a knowledge base back to
	// the root, used when a folder tree is torn down.
	ClearFolderAssignments(ctx context.Context, tenantID uint64, kbID string) error
}
