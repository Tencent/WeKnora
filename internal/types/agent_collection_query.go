package types

import "time"

type AgentCollectionProfileFilter struct {
	TenantID    uint64
	AgentID     string
	UserID      string
	Keyword     string
	Complete    *bool
	UpdatedFrom *time.Time
	UpdatedTo   *time.Time
	FieldKey    string
	FieldValue  string
	Page        int
	PageSize    int
}

type AgentCollectionProfilePage struct {
	Items    []*AgentCollectionProfile `json:"items"`
	Total    int64                     `json:"total"`
	Page     int                       `json:"page"`
	PageSize int                       `json:"page_size"`
}

type AgentCollectionSummary struct {
	Users        int64 `json:"users"`
	Profiles     int64 `json:"profiles"`
	UpdatedToday int64 `json:"updated_today"`
	Incomplete   int64 `json:"incomplete"`
}

type AgentCollectionHistoryPage struct {
	Items    []*AgentCollectionHistory `json:"items"`
	Total    int64                     `json:"total"`
	Page     int                       `json:"page"`
	PageSize int                       `json:"page_size"`
}

type AgentCollectionExportStatus string

const (
	AgentCollectionExportPending   AgentCollectionExportStatus = "pending"
	AgentCollectionExportRunning   AgentCollectionExportStatus = "running"
	AgentCollectionExportCompleted AgentCollectionExportStatus = "completed"
	AgentCollectionExportFailed    AgentCollectionExportStatus = "failed"
)

type AgentCollectionExport struct {
	ID             string                      `json:"id" gorm:"type:varchar(36);primaryKey"`
	ActorUserID    string                      `json:"actor_user_id" gorm:"type:varchar(36);not null;index"`
	Format         string                      `json:"format" gorm:"type:varchar(8);not null"`
	FilterSnapshot JSONMap                     `json:"filter_snapshot" gorm:"type:jsonb;not null"`
	Status         AgentCollectionExportStatus `json:"status" gorm:"type:varchar(16);not null"`
	StoragePath    string                      `json:"storage_path,omitempty" gorm:"type:varchar(512)"`
	Filename       string                      `json:"filename,omitempty" gorm:"type:varchar(255)"`
	RowCount       int64                       `json:"row_count"`
	ErrorMessage   string                      `json:"error_message,omitempty" gorm:"type:varchar(1000)"`
	CreatedAt      time.Time                   `json:"created_at"`
	UpdatedAt      time.Time                   `json:"updated_at"`
	ExpiresAt      *time.Time                  `json:"expires_at,omitempty"`
}

func (AgentCollectionExport) TableName() string { return "agent_collection_exports" }
