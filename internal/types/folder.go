package types

import (
	"time"

	"gorm.io/gorm"
)

// KnowledgeFolderMaxDepth is the hard cap on folder nesting for a knowledge
// base. The adjacency-list model supports arbitrary depth, but an unbounded
// tree is hard to render and reason about, so creation/move refuse to exceed
// this bound. It mirrors the defensive intent of WikiCategoryMaxDepth.
const KnowledgeFolderMaxDepth = 10

// KnowledgeFolderRootID is the sentinel parent/folder id meaning "the root
// level of a knowledge base" (a folder or knowledge directly under the top
// level, with no parent folder).
const KnowledgeFolderRootID = ""

// KnowledgeFolder is a first-class directory node in the knowledge browser.
// Folders exist independently of knowledges — an empty folder persists so
// users can lay out a skeleton and file documents into it later. The tree is
// an adjacency list (ParentID, "" = root); Path is the materialized "/"-joined
// name chain kept purely for cheap display/sort. A knowledge's placement is
// Knowledge.FolderID.
type KnowledgeFolder struct {
	ID              string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64         `json:"tenant_id" gorm:"index"`
	KnowledgeBaseID string         `json:"knowledge_base_id" gorm:"type:varchar(36);index"`
	ParentID        string         `json:"parent_id" gorm:"column:parent_id;type:varchar(36);index;default:''"`
	Name            string         `json:"name" gorm:"type:varchar(255)"`
	Path            string         `json:"path" gorm:"type:varchar(1024)"`
	Depth           int            `json:"depth" gorm:"default:0"`
	SortOrder       int            `json:"sort_order" gorm:"default:0"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

// TableName specifies the database table name
func (KnowledgeFolder) TableName() string {
	return "knowledge_folders"
}

// KnowledgeFolderNode is one directory node returned to the browser, enriched
// with the live knowledge count directly under it and whether it has child
// folders so the UI can render an expand affordance without a second
// round-trip.
type KnowledgeFolderNode struct {
	KnowledgeFolder
	KnowledgeCount int64 `json:"knowledge_count"`
	HasChildren    bool  `json:"has_children"`
}

// KnowledgeFolderListResponse is the payload for listing the direct children
// of a folder (parent_id = "" = root level).
type KnowledgeFolderListResponse struct {
	ParentID string                 `json:"parent_id"`
	Folders  []KnowledgeFolderNode  `json:"folders"`
}

// KnowledgeFolderCreateRequest creates a new (initially empty) folder under
// ParentID.
type KnowledgeFolderCreateRequest struct {
	ParentID string `json:"parent_id"`
	Name     string `json:"name"`
}

// KnowledgeFolderUpdateRequest renames and/or reparents a folder. ParentID is
// applied only when MoveParent is true so a pure rename doesn't have to
// re-send the (possibly root "") parent and risk an accidental move.
type KnowledgeFolderUpdateRequest struct {
	Name       string `json:"name,omitempty"`
	ParentID   string `json:"parent_id,omitempty"`
	MoveParent bool   `json:"move_parent,omitempty"`
}

// KnowledgeFolderMoveRequest relocates a knowledge (identified by
// KnowledgeID) into FolderID ("" = root).
type KnowledgeFolderMoveRequest struct {
	KnowledgeID string `json:"knowledge_id" binding:"required"`
	FolderID    string `json:"folder_id"`
}

// SetKnowledgeFolderRequest is the body for moving a single knowledge into a
// folder.
type SetKnowledgeFolderRequest struct {
	FolderID string `json:"folder_id"`
}
