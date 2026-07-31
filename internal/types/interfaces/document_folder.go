package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// DocumentFolderLifecycleRepository is the narrow cleanup capability needed
// by knowledge-base deletion. Keeping it separate prevents lifecycle workers
// from depending on the folder tree's full CRUD and search surface.
type DocumentFolderLifecycleRepository interface {
	DeleteFoldersByKnowledgeBase(ctx context.Context, kbID string) error
}

// DocumentFolderRepository is the persistence boundary for the document-folder
// tree (L1) and the document→folder membership column (L2). It is the single
// place that knows the table layout; services consume it via this interface.
type DocumentFolderRepository interface {
	DocumentFolderLifecycleRepository

	// CreateFolder inserts a new folder row.
	CreateFolder(ctx context.Context, folder *types.DocumentFolder) error

	// GetFolderByID returns a non-deleted folder by id, scoped to kbID.
	// Returns ErrDocumentFolderNotFound when the folder is missing or belongs
	// to a different KB (IDOR fail-closed).
	GetFolderByID(ctx context.Context, kbID string, id string) (*types.DocumentFolder, error)

	// GetChildFolderByName is the sibling-uniqueness probe used on create /
	// rename. Returns ErrDocumentFolderNotFound when no sibling with that name
	// exists.
	GetChildFolderByName(ctx context.Context, kbID string, parentID string, name string) (*types.DocumentFolder, error)

	// ListChildFolders returns one page of direct children ordered by name, id.
	// parent_id == "" returns root-level folders.
	ListChildFolders(
		ctx context.Context,
		kbID string,
		parentID string,
		keyword string,
		after *types.DocumentFolderPageCursor,
		limit int,
	) ([]*types.DocumentFolder, bool, error)

	// ListAllFolders returns every non-deleted folder in the KB, ordered by
	// depth then path so callers can walk subtrees top-down.
	ListAllFolders(ctx context.Context, kbID string) ([]*types.DocumentFolder, error)

	// UpdateFolder writes a folder's mutable fields. The service currently uses
	// it to persist rename-driven name/path updates for an entire subtree.
	// Returns ErrDocumentFolderNotFound when RowsAffected == 0.
	UpdateFolder(ctx context.Context, folder *types.DocumentFolder) error

	// UpdateFoldersInTransaction runs fn inside a single DB transaction against
	// a tx-scoped copy of the repository after locking the stable knowledge-base
	// row. Every folder mutation for the same KB therefore follows one lock
	// protocol and cannot interleave its validation reads with another mutation.
	UpdateFoldersInTransaction(
		ctx context.Context,
		kbID string,
		fn func(txFolderRepo DocumentFolderRepository) error,
	) error

	// DeleteFolder soft-deletes a folder. Returns ErrDocumentFolderNotFound
	// when the id is unknown.
	DeleteFolder(ctx context.Context, kbID string, id string) error

	// HasChildFolders reports whether at least one live child folder exists
	// under parentID — the "non-empty" guard for delete.
	HasChildFolders(ctx context.Context, kbID string, parentID string) (bool, error)

	// HasChildFoldersBatch returns child-presence for every requested parent in
	// a single grouped query.
	HasChildFoldersBatch(ctx context.Context, kbID string, parentIDs []string) (map[string]bool, error)

	// CountDocumentsInFolders returns live document counts grouped by folderID
	// for the given folder set — used to enrich a tree listing.
	CountDocumentsInFolders(ctx context.Context, tenantID uint64, kbID string, folderIDs []string) (map[string]int64, error)

	// CountAllFolders returns the count of live folders in a KB. The service
	// uses this to enforce MaxFoldersPerKB.
	CountAllFolders(ctx context.Context, kbID string) (int64, error)

	// HasDocumentsInSubtree reports whether any live document is filed under
	// any folder in subtreeIDs (or the root bucket when "" is present). Used by
	// the delete-non-empty guard to refuse deleting a folder whose subtree
	// still holds documents.
	HasDocumentsInSubtree(ctx context.Context, tenantID uint64, kbID string, subtreeIDs []string) (bool, error)

	// ListKnowledgeInFolders returns live document placements for the requested
	// folder IDs. Callers use the IDs and parse statuses for delete impact and
	// concurrency-safe folder-tree deletion.
	ListKnowledgeInFolders(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		folderIDs []string,
	) ([]*types.Knowledge, error)

	// SetKnowledgeFolderID updates folder placement for the requested live
	// documents, scoped to one tenant and knowledge base.
	SetKnowledgeFolderID(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		knowledgeIDs []string,
		folderID string,
	) (int64, error)

	// SearchFoldersInScopes searches folder names across an already-authorized
	// list of tenant/KB scopes. Paths are returned as result context, not used
	// for matching; otherwise every descendant of a matching parent pollutes
	// the search result.
	SearchFoldersInScopes(
		ctx context.Context,
		scopes []types.KnowledgeSearchScope,
		keyword string,
		offset int,
		limit int,
	) ([]*types.DocumentFolderSearchResult, bool, int64, error)
}

