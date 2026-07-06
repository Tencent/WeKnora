package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// FeedbackType represents a user's feedback action on a chat answer.
type FeedbackType string

const (
	// FeedbackLike indicates the user thumbs-up the answer.
	FeedbackLike FeedbackType = "like"
	// FeedbackDislike indicates the user thumbs-down the answer.
	FeedbackDislike FeedbackType = "dislike"
	// FeedbackNone indicates the user cancelled a previous like/dislike.
	FeedbackNone FeedbackType = "none"
)

// MessageFeedback records a user's like/dislike/cancel action on a specific
// assistant message. One row per (user, message); toggling replaces the
// existing row. The feedback is attributed to every chunk cited by the
// message via the message_chunk_refs table.
type MessageFeedback struct {
	ID           string       `json:"id"             gorm:"type:varchar(36);primaryKey"`
	TenantID     uint64       `json:"tenant_id"      gorm:"index"`
	UserID       string       `json:"user_id"        gorm:"type:varchar(512);index"`
	SessionID    string       `json:"session_id"     gorm:"type:varchar(36);index"`
	MessageID    string       `json:"message_id"     gorm:"type:varchar(36);index"`
	FeedbackType FeedbackType `json:"feedback_type"  gorm:"type:varchar(16)"`
	Reason       string       `json:"reason,omitempty"        gorm:"type:varchar(64)"`
	ReasonDetail string       `json:"reason_detail,omitempty" gorm:"type:text"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

// BeforeCreate generates a UUID for new feedback records.
func (f *MessageFeedback) BeforeCreate(tx *gorm.DB) (err error) {
	if f.ID == "" {
		f.ID = uuid.New().String()
	}
	return nil
}

// TableName overrides GORM's default pluralization to match the migration table name.
func (MessageFeedback) TableName() string { return "message_feedback" }

// MessageChunkRef is a normalized link between an assistant message and a
// knowledge chunk it cited. Populated from the message's KnowledgeReferences
// JSON so stats queries can use plain SQL JOINs.
type MessageChunkRef struct {
	ID           string    `json:"id"            gorm:"type:varchar(36);primaryKey"`
	TenantID     uint64    `json:"tenant_id"     gorm:"index"`
	MessageID    string    `json:"message_id"    gorm:"type:varchar(36);index"`
	SessionID    string    `json:"session_id"    gorm:"type:varchar(36);index"`
	ChunkID      string    `json:"chunk_id"      gorm:"type:varchar(36);index"`
	KnowledgeID  string    `json:"knowledge_id"  gorm:"type:varchar(36)"`
	KBID         string    `json:"kb_id"         gorm:"type:varchar(36)"`
	CreatedAt    time.Time `json:"created_at"`
}

// BeforeCreate generates a UUID for new ref records.
func (r *MessageChunkRef) BeforeCreate(tx *gorm.DB) (err error) {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	return nil
}

// TableName overrides GORM's default pluralization to match the migration table name.
func (MessageChunkRef) TableName() string { return "message_chunk_refs" }

// WeightLogTriggerType describes what caused a chunk's recall weight to change.
type WeightLogTriggerType string

const (
	// WeightTriggerUserFeedback – weight adjusted automatically due to a
	// like/dislike action shifting the approval rate across a threshold.
	WeightTriggerUserFeedback WeightLogTriggerType = "user_feedback"
	// WeightTriggerAdminReset – an admin reset the chunk's feedback counters
	// and weight to defaults.
	WeightTriggerAdminReset WeightLogTriggerType = "admin_reset"
	// WeightTriggerAdminManual – an admin manually set the recall weight.
	WeightTriggerAdminManual WeightLogTriggerType = "admin_manual"
)

// ChunkWeightLog records every recall_weight change on a chunk, capturing
// the before/after state and the trigger source for auditability.
type ChunkWeightLog struct {
	ID                string                `json:"id"                  gorm:"type:varchar(36);primaryKey"`
	TenantID          uint64                `json:"tenant_id"           gorm:"index"`
	ChunkID           string                `json:"chunk_id"            gorm:"type:varchar(36);index"`
	OldWeight         float64               `json:"old_weight"`
	NewWeight         float64               `json:"new_weight"`
	OldApprovalRate   float64               `json:"old_approval_rate"`
	NewApprovalRate   float64               `json:"new_approval_rate"`
	OldLikeCount      int                   `json:"old_like_count"`
	NewLikeCount      int                   `json:"new_like_count"`
	OldDislikeCount   int                   `json:"old_dislike_count"`
	NewDislikeCount   int                   `json:"new_dislike_count"`
	TriggerType       WeightLogTriggerType  `json:"trigger_type"        gorm:"type:varchar(32)"`
	TriggerDetail     string                `json:"trigger_detail,omitempty" gorm:"type:text"`
	CreatedAt         time.Time             `json:"created_at"`
}

// BeforeCreate generates a UUID for new log records.
func (l *ChunkWeightLog) BeforeCreate(tx *gorm.DB) (err error) {
	if l.ID == "" {
		l.ID = uuid.New().String()
	}
	return nil
}

// TableName overrides GORM's default pluralization to match the migration table name.
func (ChunkWeightLog) TableName() string { return "chunk_weight_logs" }

// FeedbackThresholds holds the configurable thresholds for the automatic
// weight-adjustment mechanism. Loaded from system settings with sensible
// defaults.
type FeedbackThresholds struct {
	// BoostThreshold: approval_rate >= this value → weight boosted.
	BoostThreshold float64 `json:"boost_threshold"`
	// ReduceThreshold: approval_rate < this value → weight reduced.
	ReduceThreshold float64 `json:"reduce_threshold"`
	// OptimizeThreshold: approval_rate < this value → needs_optimization flag set.
	OptimizeThreshold float64 `json:"optimize_threshold"`
	// BoostWeight: the recall weight applied to high-quality chunks.
	BoostWeight float64 `json:"boost_weight"`
	// ReduceWeight: the recall weight applied to low-quality chunks.
	ReduceWeight float64 `json:"reduce_weight"`
	// MinFeedbackCount: minimum total feedback (likes + dislikes) before
	// weight adjustment kicks in. Avoids skewing new chunks with 1 dislike.
	MinFeedbackCount int `json:"min_feedback_count"`
}

// DefaultFeedbackThresholds returns the default weight-adjustment thresholds.
//   - Boost at >= 80% approval (weight 1.5)
//   - Reduce at < 50% approval (weight 0.5)
//   - Mark "needs optimization" at < 30% approval
//   - Requires at least 3 total feedbacks before adjusting
func DefaultFeedbackThresholds() *FeedbackThresholds {
	return &FeedbackThresholds{
		BoostThreshold:    0.8,
		ReduceThreshold:   0.5,
		OptimizeThreshold: 0.3,
		BoostWeight:       1.5,
		ReduceWeight:      0.5,
		MinFeedbackCount:  3,
	}
}

// ComputeWeight calculates the recall weight and needs_optimization flag
// for a chunk based on its approval rate and the given thresholds.
// Returns (weight, needsOptimization).
func ComputeWeight(likeCount, dislikeCount int, thresholds *FeedbackThresholds) (weight float64, needsOptimization bool) {
	if thresholds == nil {
		thresholds = DefaultFeedbackThresholds()
	}
	total := likeCount + dislikeCount
	if total < thresholds.MinFeedbackCount {
		return 1.0, false
	}
	approvalRate := 0.0
	if total > 0 {
		approvalRate = float64(likeCount) / float64(total)
	}
	needsOptimization = approvalRate < thresholds.OptimizeThreshold
	switch {
	case approvalRate >= thresholds.BoostThreshold:
		return thresholds.BoostWeight, needsOptimization
	case approvalRate < thresholds.ReduceThreshold:
		return thresholds.ReduceWeight, needsOptimization
	default:
		return 1.0, needsOptimization
	}
}

// ComputeApprovalRate returns like_count / (like_count + dislike_count),
// or 0 when total is 0.
func ComputeApprovalRate(likeCount, dislikeCount int) float64 {
	total := likeCount + dislikeCount
	if total == 0 {
		return 0
	}
	return float64(likeCount) / float64(total)
}

// FeedbackRequest is the API request body for submitting like/dislike/cancel
// feedback on a chat answer.
type FeedbackRequest struct {
	SessionID    string       `json:"session_id"`
	MessageID    string       `json:"message_id"`
	FeedbackType FeedbackType `json:"feedback_type"`
	Reason       string       `json:"reason,omitempty"`
	ReasonDetail string       `json:"reason_detail,omitempty"`
}

// ChunkFeedbackStats is the per-chunk aggregate used by the admin stats view.
type ChunkFeedbackStats struct {
	ChunkID            string  `json:"chunk_id"`
	KnowledgeID        string  `json:"knowledge_id"`
	KnowledgeBaseID    string  `json:"knowledge_base_id"`
	ChunkIndex         int     `json:"chunk_index"`
	ChunkType          string  `json:"chunk_type"`
	ContentPreview     string  `json:"content_preview"`
	LikeCount          int     `json:"like_count"`
	DislikeCount       int     `json:"dislike_count"`
	ApprovalRate       float64 `json:"approval_rate"`
	RecallWeight       float64 `json:"recall_weight"`
	NeedsOptimization  bool    `json:"needs_optimization"`
	SessionCount       int64   `json:"session_count"`
	FeedbackCount      int64   `json:"feedback_count"`
	DislikeReasons     []DislikeReasonCount `json:"dislike_reasons,omitempty"`
}

// DislikeReasonCount aggregates a single dislike reason code and its frequency.
type DislikeReasonCount struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}
