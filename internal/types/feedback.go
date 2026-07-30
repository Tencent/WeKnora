package types

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type FeedbackType string

const (
	FeedbackTypeLike    FeedbackType = "like"
	FeedbackTypeDislike FeedbackType = "dislike"
	FeedbackTypeNone    FeedbackType = "none"
)

type MessageFeedback struct {
	Type      FeedbackType `json:"type"`
	Reason    string       `json:"reason,omitempty"`
	CreatedAt time.Time    `json:"created_at,omitempty"`
	ChunkIDs  []string     `json:"chunk_ids,omitempty"`
}

func (f *MessageFeedback) Value() (driver.Value, error) {
	if f == nil {
		return json.Marshal(MessageFeedback{})
	}
	return json.Marshal(f)
}

func (f *MessageFeedback) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return nil
	}
	return json.Unmarshal(b, f)
}

type MessageChunkLink struct {
	ID              string    `json:"id"                gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64    `json:"tenant_id"`
	MessageID       string    `json:"message_id"`
	ChunkID         string    `json:"chunk_id"`
	KnowledgeID     string    `json:"knowledge_id"`
	KnowledgeBaseID string    `json:"knowledge_base_id"`
	CreatedAt       time.Time `json:"created_at"`
}

func (m *MessageChunkLink) BeforeCreate(tx *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	return nil
}

type ChunkWeightLog struct {
	ID              string    `json:"id"                gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64    `json:"tenant_id"`
	ChunkID         string    `json:"chunk_id"`
	KnowledgeBaseID string    `json:"knowledge_base_id"`
	KnowledgeID     string    `json:"knowledge_id"`
	OldWeight       float64   `json:"old_weight"`
	NewWeight       float64   `json:"new_weight"`
	Reason          string    `json:"reason"`
	Operator        string    `json:"operator"`
	CreatedAt       time.Time `json:"created_at"`
}

func (c *ChunkWeightLog) BeforeCreate(tx *gorm.DB) error {
	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	return nil
}

type FeedbackConfig struct {
	LikeRateBoostThreshold     float64 `json:"like_rate_boost_threshold"`
	LikeRatePenaltyThreshold   float64 `json:"like_rate_penalty_threshold"`
	WeightBoostFactor          float64 `json:"weight_boost_factor"`
	WeightPenaltyFactor        float64 `json:"weight_penalty_factor"`
	MinFeedbackCount           int64   `json:"min_feedback_count"`
	LowQualityDislikeThreshold int64   `json:"low_quality_dislike_threshold"`
	LowQualityRate             float64 `json:"low_quality_rate"`
}

func DefaultFeedbackConfig() *FeedbackConfig {
	return &FeedbackConfig{
		LikeRateBoostThreshold:     0.8,
		LikeRatePenaltyThreshold:   0.3,
		WeightBoostFactor:          1.2,
		WeightPenaltyFactor:        0.8,
		MinFeedbackCount:           5,
		LowQualityDislikeThreshold: 3,
		LowQualityRate:             0.2,
	}
}

type ChunkFeedbackStats struct {
	ChunkID        string  `json:"chunk_id"`
	LikeCount      int64   `json:"like_count"`
	DislikeCount   int64   `json:"dislike_count"`
	LikeRate       float64 `json:"like_rate"`
	RecallWeight   float64 `json:"recall_weight"`
	Content        string  `json:"content,omitempty"`
	KnowledgeID    string  `json:"knowledge_id,omitempty"`
	KnowledgeTitle string  `json:"knowledge_title,omitempty"`
}
