package types

import (
	"time"

	"gorm.io/gorm"
)

const (
	// KnowledgeFolderRootID represents the virtual knowledge base root.
	KnowledgeFolderRootID = ""
	// KnowledgeFolderMaxDepth is the maximum persisted folder depth.
	KnowledgeFolderMaxDepth = 32
	// KnowledgeFolderMaxNameRunes is the persisted folder name limit.
	KnowledgeFolderMaxNameRunes = 255
)

// KnowledgeFolder is a document folder within a knowledge base.
// Callers must assign ID before constructing Path; persistence does not generate IDs.
type KnowledgeFolder struct {
	ID              string `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64 `json:"tenant_id" gorm:"not null"`
	KnowledgeBaseID string `json:"knowledge_base_id" gorm:"type:varchar(36);not null"`
	ParentID        string `json:"parent_id" gorm:"type:varchar(36);not null;default:''"`
	Name            string `json:"name" gorm:"type:varchar(255);not null"`
	// Path is the slash-delimited stable ID path, including leading and trailing slashes.
	Path      string         `json:"path" gorm:"type:varchar(2048);not null"`
	Depth     int            `json:"depth" gorm:"not null"`
	SortOrder int            `json:"sort_order" gorm:"not null;default:0"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

// KnowledgeFolderWithStats contains folder navigation metadata calculated at read time.
type KnowledgeFolderWithStats struct {
	KnowledgeFolder
	KnowledgeCount int64 `json:"knowledge_count" gorm:"-"`
	HasChildren    bool  `json:"has_children" gorm:"-"`
}

// KnowledgeFolderCreateRequest contains client-controlled folder creation fields.
type KnowledgeFolderCreateRequest struct {
	ParentID  string `json:"parent_id"`
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
}

// KnowledgeFolderUpdateRequest contains client-controlled mutable folder fields.
type KnowledgeFolderUpdateRequest struct {
	ParentID  *string `json:"parent_id"`
	Name      *string `json:"name"`
	SortOrder *int    `json:"sort_order"`
}

// TableName specifies the database table name.
func (KnowledgeFolder) TableName() string {
	return "knowledge_folders"
}
