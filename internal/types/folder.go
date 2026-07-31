package types

import (
	"gorm.io/gorm"
	"strings"
	"time"
)

type KnowledgeFolder struct {
	ID              string         `json:"id"                gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64         `json:"tenant_id"`
	KnowledgeBaseID string         `json:"knowledge_base_id" gorm:"type:varchar(36);index"`
	ParentID        string         `json:"parent_id"         gorm:"type:varchar(36)"`
	Name            string         `json:"name"              gorm:"type:varchar(255);not null"`
	Path            string         `json:"path"              gorm:"type:varchar(1024)"`
	Depth           int            `json:"depth"`
	SortOrder       int            `json:"sort_order"        gorm:"default:0"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `json:"deleted_at"         gorm:"index"`
}

type KnowledgeFolderWithStats struct {
	KnowledgeFolder
	KnowledgeCount int64 `json:"knowledge_count"`
	ChildCount     int64 `json:"child_count"`
}

func (KnowledgeFolder) TableName() string { return "knowledge_folders" }

func BuildFolderPath(parentPath, folderID string) string {
	if parentPath == "" {
		return folderID
	}
	return strings.TrimRight(parentPath, "/") + "/" + folderID
}

type FolderTreeNode struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	ParentID       string            `json:"parent_id"`
	Path           string            `json:"path"`
	Depth          int               `json:"depth"`
	SortOrder      int               `json:"sort_order"`
	KnowledgeCount int64             `json:"knowledge_count"`
	ChildCount     int64             `json:"child_count"`
	Children       []*FolderTreeNode `json:"children"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}
