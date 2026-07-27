package types

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrInvalidArgument     = errors.New("invalid argument")
	ErrFolderAlreadyExists = errors.New("folder already exists")
	ErrFolderNotEmpty      = errors.New("folder is not empty")
)

const (
	FolderRootID          = ""
	FolderRootFilter      = "__root__"
	MaxFolderDepth        = 10
	MaxResolveFolderPaths = 1000
)

type KnowledgeFolder struct {
	ID              string             `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64             `json:"tenant_id" gorm:"not null;index:idx_knowledge_folder_scope"`
	KnowledgeBaseID string             `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index:idx_knowledge_folder_scope"`
	ParentID        string             `json:"parent_id" gorm:"type:varchar(36);not null;default:''"`
	Name            string             `json:"name" gorm:"type:varchar(255);not null"`
	Path            string             `json:"path" gorm:"type:varchar(1024);not null;default:'';index"`
	Depth           int                `json:"depth" gorm:"not null;default:0"`
	SortOrder       int                `json:"sort_order" gorm:"not null;default:0"`
	KnowledgeCount  int64              `json:"knowledge_count" gorm:"-"`
	Children        []*KnowledgeFolder `json:"children,omitempty" gorm:"-"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
	DeletedAt       gorm.DeletedAt     `json:"-" gorm:"index"`
}

func (f *KnowledgeFolder) BeforeCreate(tx *gorm.DB) error {
	if f.ID == "" {
		f.ID = uuid.New().String()
	}
	return nil
}

type CreateFolderRequest struct {
	ParentID string `json:"parent_id"`
	Name     string `json:"name" binding:"required"`
}

type ResolveFolderPathsRequest struct {
	CurrentFolderID string   `json:"current_folder_id"`
	Paths           []string `json:"paths" binding:"required"`
}

type ResolvedFolderPath struct {
	RelativePath string `json:"relative_path"`
	FolderID     string `json:"folder_id"`
}

type ResolveFolderPathsResponse struct {
	Paths []ResolvedFolderPath `json:"paths"`
}

type UpdateFolderRequest struct {
	Name string `json:"name" binding:"required"`
}

type MoveFolderRequest struct {
	ParentID string `json:"parent_id"`
}

type BatchFolderScopeRequest struct {
	KnowledgeIDs []string `json:"knowledge_ids"`
	FolderIDs    []string `json:"folder_ids"`
}

type BatchMoveFolderRequest struct {
	KBID string `json:"kb_id" binding:"required"`
	BatchFolderScopeRequest
	TargetFolderID string `json:"target_folder_id"`
}

type FolderScope struct {
	FolderIDs []string `json:"folder_ids"`
}

// FolderKnowledgeScope is the live knowledge selection resolved from folders.
type FolderKnowledgeScope struct {
	KnowledgeIDs      []string `json:"knowledge_ids"`
	FullKnowledgeBase bool     `json:"full_knowledge_base"`
}
