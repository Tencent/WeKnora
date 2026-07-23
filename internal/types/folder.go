package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Folder is an adjacency-list directory node within a knowledge base.
// A nil ParentID identifies a top-level folder.
type Folder struct {
	ID              string         `json:"id"                gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64         `json:"tenant_id"         gorm:"not null"`
	KnowledgeBaseID string         `json:"knowledge_base_id" gorm:"type:varchar(36);not null"`
	ParentID        *string        `json:"parent_id"         gorm:"type:varchar(36);default:null"`
	Name            string         `json:"name"              gorm:"type:varchar(255);not null"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `json:"deleted_at"        gorm:"index"`
}

// BeforeCreate hook generates a UUID for new Folder entities before they are created.
func (f *Folder) BeforeCreate(tx *gorm.DB) (err error) {
	if f.ID == "" {
		f.ID = uuid.New().String()
	}
	return nil
}