// DocumentFolderService is the application boundary for document-folder CRUD
// and upload placement validation. It owns the validation rules
// (MaxFolderDepth, MaxFoldersPerKB, name validation) and cycle/unique-name
// guards. The HTTP layer depends on this interface, not on the repository.
type DocumentFolderService interface {
	// ListFolders returns the direct children of parentID enriched with
	// document counts and has-children flags.
	ListFolders(
		ctx context.Context,
		kbID string,
		tenantID uint64,
		parentID string,
		keyword string,
		cursor string,
		pageSize int,
	) (*types.DocumentFolderListResponse, error)

	// CreateFolder creates a new (initially empty) folder under parentID
	// ("" = root). Validates name, enforces MaxFoldersPerKB and MaxFolderDepth.
	CreateFolder(ctx context.Context, kbID string, tenantID uint64, parentID string, name string) (*types.DocumentFolder, error)

	// RenameFolder renames a folder and updates the materialized path of every
	// descendant. It does not change parent_id or depth.
	RenameFolder(ctx context.Context, kbID string, id string, newName string) (*types.DocumentFolder, error)

	// DeleteFolder soft-deletes an empty folder. Refuses to delete a folder
	// that still has child folders or documents filed under it (directly or in
	// its subtree).
	DeleteFolder(ctx context.Context, kbID string, id string) error

	// GetDeleteImpact returns the number of folders and documents in a subtree,
	// including documents whose parse tasks are still active.
	GetDeleteImpact(
		ctx context.Context,
		kbID string,
		tenantID uint64,
		id string,
	) (*types.DocumentFolderDeleteImpact, error)

	// DeleteFolderTree executes one explicit non-empty-folder deletion mode.
	// Background task handlers call this method after restoring tenant context.
	DeleteFolderTree(
		ctx context.Context,
		kbID string,
		tenantID uint64,
		id string,
		mode types.DocumentFolderDeleteMode,
	) error

	// SubmitDeleteFolderTree validates and enqueues one explicit destructive
	// folder deletion mode, returning the background task ID.
	SubmitDeleteFolderTree(
		ctx context.Context,
		kbID string,
		tenantID uint64,
		id string,
		mode types.DocumentFolderDeleteMode,
	) (string, error)

	// ProcessDeleteFolderTree handles the queued deletion in both Redis and
	// Lite-mode task executors.
	ProcessDeleteFolderTree(ctx context.Context, task *asynq.Task) error

	// ResolveSubtreeFolderIDs returns the BFS expansion of the folder subtree
	// rooted at folderID, including folderID itself. Errors if the root is
	// missing, if a cycle is detected, or if the subtree exceeds
	// MaxFoldersPerKB.
	ResolveSubtreeFolderIDs(ctx context.Context, kbID string, folderID string) ([]string, error)

	// ValidateFolderExistsForUpload is the lightweight membership guard used by
	// the upload / create-document paths. Returns nil if folderID is "" (root)
	// or exists and is not deleted; otherwise a sentinel error.
	ValidateFolderExistsForUpload(ctx context.Context, kbID string, folderID string) error

	// SearchFolders searches across authorized scopes for the unified mention
	// search surface.
	SearchFolders(
		ctx context.Context,
		scopes []types.KnowledgeSearchScope,
		keyword string,
		offset int,
		limit int,
	) ([]*types.DocumentFolderSearchResult, bool, int64, error)
}
