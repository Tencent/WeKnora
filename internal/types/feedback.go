package types

import (
	"fmt"
	"strings"
	"time"
)

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
	// FeedbackAt is the rating-event clock. Metadata-only updates must not
	// advance it, otherwise a pre-reset rating could become active again.
	FeedbackAt time.Time `json:"feedback_at" gorm:"not null"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// TableName returns the message feedback table name.
func (MessageFeedback) TableName() string { return "message_feedbacks" }

// MessageChunkReference stores immutable attribution from a message to a chunk.
type MessageChunkReference struct {
	ID              string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	MessageTenantID uint64    `json:"message_tenant_id" gorm:"not null;uniqueIndex:idx_msg_chunk_ref"`
	ChunkTenantID   uint64    `json:"chunk_tenant_id" gorm:"not null;uniqueIndex:idx_msg_chunk_ref"`
	MessageID       string    `json:"message_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_msg_chunk_ref"`
	ChunkID         string    `json:"chunk_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_msg_chunk_ref"`
	CreatedAt       time.Time `json:"created_at"`
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
	ChunkID               string                       `json:"chunk_id"`
	KnowledgeID           string                       `json:"knowledge_id"`
	KnowledgeBaseID       string                       `json:"knowledge_base_id"`
	KnowledgeTitle        string                       `json:"knowledge_title"`
	ChunkIndex            int                          `json:"chunk_index"`
	ChunkType             ChunkType                    `json:"chunk_type"`
	Content               string                       `json:"content"`
	ContentPreview        string                       `json:"content_preview"`
	ReasonCounts          map[FeedbackReasonCode]int64 `json:"reason_counts"`
	Audits                []*ChunkFeedbackAudit        `json:"audits"`
	LikeCount             int64                        `json:"like_count"`
	DislikeCount          int64                        `json:"dislike_count"`
	SessionCount          int64                        `json:"session_count"`
	PositiveRate          *float64                     `json:"positive_rate"`
	NeedsOptimization     bool                         `json:"needs_optimization"`
	StoredRecallWeight    float64                      `json:"stored_recall_weight"`
	EffectiveRecallWeight float64                      `json:"effective_recall_weight"`
	FeedbackResetAt       *time.Time                   `json:"feedback_reset_at"`
}

// Chunk feedback status filters accepted by the governance list.
const (
	ChunkFeedbackStatusAll     = "all"
	ChunkFeedbackStatusRated   = "rated"
	ChunkFeedbackStatusHigh    = "high"
	ChunkFeedbackStatusNormal  = "normal"
	ChunkFeedbackStatusLow     = "low"
	ChunkFeedbackStatusUnrated = "unrated"
)

// ChunkFeedbackListQuery is the validated, KB-scoped governance list query.
// Sort names are mapped to fixed SQL expressions by the repository.
type ChunkFeedbackListQuery struct {
	Page              int    `form:"page"`
	PageSize          int    `form:"page_size"`
	Keyword           string `form:"keyword"`
	FeedbackStatus    string `form:"feedback_status"`
	NeedsOptimization *bool  `form:"needs_optimization"`
	SortBy            string `form:"sort_by"`
	SortOrder         string `form:"sort_order"`
}

// Validate normalizes and validates the governance list query.
func (q *ChunkFeedbackListQuery) Validate() error {
	if q == nil {
		return fmt.Errorf("chunk feedback query is required")
	}
	if q.Page == 0 {
		q.Page = 1
	}
	if q.PageSize == 0 {
		q.PageSize = 20
	}
	if q.Page < 1 || q.PageSize < 1 || q.PageSize > 100 {
		return fmt.Errorf("page must be positive and page_size must be between 1 and 100")
	}
	q.Keyword = strings.TrimSpace(q.Keyword)
	q.FeedbackStatus = strings.ToLower(strings.TrimSpace(q.FeedbackStatus))
	if q.FeedbackStatus == "" {
		q.FeedbackStatus = ChunkFeedbackStatusAll
	}
	switch q.FeedbackStatus {
	case ChunkFeedbackStatusAll, ChunkFeedbackStatusRated, ChunkFeedbackStatusHigh,
		ChunkFeedbackStatusNormal, ChunkFeedbackStatusLow, ChunkFeedbackStatusUnrated:
	default:
		return fmt.Errorf("invalid feedback_status")
	}
	q.SortBy = strings.ToLower(strings.TrimSpace(q.SortBy))
	if q.SortBy == "" {
		q.SortBy = "updated_at"
	}
	switch q.SortBy {
	case "updated_at", "like_count", "dislike_count", "positive_rate",
		"stored_recall_weight", "effective_recall_weight", "chunk_index":
	default:
		return fmt.Errorf("invalid sort_by")
	}
	q.SortOrder = strings.ToLower(strings.TrimSpace(q.SortOrder))
	if q.SortOrder == "" {
		q.SortOrder = "desc"
	}
	if q.SortOrder != "asc" && q.SortOrder != "desc" {
		return fmt.Errorf("invalid sort_order")
	}
	return nil
}

// Pagination returns the normalized shared pagination request.
func (q *ChunkFeedbackListQuery) Pagination() *Pagination {
	return &Pagination{Page: q.Page, PageSize: q.PageSize}
}

// ChunkFeedbackListItem is a governance-only projection. Stored and
// effective weights are named separately because only the latter follows the
// current runtime policy.
type ChunkFeedbackListItem struct {
	ChunkID               string     `json:"chunk_id"`
	KnowledgeID           string     `json:"knowledge_id"`
	KnowledgeTitle        string     `json:"knowledge_title"`
	ChunkIndex            int        `json:"chunk_index"`
	ChunkType             ChunkType  `json:"chunk_type"`
	ContentPreview        string     `json:"content_preview"`
	LikeCount             int64      `json:"like_count"`
	DislikeCount          int64      `json:"dislike_count"`
	SessionCount          int64      `json:"session_count"`
	PositiveRate          *float64   `json:"positive_rate"`
	StoredRecallWeight    float64    `json:"stored_recall_weight"`
	EffectiveRecallWeight float64    `json:"effective_recall_weight"`
	NeedsOptimization     bool       `json:"needs_optimization"`
	FeedbackResetAt       *time.Time `json:"feedback_reset_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

// ChunkFeedbackScope is the complete identity used for a batched retrieval
// policy lookup. Chunk IDs alone are never trusted across workspaces or KBs.
type ChunkFeedbackScope struct {
	TenantID        uint64
	KnowledgeBaseID string
	ChunkID         string
}

// ChunkFeedbackStat is the current projection used to derive an effective
// retrieval weight under the active runtime policy.
type ChunkFeedbackStat struct {
	ChunkFeedbackScope
	LikeCount          int64
	DislikeCount       int64
	StoredRecallWeight float64
}
