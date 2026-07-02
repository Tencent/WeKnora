package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ChunkQualityStatus 片段质量状态
type ChunkQualityStatus string

const (
	ChunkQualityStatusNormal           ChunkQualityStatus = "normal"            // 正常
	ChunkQualityStatusPendingOpt       ChunkQualityStatus = "pending_optimization" // 待优化
	ChunkQualityStatusOptimizing       ChunkQualityStatus = "optimizing"        // 优化中
	ChunkQualityStatusOptimized        ChunkQualityStatus = "optimized"         // 已优化
)

// DislikeReasonType 点踩原因类型
type DislikeReasonType string

const (
	DislikeReasonInaccurate  DislikeReasonType = "inaccurate"  // 答案不准确
	DislikeReasonIncomplete  DislikeReasonType = "incomplete"  // 答案不完整
	DislikeReasonUnclear     DislikeReasonType = "unclear"     // 表达不清楚
	DislikeReasonIrrelevant  DislikeReasonType = "irrelevant"  // 与问题不相关
	DislikeReasonOther       DislikeReasonType = "other"       // 其他
)

// FeedbackTriggerType 反馈触发类型
type FeedbackTriggerType string

const (
	FeedbackTriggerUserLike     FeedbackTriggerType = "user_like"     // 用户点赞
	FeedbackTriggerUserDislike FeedbackTriggerType = "user_dislike" // 用户点踩
	FeedbackTriggerUserCancel   FeedbackTriggerType = "user_cancel"   // 用户取消评价
	FeedbackTriggerAdminReset  FeedbackTriggerType = "admin_reset"   // 管理员重置
)

// QAReplyChunkRef 问答回复与知识库片段关联关系
// 用于记录 AI 生成回复时引用了哪些知识库片段
type QAReplyChunkRef struct {
	ID        string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	MessageID string    `json:"message_id" gorm:"type:varchar(36);uniqueIndex:idx_msg_chunk,priority:1"`
	ChunkID   string    `json:"chunk_id" gorm:"type:varchar(36);uniqueIndex:idx_msg_chunk,priority:2;index"`
	TenantID  uint64    `json:"tenant_id" gorm:"index"`
	CreatedAt time.Time `json:"created_at"`
}

// BeforeCreate 创建前自动生成 UUID
func (r *QAReplyChunkRef) BeforeCreate(tx *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	return nil
}

// ChunkFeedback 用户对问答回复的评价记录
type ChunkFeedback struct {
	ID            string             `json:"id" gorm:"type:varchar(36);primaryKey"`
	MessageID     string             `json:"message_id" gorm:"type:varchar(36);index;not null"`
	SessionID     string             `json:"session_id" gorm:"type:varchar(36);index;not null"`
	TenantID      uint64             `json:"tenant_id" gorm:"index;not null"`
	UserID        string             `json:"user_id" gorm:"type:varchar(36);index"`
	IsPositive    bool               `json:"is_positive" gorm:"not null;default:true"` // true=点赞, false=点踩
	DislikeReason string             `json:"dislike_reason,omitempty" gorm:"type:varchar(255)"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`

	// IsChanged 仅用于内部逻辑，表示评价是否发生了实质性变化
	IsChanged bool `json:"-" gorm:"-"`
}

// BeforeCreate 创建前自动生成 UUID
func (f *ChunkFeedback) BeforeCreate(tx *gorm.DB) error {
	if f.ID == "" {
		f.ID = uuid.New().String()
	}
	return nil
}

// ChunkWeightLog 片段权重变更日志
type ChunkWeightLog struct {
	ID            string             `json:"id" gorm:"type:varchar(36);primaryKey"`
	ChunkID       string             `json:"chunk_id" gorm:"type:varchar(36);index;not null"`
	TenantID      uint64             `json:"tenant_id" gorm:"index;not null"`
	Action        string             `json:"action" gorm:"type:varchar(50);index;not null"` // adjust_weight, reset, manual_set
	OldWeight     float64            `json:"old_weight" gorm:"type:float;not null"`
	NewWeight     float64            `json:"new_weight" gorm:"type:float;not null"`
	TriggerType   FeedbackTriggerType `json:"trigger_type" gorm:"type:varchar(50);index;not null"`
	TriggerDetail string             `json:"trigger_detail,omitempty" gorm:"type:varchar(500)"`
	Operator      string             `json:"operator,omitempty" gorm:"type:varchar(36)"` // 管理员操作时记录操作人
	CreatedAt     time.Time          `json:"created_at"`
}

// BeforeCreate 创建前自动生成 UUID
func (l *ChunkWeightLog) BeforeCreate(tx *gorm.DB) error {
	if l.ID == "" {
		l.ID = uuid.New().String()
	}
	return nil
}

