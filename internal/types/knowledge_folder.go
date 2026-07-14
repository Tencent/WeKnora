package types

import "time"

const MaxKnowledgeFolderDepth = 20

// KnowledgeFolder is a persisted directory. The root directory is virtual and
// represented by an empty parent/folder ID, never by a row in this table.
type KnowledgeFolder struct {
	ID              string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64    `json:"tenant_id" gorm:"not null;uniqueIndex:idx_knowledge_folder_sibling,priority:1"`
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_knowledge_folder_sibling,priority:2"`
	ParentID        string    `json:"parent_id" gorm:"type:varchar(36);not null;default:'';uniqueIndex:idx_knowledge_folder_sibling,priority:3"`
	Name            string    `json:"name" gorm:"type:varchar(100);not null;uniqueIndex:idx_knowledge_folder_sibling,priority:4"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (KnowledgeFolder) TableName() string { return "knowledge_folders" }

type KnowledgeFolderClosure struct {
	AncestorID   string `json:"ancestor_id" gorm:"type:varchar(36);primaryKey"`
	DescendantID string `json:"descendant_id" gorm:"type:varchar(36);primaryKey"`
	Depth        int    `json:"depth" gorm:"not null"`
}

func (KnowledgeFolderClosure) TableName() string { return "knowledge_folder_closure" }

// KnowledgeFolderView is returned by folder list/detail APIs.
type KnowledgeFolderView struct {
	KnowledgeFolder
	Ancestors            []*KnowledgeFolder `json:"ancestors,omitempty" gorm:"-"`
	DirectKnowledgeCount int64              `json:"direct_knowledge_count" gorm:"-"`
	TotalKnowledgeCount  int64              `json:"total_knowledge_count" gorm:"-"`
	ChildFolderCount     int64              `json:"child_folder_count" gorm:"-"`
	HasChildren          bool               `json:"has_children" gorm:"-"`
}

type EnsureFolderPath struct {
	ClientKey string   `json:"client_key" binding:"required"`
	Segments  []string `json:"segments" binding:"required"`
}

type EnsureFolderPathResult struct {
	ClientKey string `json:"client_key"`
	FolderID  string `json:"folder_id"`
}
