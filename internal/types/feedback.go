package types

import "time"

// FeedbackType is the state of a user's feedback on one assistant message.
type FeedbackType string

const (
	// FeedbackTypeLike records positive feedback.
	FeedbackTypeLike FeedbackType = "like"
	// FeedbackTypeDislike records negative feedback.
	FeedbackTypeDislike FeedbackType = "dislike"
	// FeedbackTypeNone cancels the user's existing feedback.
	FeedbackTypeNone FeedbackType = "none"
)

// FeedbackReasonCode classifies optional negative-feedback reasons.
type FeedbackReasonCode string

const (
	// FeedbackReasonInaccurate marks factually incorrect answers.
	FeedbackReasonInaccurate FeedbackReasonCode = "inaccurate"
	// FeedbackReasonIrrelevant marks answers unrelated to the question.
	FeedbackReasonIrrelevant FeedbackReasonCode = "irrelevant"
	// FeedbackReasonIncomplete marks answers missing important information.
	FeedbackReasonIncomplete FeedbackReasonCode = "incomplete"
	// FeedbackReasonOutdated marks answers based on stale information.
	FeedbackReasonOutdated FeedbackReasonCode = "outdated"
	// FeedbackReasonOther marks feedback outside the predefined categories.
	FeedbackReasonOther FeedbackReasonCode = "other"
)

// ChunkFeedbackAuditAction identifies a feedback governance event.
type ChunkFeedbackAuditAction string

const (
	// ChunkFeedbackAuditActionWeightChanged records a derived weight change.
	ChunkFeedbackAuditActionWeightChanged ChunkFeedbackAuditAction = "feedback_weight_changed"
	// ChunkFeedbackAuditActionReset records an administrator reset.
	ChunkFeedbackAuditActionReset ChunkFeedbackAuditAction = "feedback_reset"
)

// FeedbackTriggerSource explains what caused a chunk projection update.
type FeedbackTriggerSource string

const (
	// FeedbackTriggerLike identifies a positive-feedback update.
	FeedbackTriggerLike FeedbackTriggerSource = "like"
	// FeedbackTriggerDislike identifies a negative-feedback update.
	FeedbackTriggerDislike FeedbackTriggerSource = "dislike"
	// FeedbackTriggerCancel identifies cancellation of prior feedback.
	FeedbackTriggerCancel FeedbackTriggerSource = "cancel"
	// FeedbackTriggerAdminReset identifies an administrator reset.
	FeedbackTriggerAdminReset FeedbackTriggerSource = "admin_reset"
	// FeedbackTriggerContentDelete identifies a lifecycle deletion.
	FeedbackTriggerContentDelete FeedbackTriggerSource = "content_delete"
	// FeedbackTriggerLegacy identifies events without a more specific source.
	FeedbackTriggerLegacy FeedbackTriggerSource = "legacy"
)

// MessageFeedback stores one user's current feedback state for a message.
type MessageFeedback struct {
	ID           string              `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID     uint64              `json:"tenant_id" gorm:"not null;uniqueIndex:idx_feedback_actor"`
	UserID       string              `json:"user_id" gorm:"type:varchar(64);not null;uniqueIndex:idx_feedback_actor"`
	SessionID    string              `json:"session_id" gorm:"type:varchar(36);not null"`
	MessageID    string              `json:"message_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_feedback_actor"`
	FeedbackType FeedbackType        `json:"type" gorm:"column:feedback_type;type:varchar(16);not null"`
	ReasonCode   *FeedbackReasonCode `json:"reason_code,omitempty" gorm:"type:varchar(16)"`
	CreatedAt    time.Time           `json:"created_at"`
	UpdatedAt    time.Time           `json:"updated_at"`
}

// TableName returns the message feedback table name.
func (MessageFeedback) TableName() string { return "message_feedbacks" }

// MessageChunkReference stores immutable attribution from a message to a chunk.
type MessageChunkReference struct {
	ID                   string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	MessageTenantID      uint64    `json:"message_tenant_id" gorm:"not null;uniqueIndex:idx_ref"`
	ChunkTenantID        uint64    `json:"chunk_tenant_id" gorm:"not null;uniqueIndex:idx_ref"`
	ChunkKnowledgeBaseID string    `json:"chunk_knowledge_base_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_ref"`
	MessageID            string    `json:"message_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_ref"`
	ChunkID              string    `json:"chunk_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_ref"`
	CreatedAt            time.Time `json:"created_at"`
}

// TableName returns the message-to-chunk attribution table name.
func (MessageChunkReference) TableName() string { return "message_chunk_references" }

// ChunkFeedbackAudit records derived-weight changes and governance resets.
type ChunkFeedbackAudit struct {
	ID            uint64                   `json:"id" gorm:"primaryKey;autoIncrement"`
	ChunkTenantID uint64                   `json:"chunk_tenant_id" gorm:"not null;index:idx_feedback_audit"`
	ChunkID       string                   `json:"chunk_id" gorm:"type:varchar(36);not null;index:idx_feedback_audit"`
	ActorTenantID uint64                   `json:"-" gorm:"not null"`
	ActorUserID   string                   `json:"-" gorm:"type:varchar(64);not null"`
	Action        ChunkFeedbackAuditAction `json:"action" gorm:"type:varchar(32);not null"`
	TriggerSource FeedbackTriggerSource    `json:"trigger_source" gorm:"type:varchar(16);not null;default:legacy"`
	OldWeight     float64                  `json:"old_weight" gorm:"not null"`
	NewWeight     float64                  `json:"new_weight" gorm:"not null"`
	CreatedAt     time.Time                `json:"created_at"`
}

// TableName returns the chunk feedback audit table name.
func (ChunkFeedbackAudit) TableName() string { return "chunk_feedback_audits" }

// MessageFeedbackState is the feedback state returned to the current user.
type MessageFeedbackState struct {
	Type       FeedbackType        `json:"type"`
	ReasonCode *FeedbackReasonCode `json:"reason_code,omitempty"`
}

// ApplyMessageFeedbackInput carries server-authorized feedback mutation data.
type ApplyMessageFeedbackInput struct {
	MessageTenantID uint64
	ActorTenantID   uint64
	ActorUserID     string
	SessionID       string
	MessageID       string
	Type            FeedbackType
	ReasonCode      *FeedbackReasonCode
}

// ResetChunkFeedbackInput carries server-authorized chunk reset data.
type ResetChunkFeedbackInput struct {
	ChunkTenantID   uint64
	ActorTenantID   uint64
	ActorUserID     string
	KnowledgeBaseID string
	ChunkID         string
}

// ChunkFeedbackDetails contains governance reason counts and recent audits.
type ChunkFeedbackDetails struct {
	ReasonCounts map[FeedbackReasonCode]int64 `json:"reason_counts"`
	Audits       []*ChunkFeedbackAudit        `json:"audits"`
}
