package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ChunkFeedbackRating is the rating a user can attach to an AI answer.
type ChunkFeedbackRating string

const (
	// ChunkFeedbackLike marks an answer as helpful.
	ChunkFeedbackLike ChunkFeedbackRating = "like"
	// ChunkFeedbackDislike marks an answer as unhelpful.
	ChunkFeedbackDislike ChunkFeedbackRating = "dislike"
)

// Valid returns true when the rating is a known value.
func (r ChunkFeedbackRating) Valid() bool {
	return r == ChunkFeedbackLike || r == ChunkFeedbackDislike
}

// MessageChunkLink records that an assistant message referenced a knowledge
// base chunk. It is the join table between Q&A answers and the knowledge
// chunks they cited, so user feedback on an answer can be attributed back to
// every chunk that contributed to it.
type MessageChunkLink struct {
	ID              string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64         `json:"tenant_id" gorm:"index"`
	SessionID       string         `json:"session_id" gorm:"type:varchar(36);index"`
	MessageID       string         `json:"message_id" gorm:"type:varchar(36);uniqueIndex:idx_message_chunk_links_message_chunk"`
	ChunkID         string         `json:"chunk_id" gorm:"type:varchar(36);uniqueIndex:idx_message_chunk_links_message_chunk"`
	ChunkSeqID      int64          `json:"chunk_seq_id" gorm:"type:bigint;default:0"`
	KnowledgeID     string         `json:"knowledge_id" gorm:"type:varchar(36);index"`
	KnowledgeBaseID string         `json:"knowledge_base_id" gorm:"type:varchar(36);index"`
	KnowledgeTitle  string         `json:"knowledge_title" gorm:"type:varchar(512);default:''"`
	ChunkContent    string         `json:"chunk_content" gorm:"type:text"`
	CreatedAt       time.Time      `json:"created_at"`
	DeletedAt       gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

// TableName pins the schema to migration 000079.
func (MessageChunkLink) TableName() string { return "message_chunk_links" }

// BeforeCreate generates a UUID primary key.
func (l *MessageChunkLink) BeforeCreate(_ *gorm.DB) error {
	l.ID = uuid.New().String()
	return nil
}

// ChunkFeedbackRecord is one user's rating for one assistant message. The
// rating is attributed to all chunks linked to the message.
type ChunkFeedbackRecord struct {
	ID        string `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID  uint64 `json:"tenant_id" gorm:"index"`
	UserID    string `json:"user_id" gorm:"type:varchar(36);uniqueIndex:idx_chunk_feedback_records_user_message"`
	SessionID string `json:"session_id" gorm:"type:varchar(36);index"`
	MessageID string `json:"message_id" gorm:"type:varchar(36);uniqueIndex:idx_chunk_feedback_records_user_message"`
	// Rating is "like" or "dislike".
	Rating string `json:"rating" gorm:"type:varchar(16);not null"`
	// Reason is the user-selected dislike reason (empty for likes).
	Reason    string         `json:"reason" gorm:"type:varchar(512);default:''"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

// TableName pins the schema to migration 000079.
func (ChunkFeedbackRecord) TableName() string { return "chunk_feedback_records" }

// BeforeCreate generates a UUID primary key.
func (r *ChunkFeedbackRecord) BeforeCreate(_ *gorm.DB) error {
	r.ID = uuid.New().String()
	return nil
}

// ChunkWeightLogSource describes what triggered a recall-weight change.
type ChunkWeightLogSource string

const (
	// ChunkWeightLogSourceFeedback means the change was derived from user likes/dislikes.
	ChunkWeightLogSourceFeedback ChunkWeightLogSource = "feedback"
	// ChunkWeightLogSourceManualReset means an admin reset the chunk feedback/weight.
	ChunkWeightLogSourceManualReset ChunkWeightLogSource = "manual_reset"
	// ChunkWeightLogSourceManualAdjust means an admin manually adjusted the weight.
	ChunkWeightLogSourceManualAdjust ChunkWeightLogSource = "manual_adjust"
)

// ChunkWeightLog audits every recall-weight change so admins can trace the
// trigger source (user feedback vs manual reset) for a chunk.
type ChunkWeightLog struct {
	ID              string  `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64  `json:"tenant_id" gorm:"index"`
	ChunkID         string  `json:"chunk_id" gorm:"type:varchar(36);index"`
	KnowledgeBaseID string  `json:"knowledge_base_id" gorm:"type:varchar(36);index"`
	OldWeight       float64 `json:"old_weight" gorm:"not null;default:1"`
	NewWeight       float64 `json:"new_weight" gorm:"not null;default:1"`
	// Source is feedback | manual_reset | manual_adjust.
	Source    string    `json:"source" gorm:"type:varchar(24);not null"`
	MessageID string    `json:"message_id" gorm:"type:varchar(36);default:''"`
	UserID    string    `json:"user_id" gorm:"type:varchar(36);default:''"`
	Reason    string    `json:"reason" gorm:"type:varchar(512);default:''"`
	CreatedAt time.Time `json:"created_at" gorm:"index"`
}

// TableName pins the schema to migration 000079.
func (ChunkWeightLog) TableName() string { return "chunk_weight_logs" }

// BeforeCreate generates a UUID primary key.
func (l *ChunkWeightLog) BeforeCreate(_ *gorm.DB) error {
	l.ID = uuid.New().String()
	return nil
}

// ChunkFeedbackConfig holds the tunable thresholds of the automatic recall
// weight adjustment mechanism. It is tenant-scoped with sensible defaults:
//   - BoostThreshold: approval rate >= this value raises the recall weight.
//   - DegradeThreshold: approval rate < this value lowers the recall weight.
//   - OptimizeThreshold: approval rate < this value marks the chunk as
//     "needs optimization" for manual review.
//   - MinVotes: minimum number of ratings before weight auto-adjustment kicks in.
//   - WeightStep / MaxWeight / MinWeight bound each adjustment.
type ChunkFeedbackConfig struct {
	TenantID          uint64    `json:"tenant_id" gorm:"primaryKey"`
	BoostThreshold    float64   `json:"boost_threshold" gorm:"not null;default:0.8"`
	DegradeThreshold  float64   `json:"degrade_threshold" gorm:"not null;default:0.5"`
	OptimizeThreshold float64   `json:"optimize_threshold" gorm:"not null;default:0.4"`
	MinVotes          int64     `json:"min_votes" gorm:"not null;default:1"`
	WeightStep        float64   `json:"weight_step" gorm:"not null;default:0.1"`
	MaxWeight         float64   `json:"max_weight" gorm:"not null;default:2.0"`
	MinWeight         float64   `json:"min_weight" gorm:"not null;default:0.1"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// TableName pins the schema to migration 000079.
func (ChunkFeedbackConfig) TableName() string { return "chunk_feedback_configs" }

// DefaultChunkFeedbackConfig returns the built-in defaults for a tenant.
func DefaultChunkFeedbackConfig(tenantID uint64) *ChunkFeedbackConfig {
	return &ChunkFeedbackConfig{
		TenantID:          tenantID,
		BoostThreshold:    0.8,
		DegradeThreshold:  0.5,
		OptimizeThreshold: 0.4,
		MinVotes:          1,
		WeightStep:        0.1,
		MaxWeight:         2.0,
		MinWeight:         0.1,
	}
}

// DislikeReasonStat aggregates one dislike reason across feedback records.
type DislikeReasonStat struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}

// RelatedSessionStat summarises a chat session that cited a chunk.
type RelatedSessionStat struct {
	SessionID    string     `json:"session_id"`
	Title        string     `json:"title"`
	MessageCount int64      `json:"message_count"`
	LastActiveAt *time.Time `json:"last_active_at,omitempty"`
}

// ChunkFeedbackStat is the per-chunk feedback aggregation row shown in the
// admin console.
type ChunkFeedbackStat struct {
	ChunkID           string  `json:"chunk_id"`
	ChunkSeqID        int64   `json:"chunk_seq_id"`
	KnowledgeID       string  `json:"knowledge_id"`
	KnowledgeBaseID   string  `json:"knowledge_base_id"`
	KnowledgeTitle    string  `json:"knowledge_title"`
	KnowledgeFileName string  `json:"knowledge_filename"`
	ChunkIndex        int     `json:"chunk_index"`
	ContentPreview    string  `json:"content_preview"`
	ChunkType         string  `json:"chunk_type"`
	LikeCount         int64   `json:"like_count"`
	DislikeCount      int64   `json:"dislike_count"`
	ApprovalRate      float64 `json:"approval_rate"`
	RecallWeight      float64 `json:"recall_weight"`
	NeedsOptimization bool    `json:"needs_optimization"`
	// SessionCount is the number of distinct chat sessions whose answers cited this chunk.
	SessionCount int64 `json:"session_count"`
	// FeedbackCount is the number of rating records attributed to this chunk.
	FeedbackCount int64 `json:"feedback_count"`
	// UpdatedAt reflects the chunk row update time.
	UpdatedAt time.Time `json:"updated_at"`
}

// ChunkFeedbackDetail extends the stat row with dislike reason aggregation and
// the related chat sessions.
type ChunkFeedbackDetail struct {
	ChunkFeedbackStat
	DislikeReasons  []DislikeReasonStat  `json:"dislike_reasons"`
	RelatedSessions []RelatedSessionStat `json:"related_sessions"`
}
