package types

import (
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	ChunkFeedbackSourceUserFeedback = "user_feedback"
	ChunkFeedbackSourceAdminReset   = "admin_reset"
)

type ChunkFeedbackConfig struct {
	HighPositiveRateThreshold float64 `json:"high_positive_rate_threshold"`
	LowPositiveRateThreshold  float64 `json:"low_positive_rate_threshold"`
	OptimizeRateThreshold     float64 `json:"optimize_rate_threshold"`
	HighRecallWeight          float64 `json:"high_recall_weight"`
	DefaultRecallWeight       float64 `json:"default_recall_weight"`
	LowRecallWeight           float64 `json:"low_recall_weight"`
}

func DefaultChunkFeedbackConfig() ChunkFeedbackConfig {
	return ChunkFeedbackConfig{
		HighPositiveRateThreshold: envPercent("WEKNORA_CHUNK_FEEDBACK_HIGH_RATE_PERCENT", 0.8),
		LowPositiveRateThreshold:  envPercent("WEKNORA_CHUNK_FEEDBACK_LOW_RATE_PERCENT", 0.5),
		OptimizeRateThreshold:     envPercent("WEKNORA_CHUNK_FEEDBACK_OPTIMIZE_RATE_PERCENT", 0.3),
		HighRecallWeight:          envPercent("WEKNORA_CHUNK_FEEDBACK_HIGH_WEIGHT_PERCENT", 1.2),
		DefaultRecallWeight:       envPercent("WEKNORA_CHUNK_FEEDBACK_DEFAULT_WEIGHT_PERCENT", 1.0),
		LowRecallWeight:           envPercent("WEKNORA_CHUNK_FEEDBACK_LOW_WEIGHT_PERCENT", 0.8),
	}
}

func envPercent(key string, fallback float64) float64 {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return value / 100
}

func (c ChunkFeedbackConfig) RecallWeight(rate *float64) float64 {
	if rate == nil {
		return c.DefaultRecallWeight
	}
	if *rate >= c.HighPositiveRateThreshold {
		return c.HighRecallWeight
	}
	if *rate < c.LowPositiveRateThreshold {
		return c.LowRecallWeight
	}
	return c.DefaultRecallWeight
}

func (c ChunkFeedbackConfig) NeedsOptimization(rate *float64) bool {
	return rate != nil && *rate < c.OptimizeRateThreshold
}

type ChunkFeedbackWeightLog struct {
	ID              string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64         `json:"tenant_id" gorm:"index"`
	ChunkID         string         `json:"chunk_id" gorm:"type:varchar(36);index"`
	KnowledgeID     string         `json:"knowledge_id" gorm:"type:varchar(36);index"`
	KnowledgeBaseID string         `json:"knowledge_base_id" gorm:"type:varchar(36);index"`
	OldWeight       float64        `json:"old_weight" gorm:"type:decimal(4,2)"`
	NewWeight       float64        `json:"new_weight" gorm:"type:decimal(4,2)"`
	OldPositiveRate *float64       `json:"old_positive_rate" gorm:"type:decimal(5,4)"`
	NewPositiveRate *float64       `json:"new_positive_rate" gorm:"type:decimal(5,4)"`
	TriggerSource   string         `json:"trigger_source" gorm:"type:varchar(32);index"`
	MessageID       string         `json:"message_id" gorm:"type:varchar(36);index"`
	CreatedAt       time.Time      `json:"created_at"`
	DeletedAt       gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

type ChunkDislikeReasonStat struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}

func (l *ChunkFeedbackWeightLog) BeforeCreate(tx *gorm.DB) error {
	if l.ID == "" {
		l.ID = uuid.New().String()
	}
	return nil
}

type ChunkFeedbackResetRequest struct {
	ResetWeight bool `json:"reset_weight"`
}

type ChunkFeedbackListFilter struct {
	MinPositiveRate   *float64
	MaxPositiveRate   *float64
	NeedsOptimization *bool
	OnlyWithFeedback  bool
	SortBy            string
	SortOrder         string
}
