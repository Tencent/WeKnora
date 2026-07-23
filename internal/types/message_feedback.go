package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	MessageFeedbackLike    = 1
	MessageFeedbackDislike = -1

	ChunkFeedbackStatusNormal              = "normal"
	ChunkFeedbackStatusPendingOptimization = "pending_optimization"

	ChunkWeightTriggerUserFeedback = "user_feedback"
	ChunkWeightTriggerAdminReset   = "admin_reset"
)

// MessageFeedback stores one user's current evaluation of an assistant message.
type MessageFeedback struct {
	ID           string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID     uint64    `json:"tenant_id" gorm:"not null;uniqueIndex:idx_message_feedback_actor"`
	SessionID    string    `json:"session_id" gorm:"type:varchar(36);not null;index"`
	MessageID    string    `json:"message_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_message_feedback_actor"`
	ActorID      string    `json:"actor_id" gorm:"type:varchar(512);not null;uniqueIndex:idx_message_feedback_actor"`
	Rating       int       `json:"rating" gorm:"not null"`
	ReasonCode   string    `json:"reason_code,omitempty" gorm:"type:varchar(64);not null;default:''"`
	ReasonDetail string    `json:"reason_detail,omitempty" gorm:"type:text;not null;default:''"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (m *MessageFeedback) BeforeCreate(_ *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

// MessageChunkReference is the durable answer-to-source snapshot used for
// attribution and session statistics.
type MessageChunkReference struct {
	ID              uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID        uint64    `json:"tenant_id" gorm:"not null;index"`
	SessionID       string    `json:"session_id" gorm:"type:varchar(36);not null;index"`
	MessageID       string    `json:"message_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_message_chunk_ref"`
	RequestID       string    `json:"request_id" gorm:"type:varchar(36);not null;default:''"`
	ChunkTenantID   uint64    `json:"chunk_tenant_id" gorm:"not null;index"`
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index"`
	KnowledgeID     string    `json:"knowledge_id" gorm:"type:varchar(36);not null;index"`
	ChunkID         string    `json:"chunk_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_message_chunk_ref;index"`
	CreatedAt       time.Time `json:"created_at"`
}

// MessageFeedbackAttribution materializes a message evaluation onto each
// referenced chunk. Keeping this separate allows an administrator to reset one
// chunk without erasing the user's evaluation from other cited chunks.
type MessageFeedbackAttribution struct {
	ID              uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	FeedbackID      string    `json:"feedback_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_feedback_chunk"`
	TenantID        uint64    `json:"tenant_id" gorm:"not null;index"`
	SessionID       string    `json:"session_id" gorm:"type:varchar(36);not null;index"`
	MessageID       string    `json:"message_id" gorm:"type:varchar(36);not null;index"`
	ChunkTenantID   uint64    `json:"chunk_tenant_id" gorm:"not null;index"`
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index"`
	KnowledgeID     string    `json:"knowledge_id" gorm:"type:varchar(36);not null;index"`
	ChunkID         string    `json:"chunk_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_feedback_chunk;index"`
	Rating          int       `json:"rating" gorm:"not null"`
	ReasonCode      string    `json:"reason_code,omitempty" gorm:"type:varchar(64);not null;default:''"`
	ReasonDetail    string    `json:"reason_detail,omitempty" gorm:"type:text;not null;default:''"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ChunkFeedbackWeightLog records every effective automatic or administrative
// weight transition.
type ChunkFeedbackWeightLog struct {
	ID               uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID         uint64    `json:"tenant_id" gorm:"not null;index"`
	KnowledgeBaseID  string    `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index"`
	ChunkID          string    `json:"chunk_id" gorm:"type:varchar(36);not null;index"`
	FeedbackID       string    `json:"feedback_id,omitempty" gorm:"type:varchar(36);not null;default:''"`
	ActorID          string    `json:"actor_id" gorm:"type:varchar(512);not null;default:''"`
	TriggerSource    string    `json:"trigger_source" gorm:"type:varchar(32);not null"`
	TriggerAction    string    `json:"trigger_action" gorm:"type:varchar(32);not null"`
	OldPositiveCount int64     `json:"old_positive_count" gorm:"not null"`
	NewPositiveCount int64     `json:"new_positive_count" gorm:"not null"`
	OldNegativeCount int64     `json:"old_negative_count" gorm:"not null"`
	NewNegativeCount int64     `json:"new_negative_count" gorm:"not null"`
	OldPositiveRate  float64   `json:"old_positive_rate" gorm:"not null"`
	NewPositiveRate  float64   `json:"new_positive_rate" gorm:"not null"`
	OldRecallWeight  float64   `json:"old_recall_weight" gorm:"not null"`
	NewRecallWeight  float64   `json:"new_recall_weight" gorm:"not null"`
	OldStatus        string    `json:"old_status" gorm:"type:varchar(32);not null"`
	NewStatus        string    `json:"new_status" gorm:"type:varchar(32);not null"`
	CreatedAt        time.Time `json:"created_at"`
}

// MessageFeedbackView is embedded into message history responses.
type MessageFeedbackView struct {
	Rating       int    `json:"rating"`
	ReasonCode   string `json:"reason_code,omitempty"`
	ReasonDetail string `json:"reason_detail,omitempty"`
}

type FeedbackReasonCount struct {
	ReasonCode string `json:"reason_code"`
	Count      int64  `json:"count"`
}

type ChunkFeedbackStats struct {
	ChunkID             string                `json:"chunk_id"`
	KnowledgeID         string                `json:"knowledge_id"`
	KnowledgeTitle      string                `json:"knowledge_title"`
	ChunkIndex          int                   `json:"chunk_index"`
	Content             string                `json:"content"`
	PositiveCount       int64                 `json:"positive_count"`
	NegativeCount       int64                 `json:"negative_count"`
	PositiveRate        float64               `json:"positive_rate"`
	RecallWeight        float64               `json:"recall_weight"`
	FeedbackStatus      string                `json:"feedback_status"`
	RelatedSessionCount int64                 `json:"related_session_count"`
	ReasonCounts        []FeedbackReasonCount `json:"reason_counts" gorm:"-"`
}

type ChunkFeedbackStatsFilter struct {
	Page            int
	PageSize        int
	MinPositiveRate *float64
	MaxPositiveRate *float64
	Status          string
	Keyword         string
	SortBy          string
	SortOrder       string
}

type ChunkFeedbackStatsPage struct {
	Data     []*ChunkFeedbackStats `json:"data"`
	Total    int64                 `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
}
