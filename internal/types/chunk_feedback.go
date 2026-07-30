package types

import (
	"time"

	"gorm.io/gorm"
)

// FeedbackType represents the type of user feedback
type FeedbackType string

const (
	FeedbackTypeLike      FeedbackType = "like"
	FeedbackTypeDislike   FeedbackType = "dislike"
	FeedbackTypeUnlike    FeedbackType = "unlike"
	FeedbackTypeUndislike FeedbackType = "undislike"
)

// DislikeReason represents the reason for a dislike
type DislikeReason string

const (
	DislikeReasonInaccurate   DislikeReason = "inaccurate"
	DislikeReasonIncomplete   DislikeReason = "incomplete"
	DislikeReasonIrrelevant   DislikeReason = "irrelevant"
	DislikeReasonOther        DislikeReason = "other"
)

// TriggerType represents what triggered a weight change
type TriggerType string

const (
	TriggerTypeUserFeedback TriggerType = "user_feedback"
	TriggerTypeAutoAdjust   TriggerType = "auto_adjust"
	TriggerTypeManualReset  TriggerType = "manual_reset"
	TriggerTypeBatchUpdate  TriggerType = "batch_update"
)

// MessageChunkRelation represents the relationship between a message and its referenced chunks
type MessageChunkRelation struct {
	ID              string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64    `json:"tenant_id"`
	MessageID       string    `json:"message_id" gorm:"type:varchar(36);not null;index"`
	SessionID       string    `json:"session_id" gorm:"type:varchar(36);not null;index"`
	ChunkID         string    `json:"chunk_id" gorm:"type:varchar(36);not null;index"`
	KnowledgeID     string    `json:"knowledge_id" gorm:"type:varchar(36);not null"`
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index"`
	Score           *float64  `json:"score" gorm:"type:decimal(10,6)"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

// TableName returns the table name for MessageChunkRelation
func (MessageChunkRelation) TableName() string {
	return "message_chunk_relations"
}

// ChunkFeedback represents a user's feedback on a chat message
type ChunkFeedback struct {
	ID                   string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID             uint64         `json:"tenant_id"`
	UserID               string         `json:"user_id" gorm:"type:varchar(64);not null;default:'';index"`
	SessionID            string         `json:"session_id" gorm:"type:varchar(36);not null;index"`
	MessageID            string         `json:"message_id" gorm:"type:varchar(36);not null;index"`
	FeedbackType         FeedbackType   `json:"feedback_type" gorm:"type:varchar(20);not null;index"`
	DislikeReason        *DislikeReason `json:"dislike_reason" gorm:"type:varchar(50)"`
	DislikeReasonDetail  *string        `json:"dislike_reason_detail" gorm:"type:text"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
	DeletedAt            gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

// TableName returns the table name for ChunkFeedback
func (ChunkFeedback) TableName() string {
	return "chunk_feedbacks"
}

// ChunkWeightLog represents a log entry for chunk weight changes
type ChunkWeightLog struct {
	ID              string      `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64      `json:"tenant_id"`
	ChunkID         string      `json:"chunk_id" gorm:"type:varchar(36);not null;index"`
	KnowledgeBaseID string      `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index"`
	TriggerType     TriggerType `json:"trigger_type" gorm:"type:varchar(30);not null;index"`
	TriggerReason   *string     `json:"trigger_reason" gorm:"type:varchar(200)"`
	OldWeight       float64     `json:"old_weight" gorm:"type:decimal(5,2);not null"`
	NewWeight       float64     `json:"new_weight" gorm:"type:decimal(5,2);not null"`
	OldLikeRate     *float64    `json:"old_like_rate" gorm:"type:decimal(5,4)"`
	NewLikeRate     *float64    `json:"new_like_rate" gorm:"type:decimal(5,4)"`
	FeedbackID      *string     `json:"feedback_id" gorm:"type:varchar(36)"`
	OperatorID      *string     `json:"operator_id" gorm:"type:varchar(64)"`
	CreatedAt       time.Time   `json:"created_at"`
}

// TableName returns the table name for ChunkWeightLog
func (ChunkWeightLog) TableName() string {
	return "chunk_weight_logs"
}

// ChunkFeedbackConfigKey represents configuration keys
type ChunkFeedbackConfigKey string

const (
	ConfigKeyLikeRateHighThreshold   ChunkFeedbackConfigKey = "like_rate_high_threshold"
	ConfigKeyLikeRateLowThreshold   ChunkFeedbackConfigKey = "like_rate_low_threshold"
	ConfigKeyLikeRateOptimizeThreshold ChunkFeedbackConfigKey = "like_rate_optimize_threshold"
	ConfigKeyWeightBoostFactor      ChunkFeedbackConfigKey = "weight_boost_factor"
	ConfigKeyWeightPenaltyFactor    ChunkFeedbackConfigKey = "weight_penalty_factor"
	ConfigKeyWeightMin              ChunkFeedbackConfigKey = "weight_min"
	ConfigKeyWeightMax              ChunkFeedbackConfigKey = "weight_max"
	ConfigKeyMinFeedbackCount       ChunkFeedbackConfigKey = "min_feedback_count"
)

// ChunkFeedbackConfig represents a configuration entry for the feedback system
type ChunkFeedbackConfig struct {
	ID          string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID    uint64    `json:"tenant_id"`
	ConfigKey   string    `json:"config_key" gorm:"type:varchar(50);not null"`
	ConfigValue string    `json:"config_value" gorm:"type:varchar(200);not null"`
	Description *string   `json:"description" gorm:"type:varchar(200)"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TableName returns the table name for ChunkFeedbackConfig
func (ChunkFeedbackConfig) TableName() string {
	return "chunk_feedback_config"
}

// ChunkStats represents statistics for a chunk
type ChunkStats struct {
	LikeCount            int     `json:"like_count"`
	DislikeCount         int     `json:"dislike_count"`
	LikeRate             float64 `json:"like_rate"`
	RecallWeight         float64 `json:"recall_weight"`
	IsPendingOptimization bool    `json:"is_pending_optimization"`
	RelatedSessionCount   int     `json:"related_session_count"`
}

// ChunkStatsDetail represents detailed statistics for a chunk with feedback breakdown
type ChunkStatsDetail struct {
	ChunkStats
	DislikeReasonStats map[string]int `json:"dislike_reason_stats"`
}

// ChunkFeedbackSummary represents aggregated feedback statistics
type ChunkFeedbackSummary struct {
	TotalChunks          int     `json:"total_chunks"`
	TotalFeedbacks       int     `json:"total_feedbacks"`
	TotalLikes           int     `json:"total_likes"`
	TotalDislikes        int     `json:"total_dislikes"`
	AverageLikeRate      float64 `json:"average_like_rate"`
	PendingOptimizationCount int  `json:"pending_optimization_count"`
}
