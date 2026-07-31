package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// KnowledgeFolder represents a folder inside a knowledge base for organizing knowledge entries.
// Uses Materialized Path pattern for efficient subtree queries.
type KnowledgeFolder struct {
	// Unique identifier of the folder
	ID string `json:"id" gorm:"type:varchar(36);primaryKey"`
	// Tenant ID
	TenantID uint64 `json:"tenant_id"`
	// ID of the knowledge base this folder belongs to
	KnowledgeBaseID string `json:"knowledge_base_id"`
	// Display name of the folder
	Name string `json:"name"`
	// ID of the parent folder, nil for root-level folders
	ParentFolderID *string `json:"parent_folder_id"`
	// Materialized path (e.g., /parent_id/self_id/) for efficient subtree queries
	Path string `json:"path"`
	// Depth level (1-based, depth=1 means direct child of root)
	Depth int `json:"depth"`
	// Sort order within the parent folder
	SortOrder int `json:"sort_order"`
	// Optional display color
	Color string `json:"color"`
	// Optional description
	Description string `json:"description"`
	// Creation time
	CreatedAt time.Time `json:"created_at"`
	// Last updated time
	UpdatedAt time.Time `json:"updated_at"`
	// Soft delete timestamp
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`

	// Non-persisted fields populated on query
	Children       []*KnowledgeFolder `json:"children,omitempty" gorm:"-"`
	KnowledgeCount int64              `json:"knowledge_count" gorm:"-"`
}

// TableName overrides the default table name.
func (KnowledgeFolder) TableName() string {
	return "knowledge_folders"
}

// BeforeCreate hook generates a UUID for new KnowledgeFolder entities.
func (f *KnowledgeFolder) BeforeCreate(tx *gorm.DB) (err error) {
	if f.ID == "" {
		f.ID = uuid.New().String()
	}
	return nil
}

// CreateFolderRequest is the DTO for creating a folder.
type CreateFolderRequest struct {
	Name           string  `json:"name" binding:"required,max=255"`
	ParentFolderID *string `json:"parent_folder_id"`
	Color          string  `json:"color"`
	Description    string  `json:"description"`
}

// UpdateFolderRequest is the DTO for updating folder properties.
type UpdateFolderRequest struct {
	Name        *string `json:"name" binding:"omitempty,max=255"`
	Color       *string `json:"color"`
	Description *string `json:"description"`
	SortOrder   *int    `json:"sort_order"`
}

// MoveFolderRequest is the DTO for moving a folder to a new parent.
type MoveFolderRequest struct {
	TargetParentFolderID *string `json:"target_parent_folder_id"`
}

// MaxFolderDepth is the maximum allowed nesting depth for folders.
const MaxFolderDepth = 10
