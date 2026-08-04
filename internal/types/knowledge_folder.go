package types

import (
	"time"

	"gorm.io/gorm"
)

// KnowledgeFolder is a directory node used to organize documents in one
// knowledge base. Root folders have a nil ParentID. Path stores a stable chain
// of folder IDs (for example "/a/b"), so renaming does not rewrite descendants.
type KnowledgeFolder struct {
	ID              string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64         `json:"-" gorm:"not null;index"`
	KnowledgeBaseID string         `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index"`
	ParentID        *string        `json:"parent_id" gorm:"type:varchar(36);index"`
	Name            string         `json:"name" gorm:"type:varchar(255);not null"`
	Path            string         `json:"path" gorm:"type:varchar(4096);not null;index"`
	Depth           int            `json:"depth" gorm:"not null;default:1"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `json:"-" gorm:"index"`
}

func (KnowledgeFolder) TableName() string {
	return "knowledge_folders"
}

// KnowledgeFolderScope restricts retrieval to the union of folder subtrees in
// one knowledge base.
type KnowledgeFolderScope struct {
	KnowledgeBaseID string   `json:"knowledge_base_id"`
	FolderIDs       []string `json:"folder_ids"`
}

type CreateKnowledgeFolderRequest struct {
	Name     string  `json:"name"`
	ParentID *string `json:"parent_id"`
}

type UpdateKnowledgeFolderRequest struct {
	Name       *string `json:"name,omitempty"`
	ParentID   *string `json:"parent_id,omitempty"`
	MoveParent bool    `json:"move_parent,omitempty"`
}

type MoveKnowledgeFolderRequest struct {
	FolderID *string `json:"folder_id"`
}

type BatchMoveKnowledgeFolderRequest struct {
	KnowledgeIDs []string `json:"knowledge_ids"`
	FolderID     *string  `json:"folder_id"`
}
