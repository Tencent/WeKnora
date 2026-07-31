package types

import (
	"time"

	"gorm.io/gorm"
)

// DocumentFolderRootID is the sentinel parent_id meaning "the document-KB
// root" (a folder or document directly under the top level, with no parent).
// Equal to "" so the default value of parent_id on the DB row means root.
const DocumentFolderRootID = ""

// DocumentFolderDeleteMode selects the explicit destructive behavior for a
// non-empty folder tree.
type DocumentFolderDeleteMode string

const (
	// DocumentFolderDeleteModeKeepDocuments removes only the directory tree and
	// files every affected document at the virtual KB root.
	DocumentFolderDeleteModeKeepDocuments DocumentFolderDeleteMode = "keep_documents"
	// DocumentFolderDeleteModeDeleteAll permanently deletes the directory tree
	// together with all documents and derived data in its subtree.
	DocumentFolderDeleteModeDeleteAll DocumentFolderDeleteMode = "delete_all"
)

// IsValid reports whether the mode is one of the two explicit non-empty
// folder deletion behaviors exposed by the API.
func (m DocumentFolderDeleteMode) IsValid() bool {
	return m == DocumentFolderDeleteModeKeepDocuments || m == DocumentFolderDeleteModeDeleteAll
}

// Hard limits enforced by the application layer. They cap the cost of the
// query-time BFS subtree expansion (see the L3 retrieval design): the worst
// case is MaxFoldersPerKB folder IDs being expanded into a vector-store
// terms/IN filter, which stays well under every backend's bind/term limit.
const (
	MaxFolderDepth                = 20
	MaxFoldersPerKB               = 5000
	MaxFolderNameLen              = 255
	MaxFolderPathLen              = 1024
	DefaultDocumentFolderPageSize = 50
	MaxDocumentFolderPageSize     = 200
)

// DocumentFolder is a directory node inside a document-type knowledge base.
// Folders exist independently of documents — an empty folder persists so users
// can lay out a skeleton and file documents into it later. The tree is an
// adjacency list (ParentID, "" = root); Path is the materialized "/"-joined
// name chain kept purely for cheap display/sort. A document's placement is
// Knowledge.FolderID.
//
// This struct mirrors WikiFolder exactly in shape — both features converge on
// the same adjacency-list + materialized-path model.
type DocumentFolder struct {
	ID              string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64         `json:"tenant_id" gorm:"index"`
	KnowledgeBaseID string         `json:"knowledge_base_id" gorm:"type:varchar(36);index"`
	ParentID        string         `json:"parent_id" gorm:"column:parent_id;type:varchar(36);index;default:''"`
	Name            string         `json:"name" gorm:"type:varchar(255)"`
	Path            string         `json:"path" gorm:"type:varchar(1024)"`
	Depth           int            `json:"depth" gorm:"default:0"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

// TableName specifies the database table name.
func (DocumentFolder) TableName() string {
	return "document_folders"
}

// DocumentFolderNode is one directory node returned to the browser, enriched
// with the live document count directly under it and whether it has child
// folders so the UI can render an expand affordance without a second
// round-trip.
type DocumentFolderNode struct {
	DocumentFolder
	DocumentCount int64 `json:"document_count"`
	HasChildren   bool  `json:"has_children"`
}

// DocumentFolderListResponse is the payload for listing the direct children of
// a folder (parent_id="" = root level).
type DocumentFolderListResponse struct {
	ParentID   string               `json:"parent_id"`
	Folders    []DocumentFolderNode `json:"folders"`
	NextCursor string               `json:"next_cursor,omitempty"`
	HasMore    bool                 `json:"has_more"`
}

// DocumentFolderDeleteImpact describes the live subtree affected by deleting
// a folder. ActiveDocumentCount lets the UI explain why the keep-documents
// mode is temporarily unavailable while parsing is still in flight.
type DocumentFolderDeleteImpact struct {
	FolderCount         int `json:"folder_count"`
	DocumentCount       int `json:"document_count"`
	ActiveDocumentCount int `json:"active_document_count"`
}

// DocumentFolderPageCursor is the stable keyset position used to page through
// sibling folders. It mirrors the repository ordering exactly, so inserts or
// deletes before the current page cannot shift later rows out of the result.
type DocumentFolderPageCursor struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// DocumentFolderSearchResult is the compact, typed result returned by the
// global @mention folder search.
type DocumentFolderSearchResult struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Path              string `json:"path"`
	ParentID          string `json:"parent_id"`
	KnowledgeBaseID   string `json:"knowledge_base_id"`
	KnowledgeBaseName string `json:"knowledge_base_name"`
}

// DocumentFolderCreateRequest creates a new (initially empty) folder under
// ParentID. ParentID == "" (or omitted) places the folder at the root level.
type DocumentFolderCreateRequest struct {
	ParentID string `json:"parent_id"`
	Name     string `json:"name" binding:"required,min=1,max=255"`
}

// DocumentFolderUpdateRequest renames a folder. Folder reparenting is not part
// of the document-folder API.
type DocumentFolderUpdateRequest struct {
	Name *string `json:"name,omitempty"`
}

// FolderScope is the QA passthrough layer: a user's request to constrain
// retrieval to the subtree rooted at FolderID inside KnowledgeBaseID.
// FolderID must be non-empty (root Q&A uses the plain KB scope, not a
// folder_id="" filter, so old chunks without a folder_id field are never
// accidentally filtered out).
type FolderScope struct {
	KnowledgeBaseID string `json:"knowledge_base_id"`
	FolderID        string `json:"folder_id"`
}
