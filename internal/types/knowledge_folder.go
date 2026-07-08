package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const KnowledgeFolderRootID = "__root__"

// KnowledgeFolder is a document-management directory inside a document KB.
type KnowledgeFolder struct {
	ID              string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64         `json:"tenant_id"`
	KnowledgeBaseID string         `json:"knowledge_base_id"`
	ParentID        string         `json:"parent_id" gorm:"type:varchar(36);default:''"`
	Name            string         `json:"name" gorm:"type:varchar(255);not null"`
	Path            string         `json:"path" gorm:"type:varchar(1024);default:''"`
	Depth           int            `json:"depth" gorm:"default:0"`
	SortOrder       int            `json:"sort_order" gorm:"default:0"`
	KnowledgeCount  int64          `json:"knowledge_count" gorm:"-"`
	HasChildren     bool           `json:"has_children" gorm:"-"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

// BeforeCreate assigns a stable UUID when the caller did not provide one.
func (f *KnowledgeFolder) BeforeCreate(tx *gorm.DB) error {
	if f.ID == "" {
		f.ID = uuid.New().String()
	}
	return nil
}

// KnowledgeFolderCreateRequest is the body for creating a document folder.
type KnowledgeFolderCreateRequest struct {
	ParentID string `json:"parent_id"`
	Name     string `json:"name" binding:"required"`
}

// KnowledgeFolderUpdateRequest is the body for renaming a document folder.
type KnowledgeFolderUpdateRequest struct {
	Name string `json:"name" binding:"required"`
}

// KnowledgeFolderMoveRequest is the body for moving a knowledge row to a folder.
type KnowledgeFolderMoveRequest struct {
	FolderID string `json:"folder_id"`
}
