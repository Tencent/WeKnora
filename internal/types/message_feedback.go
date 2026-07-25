package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type FeedbackAction string

const (
	FeedbackActionLike    FeedbackAction = "like"
	FeedbackActionDislike FeedbackAction = "dislike"
)

func (a FeedbackAction) Valid() bool {
	return a == FeedbackActionLike || a == FeedbackActionDislike
}

type MessageChunkReference struct {
	ID              string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64         `json:"tenant_id" gorm:"index"`
	SessionID       string         `json:"session_id" gorm:"type:varchar(36);index"`
	MessageID       string         `json:"message_id" gorm:"type:varchar(36);index"`
	ChunkID         string         `json:"chunk_id" gorm:"type:varchar(36);index"`
	KnowledgeID     string         `json:"knowledge_id" gorm:"type:varchar(36)"`
	KnowledgeBaseID string         `json:"knowledge_base_id" gorm:"type:varchar(36)"`
	CreatedAt       time.Time      `json:"created_at"`
	DeletedAt       gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

func (r *MessageChunkReference) BeforeCreate(tx *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	return nil
}

type MessageFeedback struct {
	ID        string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID  uint64         `json:"tenant_id" gorm:"index"`
	SessionID string         `json:"session_id" gorm:"type:varchar(36);index"`
	MessageID string         `json:"message_id" gorm:"type:varchar(36);index"`
	UserID    string         `json:"user_id" gorm:"type:varchar(512);index"`
	Action    FeedbackAction `json:"action" gorm:"type:varchar(16)"`
	Reason    string         `json:"reason" gorm:"type:text;default:''"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

func (f *MessageFeedback) BeforeCreate(tx *gorm.DB) error {
	if f.ID == "" {
		f.ID = uuid.New().String()
	}
	return nil
}

type MessageFeedbackRequest struct {
	Action FeedbackAction `json:"action" binding:"required"`
	Reason string         `json:"reason" binding:"max=1000"`
}

type MessageFeedbackResponse struct {
	Feedback *MessageFeedback `json:"feedback,omitempty"`
}
