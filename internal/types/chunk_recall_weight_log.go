package types

import "time"

type ChunkRecallWeightLog struct {
	ID           string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID     uint64    `json:"tenant_id" gorm:"index"`
	ChunkID      string    `json:"chunk_id" gorm:"type:varchar(36);index"`
	TriggerType  string    `json:"trigger_type" gorm:"type:varchar(30)"`
	UserID       string    `json:"user_id,omitempty" gorm:"type:varchar(36)"`
	MessageID    string    `json:"message_id,omitempty" gorm:"type:varchar(36)"`
	OldWeight    float64   `json:"old_weight"`
	NewWeight    float64   `json:"new_weight"`
	LikeCount    int64     `json:"like_count"`
	DislikeCount int64     `json:"dislike_count"`
	PositiveRate float64   `json:"positive_rate"`
	CreatedAt    time.Time `json:"created_at" gorm:"autoCreateTime"`
}

func (ChunkRecallWeightLog) TableName() string {
	return "chunk_recall_weight_logs"
}
