package types

import "time"

const (
	FeedbackTypeLike    = "like"
	FeedbackTypeDislike = "dislike"
	FeedbackTypeNone    = "none"

	ChunkWeightLogSourceFeedback   = "user_feedback"
	ChunkWeightLogSourceAdminReset = "admin_reset"
)

// MessageChunkRef stores the attribution relation between an assistant message
// and a concrete knowledge chunk used as a reference.
type MessageChunkRef struct {
	ID              string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	SessionTenantID uint64    `json:"session_tenant_id" gorm:"column:session_tenant_id;not null;uniqueIndex:idx_message_chunk_refs_session_msg_chunk,priority:1"`
	ChunkTenantID   uint64    `json:"chunk_tenant_id" gorm:"column:chunk_tenant_id;not null;index"`
	SessionID       string    `json:"session_id" gorm:"column:session_id;type:varchar(36);not null;index"`
	MessageID       string    `json:"message_id" gorm:"column:message_id;type:varchar(36);not null;index;uniqueIndex:idx_message_chunk_refs_session_msg_chunk,priority:2"`
	ChunkID         string    `json:"chunk_id" gorm:"column:chunk_id;type:varchar(36);not null;index;uniqueIndex:idx_message_chunk_refs_session_msg_chunk,priority:3"`
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"column:knowledge_base_id;type:varchar(36);not null;index"`
	KnowledgeID     string    `json:"knowledge_id" gorm:"column:knowledge_id;type:varchar(36);not null;index"`
	ChunkIndex      int       `json:"chunk_index" gorm:"column:chunk_index;not null;default:0"`
	ChunkType       string    `json:"chunk_type" gorm:"column:chunk_type;type:varchar(32);not null;default:''"`
	MatchType       MatchType `json:"match_type" gorm:"column:match_type;not null;default:0"`
	Score           float64   `json:"score" gorm:"column:score;not null;default:0"`
	CreatedAt       time.Time `json:"created_at"`
}

// MessageFeedback stores the current feedback for one user on one assistant message.
type MessageFeedback struct {
	ID              string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	SessionTenantID uint64    `json:"session_tenant_id" gorm:"column:session_tenant_id;not null;uniqueIndex:idx_message_feedbacks_current,priority:1"`
	UserID          string    `json:"user_id" gorm:"column:user_id;type:varchar(36);not null;uniqueIndex:idx_message_feedbacks_current,priority:2;index"`
	SessionID       string    `json:"session_id" gorm:"column:session_id;type:varchar(36);not null;index"`
	MessageID       string    `json:"message_id" gorm:"column:message_id;type:varchar(36);not null;uniqueIndex:idx_message_feedbacks_current,priority:3;index"`
	FeedbackType    string    `json:"feedback_type" gorm:"column:feedback_type;type:varchar(16);not null;default:'none'"`
	ReasonCode      string    `json:"reason_code,omitempty" gorm:"column:reason_code;type:varchar(64);not null;default:''"`
	ReasonText      string    `json:"reason_text,omitempty" gorm:"column:reason_text;type:text;not null;default:''"`
	FeedbackAt      time.Time `json:"feedback_at" gorm:"column:feedback_at;not null;index"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ChunkWeightLog records automatic or manual changes to a chunk's recall weight.
type ChunkWeightLog struct {
	ID               string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	ChunkTenantID    uint64    `json:"chunk_tenant_id" gorm:"column:chunk_tenant_id;not null;index:idx_chunk_weight_logs_chunk_created,priority:1"`
	ChunkID          string    `json:"chunk_id" gorm:"column:chunk_id;type:varchar(36);not null;index:idx_chunk_weight_logs_chunk_created,priority:2"`
	OldWeight        float64   `json:"old_weight" gorm:"column:old_weight;not null"`
	NewWeight        float64   `json:"new_weight" gorm:"column:new_weight;not null"`
	Source           string    `json:"source" gorm:"column:source;type:varchar(32);not null"`
	SourceMessageID  string    `json:"source_message_id,omitempty" gorm:"column:source_message_id;type:varchar(36);not null;default:'';index"`
	SourceFeedbackID string    `json:"source_feedback_id,omitempty" gorm:"column:source_feedback_id;type:varchar(36);not null;default:'';index"`
	Reason           string    `json:"reason,omitempty" gorm:"column:reason;type:text;not null;default:''"`
	CreatedAt        time.Time `json:"created_at" gorm:"index:idx_chunk_weight_logs_chunk_created,priority:3"`
}

type MessageFeedbackRequest struct {
	FeedbackType string `json:"feedback_type" binding:"required"`
	ReasonCode   string `json:"reason_code,omitempty"`
	ReasonText   string `json:"reason_text,omitempty"`
}

type MessageFeedbackResponse struct {
	FeedbackType string `json:"feedback_type"`
	ReasonCode   string `json:"reason_code,omitempty"`
	ReasonText   string `json:"reason_text,omitempty"`
}

type FeedbackReasonStat struct {
	ReasonCode string `json:"reason_code"`
	Count      int64  `json:"count"`
}

type ChunkFeedbackStats struct {
	ChunkID                string               `json:"chunk_id"`
	ChunkTenantID          uint64               `json:"chunk_tenant_id"`
	LikeCount              int64                `json:"like_count"`
	DislikeCount           int64                `json:"dislike_count"`
	PositiveRate           *float64             `json:"positive_rate"`
	RecallWeight           float64              `json:"recall_weight"`
	NeedsOptimization      bool                 `json:"needs_optimization"`
	AssociatedSessionCount int64                `json:"associated_session_count"`
	ReasonStats            []FeedbackReasonStat `json:"reason_stats"`
	FeedbackResetAt        *time.Time           `json:"feedback_reset_at,omitempty"`
}
