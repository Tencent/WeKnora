package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// ErrFeedbackRecomputeStale aborts a weight recomputation whose source
// retrieval config changed while the recomputation was running.
var ErrFeedbackRecomputeStale = errors.New("feedback weight recompute aborted: retrieval config changed")

const (
	// FeedbackRatingLike marks a stored thumbs-up rating.
	FeedbackRatingLike = "like"
	// FeedbackRatingDislike marks a stored thumbs-down rating.
	FeedbackRatingDislike = "dislike"
	// FeedbackRatingNone is a request-only value that cancels an existing
	// rating; it is never persisted.
	FeedbackRatingNone = "none"

	// FeedbackWeightTriggerFeedback marks a weight change caused by a user rating.
	FeedbackWeightTriggerFeedback = "feedback"
	// FeedbackWeightTriggerReset marks a weight change caused by an admin reset.
	FeedbackWeightTriggerReset = "reset"
	// FeedbackWeightTriggerConfig marks a weight change caused by a retrieval
	// config update recomputation.
	FeedbackWeightTriggerConfig = "config"
)

// FeedbackDislikeReasons is the whitelist of preset dislike reason codes
// accepted from clients. Labels are rendered client-side via i18n.
var FeedbackDislikeReasons = map[string]bool{
	"inaccurate": true,
	"incomplete": true,
	"irrelevant": true,
	"outdated":   true,
	"other":      true,
}

// FeedbackReasons stores preset dislike reason codes as a JSON array.
type FeedbackReasons []string

func (r FeedbackReasons) Value() (driver.Value, error) {
	if r == nil {
		r = FeedbackReasons{}
	}
	return json.Marshal(r)
}

func (r *FeedbackReasons) Scan(value interface{}) error {
	if value == nil {
		*r = FeedbackReasons{}
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		*r = FeedbackReasons{}
		return nil
	}
	return json.Unmarshal(b, r)
}

// MessageFeedback is one user's rating of one assistant message.
// TenantID is the evaluator's tenant; chunk attribution goes through
// MessageChunkReference rows keyed by MessageID.
type MessageFeedback struct {
	ID        string          `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID  uint64          `json:"tenant_id" gorm:"not null;index:idx_message_feedbacks_tenant_session"`
	SessionID string          `json:"session_id" gorm:"type:varchar(36);not null;index:idx_message_feedbacks_tenant_session"`
	MessageID string          `json:"message_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_message_feedbacks_message_user"`
	UserID    string          `json:"-" gorm:"type:varchar(512);not null;default:'';uniqueIndex:idx_message_feedbacks_message_user"`
	Rating    string          `json:"rating" gorm:"type:varchar(16);not null"`
	Reasons   FeedbackReasons `json:"reasons" gorm:"type:json;not null"`
	Comment   string          `json:"comment" gorm:"type:text;not null;default:''"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// TableName returns the table name for the MessageFeedback model
func (MessageFeedback) TableName() string {
	return "message_feedbacks"
}

// MessageChunkReference records that an assistant message cited a chunk.
// Written when the answer completes, independently from any rating, so
// per-chunk session stats count every citing answer.
type MessageChunkReference struct {
	ID              uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	MessageID       string    `json:"message_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_msg_chunk_refs_message_chunk"`
	SessionID       string    `json:"session_id" gorm:"type:varchar(36);not null"`
	ChunkID         string    `json:"chunk_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_msg_chunk_refs_message_chunk;index"`
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index"`
	CreatedAt       time.Time `json:"created_at"`
}

// TableName returns the table name for the MessageChunkReference model
func (MessageChunkReference) TableName() string {
	return "message_chunk_references"
}

// ChunkWeightLog is one recall-weight change of one chunk, with the trigger
// that caused it. TenantID is the chunk owner's tenant.
type ChunkWeightLog struct {
	ID              uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID        uint64    `json:"tenant_id" gorm:"not null"`
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index:idx_chunk_weight_logs_kb_chunk"`
	ChunkID         string    `json:"chunk_id" gorm:"type:varchar(36);not null;index:idx_chunk_weight_logs_kb_chunk"`
	OldWeight       float64   `json:"old_weight" gorm:"not null"`
	NewWeight       float64   `json:"new_weight" gorm:"not null"`
	PositiveRate    float64   `json:"positive_rate" gorm:"not null"`
	TriggerSource   string    `json:"trigger_source" gorm:"column:trigger_source;type:varchar(16);not null"`
	FeedbackID      string    `json:"feedback_id,omitempty" gorm:"type:varchar(36)"`
	CreatedAt       time.Time `json:"created_at"`
}

// TableName returns the table name for the ChunkWeightLog model
func (ChunkWeightLog) TableName() string {
	return "chunk_weight_logs"
}

// ChunkFeedbackStat is one row of the per-chunk feedback statistics surface.
type ChunkFeedbackStat struct {
	ChunkID           string         `json:"chunk_id"`
	KnowledgeID       string         `json:"knowledge_id"`
	KnowledgeTitle    string         `json:"knowledge_title"`
	ContentPreview    string         `json:"content_preview"`
	LikeCount         int            `json:"like_count"`
	DislikeCount      int            `json:"dislike_count"`
	Total             int            `json:"total"`
	PositiveRate      float64        `json:"positive_rate"`
	RecallWeight      float64        `json:"recall_weight"`
	NeedsOptimization bool           `json:"needs_optimization"`
	DislikeReasons    map[string]int `json:"dislike_reasons"`
	SessionCount      int            `json:"session_count"`
}

// FeedbackCounterDeltas maps an (old, new) rating transition to the like /
// dislike counter deltas to apply. Ratings are "", "like" or "dislike";
// oldEffective must already account for the feedback epoch (a rating from
// before the KB's feedback_reset_at is passed as "").
func FeedbackCounterDeltas(oldEffective, newRating string) (likeDelta, dislikeDelta int) {
	if oldEffective == newRating {
		return 0, 0
	}
	switch oldEffective {
	case FeedbackRatingLike:
		likeDelta--
	case FeedbackRatingDislike:
		dislikeDelta--
	}
	switch newRating {
	case FeedbackRatingLike:
		likeDelta++
	case FeedbackRatingDislike:
		dislikeDelta++
	}
	return likeDelta, dislikeDelta
}

// ComputeRecallWeight derives a chunk's recall weight and needs-optimization
// flag from its cumulative counters. Chunks with fewer than the minimum
// sample count keep the neutral weight so a single early rating cannot swing
// retrieval.
func ComputeRecallWeight(likeCount, dislikeCount int, cfg *RetrievalConfig) (weight float64, needsOptimization bool) {
	total := likeCount + dislikeCount
	if total < cfg.GetEffectiveFeedbackMinSamples() {
		return 1.0, false
	}
	rate := float64(likeCount) / float64(total)
	switch {
	case rate >= cfg.GetEffectiveFeedbackBoostThreshold():
		weight = cfg.GetEffectiveFeedbackBoostFactor()
	case rate < cfg.GetEffectiveFeedbackPenaltyThreshold():
		weight = cfg.GetEffectiveFeedbackPenaltyFactor()
	default:
		weight = 1.0
	}
	return weight, rate < cfg.GetEffectiveFeedbackNeedsOptimizationThreshold()
}

// PositiveRateOf returns like/(like+dislike), or 0 when there are no ratings.
func PositiveRateOf(likeCount, dislikeCount int) float64 {
	total := likeCount + dislikeCount
	if total == 0 {
		return 0
	}
	return float64(likeCount) / float64(total)
}
