package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const MaxKnowledgeFolderDepth = 10

// KnowledgeFolder is an organizational node inside one knowledge base.
// Retrieval settings remain owned by KnowledgeBase; folders only define a
// stable, recursively-resolved document scope.
type KnowledgeFolder struct {
	ID              string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64         `json:"tenant_id" gorm:"index"`
	KnowledgeBaseID string         `json:"knowledge_base_id" gorm:"type:varchar(36);index"`
	ParentFolderID  *string        `json:"parent_folder_id,omitempty" gorm:"type:varchar(36);index"`
	Name            string         `json:"name" gorm:"type:varchar(255)"`
	Description     string         `json:"description"`
	Depth           int            `json:"depth" gorm:"not null;default:1"`
	SortOrder       int            `json:"sort_order" gorm:"not null;default:0"`
	CreatorID       string         `json:"creator_id" gorm:"type:varchar(36);index"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

func (KnowledgeFolder) TableName() string { return "knowledge_folders" }

func (f *KnowledgeFolder) BeforeCreate(_ *gorm.DB) error {
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	return nil
}

// KnowledgeFolderNode is returned to the browser with live document counts.
type KnowledgeFolderNode struct {
	KnowledgeFolder
	DirectKnowledgeCount    int64 `json:"direct_knowledge_count"`
	RecursiveKnowledgeCount int64 `json:"recursive_knowledge_count"`
	HasChildren             bool  `json:"has_children"`
}

// FolderScope is the stable logical scope persisted with a QA request. It is
// resolved to current KnowledgeIDs for every turn.
type FolderScope struct {
	KnowledgeBaseID    string `json:"knowledge_base_id"`
	FolderID           string `json:"folder_id"`
	IncludeDescendants bool   `json:"include_descendants"`
}

// KnowledgePlacement selects the folder used when a knowledge item is created.
type KnowledgePlacement struct {
	FolderID *string `json:"folder_id,omitempty"`
}

type KnowledgeFolderCreateRequest struct {
	ParentFolderID *string `json:"parent_folder_id,omitempty"`
	Name           string  `json:"name" binding:"required"`
	Description    string  `json:"description"`
}

type KnowledgeFolderUpdateRequest struct {
	Name            *string `json:"name,omitempty"`
	Description     *string `json:"description,omitempty"`
	ParentFolderID  *string `json:"parent_folder_id,omitempty"`
	MoveToRoot      bool    `json:"move_to_root,omitempty"`
	UpdateSortOrder bool    `json:"update_sort_order,omitempty"`
	SortOrder       int     `json:"sort_order,omitempty"`
}

type MoveKnowledgeToFolderRequest struct {
	KnowledgeIDs []string `json:"knowledge_ids" binding:"required,min=1"`
	FolderID     *string  `json:"folder_id"`
}