// ============================================
// 请求/响应类型
// ============================================

// SubmitFeedbackRequest 提交反馈请求
type SubmitFeedbackRequest struct {
	MessageID     string `json:"message_id" binding:"required"`      // 消息ID
	IsPositive    bool   `json:"is_positive"`                       // 是否为点赞
	DislikeReason string `json:"dislike_reason,omitempty"`          // 点踩原因
}

// SubmitFeedbackResponse 提交反馈响应
type SubmitFeedbackResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// ChunkStatsResponse 片段统计响应
type ChunkStatsResponse struct {
	ChunkID             string    `json:"chunk_id"`              // 片段ID
	LikeCount           int       `json:"like_count"`            // 点赞数
	DislikeCount        int       `json:"dislike_count"`         // 点踩数
	PositiveRate        float64   `json:"positive_rate"`         // 好评率
	RecallWeight        float64   `json:"recall_weight"`         // 召回权重
	QualityStatus       string    `json:"quality_status"`        // 质量状态
	RelatedSessionCount int       `json:"related_session_count"` // 关联会话数
	DislikeReasons      []string  `json:"dislike_reasons"`       // 点踩原因聚合
	LastFeedbackAt      *time.Time `json:"last_feedback_at"`      // 最后反馈时间
}

// ListLowQualityChunksRequest 列出低质量片段请求
type ListLowQualityChunksRequest struct {
	MaxRate float64 `form:"max_rate"` // 最高好评率阈值
	Limit   int     `form:"limit,default=20"`
	Offset  int     `form:"offset,default=0"`
}

// ChunkQualityStats 片段质量统计（用于列表展示）
type ChunkQualityStats struct {
	ChunkID       string  `json:"chunk_id"`
	KnowledgeID   string  `json:"knowledge_id"`
	KnowledgeName string  `json:"knowledge_name"`
	Content       string  `json:"content"` // 片段内容摘要
	LikeCount     int     `json:"like_count"`
	DislikeCount  int     `json:"dislike_count"`
	PositiveRate  float64 `json:"positive_rate"`
	RecallWeight  float64 `json:"recall_weight"`
	QualityStatus string  `json:"quality_status"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ChunkFeedbackConfig 反馈配置
type ChunkFeedbackConfig struct {
	// 好评率阈值配置
	HighQualityThreshold float64 `json:"high_quality_threshold"` // >= 此值提升权重，默认 0.8
	LowQualityThreshold float64 `json:"low_quality_threshold"`  // < 此值降低权重，默认 0.5

	// 权重调整参数
	WeightBoostFactor   float64 `json:"weight_boost_factor"`   // 高质量权重提升倍数，默认 1.5
	WeightPenaltyFactor float64 `json:"weight_penalty_factor"`  // 低质量权重降低倍数，默认 0.5
	MinWeight           float64 `json:"min_weight"`            // 最小权重，默认 0.1
	MaxWeight           float64 `json:"max_weight"`            // 最大权重，默认 2.0

	// 自动标记阈值
	AutoMarkThreshold float64 `json:"auto_mark_threshold"` // 自动标记待优化的好评率阈值，默认 0.3
}

// DefaultChunkFeedbackConfig 返回默认配置
func DefaultChunkFeedbackConfig() *ChunkFeedbackConfig {
	return &ChunkFeedbackConfig{
		HighQualityThreshold: 0.8,
		LowQualityThreshold:  0.5,
		WeightBoostFactor:    1.5,
		WeightPenaltyFactor:  0.5,
		MinWeight:            0.1,
		MaxWeight:            2.0,
		AutoMarkThreshold:    0.3,
	}
}

// ResetChunkFeedbackRequest 重置片段反馈请求
type ResetChunkFeedbackRequest struct {
	ChunkID  string `json:"chunk_id" binding:"required"`
	Operator string `json:"operator,omitempty"` // 操作人
}

// WeightLogResponse 权重日志响应
type WeightLogResponse struct {
	Logs []ChunkWeightLog `json:"logs"`
	Total int64           `json:"total"`
}

// GetDislikeReasons 返回预定义的原因选项
func GetDislikeReasons() []string {
	return []string{
		"答案不准确",
		"答案不完整",
		"表达不清楚",
		"与问题不相关",
		"其他",
	}
}

// UserFeedbackResponse 用户反馈状态响应
type UserFeedbackResponse struct {
	MessageID     string `json:"message_id"`
	IsPositive    *bool  `json:"is_positive"`
	DislikeReason string `json:"dislike_reason,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
}
