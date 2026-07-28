package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// KnowledgeFolderRootID is the sentinel parent/folder id meaning "the knowledge
// base root" (a document or folder directly under the top level, with no parent
// folder).
const KnowledgeFolderRootID = ""

// FolderRootSentinel is the query-string value callers pass to filter the
// knowledge list down to root-level documents only. The empty string cannot be
// used because it already means "no folder filter" on that endpoint.
const FolderRootSentinel = "__root__"

// KnowledgeFolderMaxDepth caps the folder tree depth (root children are depth 1).
const KnowledgeFolderMaxDepth = 10

// KnowledgeFolder is a first-class directory node for document knowledge bases.
// Folders exist independently of documents — an empty folder persists so users
// can lay out a skeleton and file documents into it later. The tree is an
// adjacency list (ParentID, "" = root); Path is the materialized "/"-joined
// name chain kept purely for cheap display/sort. A document's placement is
// Knowledge.FolderID. Mirrors the WikiFolder design.
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

// BeforeCreate hook generates a UUID for new KnowledgeFolder entities.
func (f *KnowledgeFolder) BeforeCreate(tx *gorm.DB) error {
	if f.ID == "" {
		f.ID = uuid.New().String()
	}
	return nil
}

// KnowledgeFolderNode is one directory node returned to the browser, enriched
// with the live document count directly under it and whether it has child
// folders so the UI can render an expand affordance without a second
// round-trip.
type KnowledgeFolderNode struct {
	KnowledgeFolder
	KnowledgeCount int64 `json:"knowledge_count"`
	HasChildren    bool  `json:"has_children"`
}
