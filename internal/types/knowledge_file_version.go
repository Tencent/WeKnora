package types

import "time"

// KnowledgeFileVersion retains the immutable original source of a superseded
// file. Storage paths are private and are only consumed by authorized downloads.
type KnowledgeFileVersion struct {
	ID          string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	TenantID    uint64    `json:"-" gorm:"not null;index"`
	KnowledgeID string    `json:"knowledge_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_knowledge_file_version"`
	Version     int       `json:"version" gorm:"not null;uniqueIndex:idx_knowledge_file_version"`
	FileName    string    `json:"file_name"`
	FileType    string    `json:"file_type"`
	FileSize    int64     `json:"file_size"`
	FileHash    string    `json:"file_hash"`
	FilePath    string    `json:"-"`
	CreatedAt   time.Time `json:"created_at"`
	IsCurrent   bool      `json:"is_current" gorm:"-"`
}

// KnowledgeFileVersionList includes the current version in its paginated history.
type KnowledgeFileVersionList struct {
	Items []*KnowledgeFileVersion `json:"items"`
	Total int64                   `json:"total"`
}

// CurrentFileVersion treats files created before versioning as version one.
func (k *Knowledge) CurrentFileVersion() int {
	if k.FileVersion < 1 {
		return 1
	}
	return k.FileVersion
}

// FileVersionSnapshot captures original-file metadata for the current source.
func (k *Knowledge) FileVersionSnapshot() *KnowledgeFileVersion {
	createdAt := k.CreatedAt
	if k.FileVersionCreatedAt != nil {
		createdAt = *k.FileVersionCreatedAt
	}
	return &KnowledgeFileVersion{
		KnowledgeID: k.ID, TenantID: k.TenantID,
		Version: k.CurrentFileVersion(), FileName: k.FileName, FileType: k.FileType,
		FileSize: k.FileSize, FileHash: k.FileHash, FilePath: k.FilePath, CreatedAt: createdAt,
	}
}
