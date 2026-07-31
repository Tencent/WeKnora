package types

import (
	"strings"
	"time"

	"gorm.io/gorm"
)

// KnowledgeFolderRootID is the sentinel parent/folder id meaning "the knowledge
// base root": a document or folder that sits at the top level with no parent.
// An empty string (rather than NULL) keeps the column NOT NULL so the unique
// sibling-name index below covers root-level folders too — NULLs would compare
// as distinct and let duplicate names through at the root.
const KnowledgeFolderRootID = ""

// KnowledgeFolderMaxDepth caps nesting depth. The tree is meant for organising
// documents, not for encoding arbitrary hierarchies: a bound keeps the
// materialized path within its column width and makes recursive scope
// resolution predictable. Root-level folders have Depth == 1.
const KnowledgeFolderMaxDepth = 10

// KnowledgeFolderMaxNameLength matches the `name` column width.
const KnowledgeFolderMaxNameLength = 255

// knowledgeFolderPathSeparator delimits ids inside KnowledgeFolder.Path.
const knowledgeFolderPathSeparator = "/"

// KnowledgeFolder is a first-class directory node inside a document knowledge
// base. Folders exist independently of documents, so an empty folder persists
// and users can lay out a skeleton before filing anything into it.
//
// The tree is an adjacency list — ParentID ("" = root) is the single source of
// truth for structure. Path is a materialized *id* chain of the form
// "/<root-id>/<child-id>/<self-id>/" maintained alongside it.
//
// Storing ids rather than names (the approach wiki_folders takes) is a
// deliberate difference: a rename then touches exactly one row instead of
// rewriting every descendant, and subtree prefix matching stays correct even
// when a folder name contains the separator or two siblings differ only by
// case. Display paths are assembled from names on read, where the caller
// already holds the folder set.
type KnowledgeFolder struct {
	ID              string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64         `json:"tenant_id" gorm:"index"`
	KnowledgeBaseID string         `json:"knowledge_base_id" gorm:"type:varchar(36);index"`
	ParentID        string         `json:"parent_id" gorm:"column:parent_id;type:varchar(36);index;default:''"`
	Name            string         `json:"name" gorm:"type:varchar(255)"`
	Path            string         `json:"path" gorm:"type:varchar(1024);index"`
	Depth           int            `json:"depth" gorm:"default:0"`
	SortOrder       int            `json:"sort_order" gorm:"default:0"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

// TableName specifies the database table name.
func (KnowledgeFolder) TableName() string {
	return "knowledge_folders"
}

// SubtreePrefix returns the LIKE prefix selecting this folder together with
// every descendant. Because Path both starts and ends with the separator, the
// folder's own path also matches the prefix, so a `path LIKE prefix + '%'`
// query yields the inclusive subtree in one statement.
func (f *KnowledgeFolder) SubtreePrefix() string {
	if f == nil || f.Path == "" {
		return ""
	}
	return f.Path
}

// BuildKnowledgeFolderPath composes the materialized id path of a folder from
// its parent's path and its own id. A root-level folder ("" parent path)
// becomes "/<id>/".
func BuildKnowledgeFolderPath(parentPath string, id string) string {
	if parentPath == "" {
		return knowledgeFolderPathSeparator + id + knowledgeFolderPathSeparator
	}
	return parentPath + id + knowledgeFolderPathSeparator
}

// KnowledgeFolderPathIDs splits a materialized path back into its ordered id
// chain (root first, the folder itself last).
func KnowledgeFolderPathIDs(path string) []string {
	trimmed := strings.Trim(path, knowledgeFolderPathSeparator)
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, knowledgeFolderPathSeparator)
}

// IsDescendantOfKnowledgeFolder reports whether candidate sits inside the
// subtree rooted at ancestor (inclusive). Both arguments are materialized
// paths.
func IsDescendantOfKnowledgeFolder(candidatePath string, ancestorPath string) bool {
	if candidatePath == "" || ancestorPath == "" {
		return false
	}
	return strings.HasPrefix(candidatePath, ancestorPath)
}

// KnowledgeFolderNode is one directory node returned to the UI, enriched with
// document counts and a child flag so a tree can render expand affordances and
// badges without a second round-trip per node.
type KnowledgeFolderNode struct {
	KnowledgeFolder
	// DocumentCount counts documents filed directly in this folder.
	DocumentCount int64 `json:"document_count"`
	// TotalDocumentCount counts documents in this folder and all descendants,
	// which is what a collapsed row should display.
	TotalDocumentCount int64 `json:"total_document_count"`
	// HasChildren reports whether at least one child folder exists.
	HasChildren bool `json:"has_children"`
	// NamePath is the human-readable "/"-joined ancestor name chain, provided
	// so breadcrumbs need no client-side reconstruction.
	NamePath []string `json:"name_path,omitempty"`
}

// KnowledgeFolderListResponse is the payload for listing folders. When
// Recursive is false it holds the direct children of ParentID; when true it
// holds the whole tree of the knowledge base in a single response so the UI can
// hydrate a full tree without one request per level.
type KnowledgeFolderListResponse struct {
	ParentID string                `json:"parent_id"`
	Folders  []KnowledgeFolderNode `json:"folders"`
	// RootDocumentCount is the number of documents sitting at the knowledge
	// base root (folder_id == ""), which has no folder row to hang a count on.
	RootDocumentCount int64 `json:"root_document_count"`
}

// KnowledgeFolderCreateRequest creates a new (initially empty) folder.
type KnowledgeFolderCreateRequest struct {
	ParentID string `json:"parent_id"`
	Name     string `json:"name" binding:"required"`
}

// KnowledgeFolderUpdateRequest renames and/or reparents a folder. ParentID is
// honoured only when MoveParent is true, so a pure rename need not re-send the
// (possibly root "") parent and risk an accidental move to the root.
type KnowledgeFolderUpdateRequest struct {
	Name       string `json:"name,omitempty"`
	ParentID   string `json:"parent_id,omitempty"`
	MoveParent bool   `json:"move_parent,omitempty"`
}

// KnowledgeFolderDeleteRequest controls how a non-empty folder is handled.
// The default (Recursive false) refuses to delete a folder that still holds
// documents or child folders, keeping deletion non-destructive; callers that
// really mean it can opt into relocating the contents to the parent folder.
type KnowledgeFolderDeleteRequest struct {
	// Strategy selects the behaviour for a non-empty folder:
	//   ""       / "fail"    -> reject with a conflict (default)
	//   "reparent"           -> move documents and child folders up one level
	Strategy string `json:"strategy,omitempty"`
}

// Knowledge folder delete strategies.
const (
	KnowledgeFolderDeleteFail     = "fail"
	KnowledgeFolderDeleteReparent = "reparent"
)

// KnowledgeMoveToFolderRequest relocates documents into FolderID ("" = root).
// Every id must belong to KnowledgeBaseID; the request is rejected wholesale
// otherwise so a partial move cannot scatter documents across knowledge bases.
type KnowledgeMoveToFolderRequest struct {
	KnowledgeBaseID string   `json:"kb_id" binding:"required"`
	KnowledgeIDs    []string `json:"knowledge_ids" binding:"required"`
	FolderID        string   `json:"folder_id"`
}

// KnowledgeFolderScope constrains retrieval to one or more folder subtrees
// inside a knowledge base. It is the folder analogue of TagScope.
type KnowledgeFolderScope struct {
	KnowledgeBaseID string   `json:"knowledge_base_id"`
	FolderIDs       []string `json:"folder_ids"`
	// Recursive includes documents in descendant folders. It defaults to true
	// at the API boundary because "ask this folder" almost always means the
	// whole subtree; callers wanting a single level set it explicitly.
	Recursive bool `json:"recursive"`
}
