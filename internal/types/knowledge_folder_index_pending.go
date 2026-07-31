package types

import "time"

// KnowledgeFolderIndexPending is the durable latest-wins pending row for
// propagating authoritative knowledge folder placement to derived indexes.
type KnowledgeFolderIndexPending struct {
	ID               string    `json:"-" gorm:"type:varchar(36);primaryKey"`
	TenantID         uint64    `json:"-" gorm:"not null"`
	KnowledgeBaseID  string    `json:"-" gorm:"type:varchar(36);not null"`
	KnowledgeID      string    `json:"-" gorm:"type:varchar(36);not null"`
	TargetFolderID   string    `json:"-" gorm:"type:varchar(36);not null;default:''"`
	RequestedVersion uint64    `json:"-" gorm:"type:bigint;not null"`
	CreatedAt        time.Time `json:"-"`
	UpdatedAt        time.Time `json:"-"`
}

// TableName binds the model to the intentionally singular pending table.
func (KnowledgeFolderIndexPending) TableName() string {
	return "knowledge_folder_index_pending"
}
