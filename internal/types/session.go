package types

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// FallbackStrategy represents the fallback strategy type
type FallbackStrategy string

const (
	FallbackStrategyFixed FallbackStrategy = "fixed" // Fixed response
	FallbackStrategyModel FallbackStrategy = "model" // Model fallback response
)

// SummaryConfig represents the summary configuration for a session
type SummaryConfig struct {
	// Max tokens
	MaxTokens int `json:"max_tokens"`
	// Repeat penalty
	RepeatPenalty float64 `json:"repeat_penalty"`
	// TopK
	TopK int `json:"top_k"`
	// TopP
	TopP float64 `json:"top_p"`
	// Frequency penalty
	FrequencyPenalty float64 `json:"frequency_penalty"`
	// Presence penalty
	PresencePenalty float64 `json:"presence_penalty"`
	// Prompt
	Prompt string `json:"prompt"`
	// Context template
	ContextTemplate string `json:"context_template"`
	// No match prefix
	NoMatchPrefix string `json:"no_match_prefix"`
	// Temperature
	Temperature float64 `json:"temperature"`
	// Seed
	Seed int `json:"seed"`
	// Max completion tokens
	MaxCompletionTokens int `json:"max_completion_tokens"`
	// Thinking - whether to enable thinking mode
	Thinking *bool `json:"thinking"`
}

// ContextCompressionStrategy represents the strategy for context compression
type ContextCompressionStrategy string

const (
	// ContextCompressionSlidingWindow keeps the most recent N messages
	ContextCompressionSlidingWindow ContextCompressionStrategy = "sliding_window"
	// ContextCompressionSmart uses LLM to summarize old messages
	ContextCompressionSmart ContextCompressionStrategy = "smart"
)

// ContextConfig configures LLM context management
// This is separate from message storage and manages token limits
type ContextConfig struct {
	// Maximum tokens allowed in LLM context
	MaxTokens int `json:"max_tokens"`
	// Compression strategy: "sliding_window" or "smart"
	CompressionStrategy ContextCompressionStrategy `json:"compression_strategy"`
	// For sliding_window: number of messages to keep
	// For smart: number of recent messages to keep uncompressed
	RecentMessageCount int `json:"recent_message_count"`
	// Summarize threshold: number of messages before summarization
	SummarizeThreshold int `json:"summarize_threshold"`
}

// Session represents the session
type Session struct {
	// ID
	ID string `json:"id"          gorm:"type:varchar(36);primaryKey"`
	// Title
	Title string `json:"title"`
	// Description
	Description string `json:"description"`
	// Tenant ID
	TenantID uint64 `json:"tenant_id"   gorm:"index"`

	// // Strategy configuration
	// KnowledgeBaseID   string              `json:"knowledge_base_id"`                    // 关联的知识库ID
	// MaxRounds         int                 `json:"max_rounds"`                           // 多轮保持轮数
	// EnableRewrite     bool                `json:"enable_rewrite"`                       // 多轮改写开关
	// FallbackStrategy  FallbackStrategy    `json:"fallback_strategy"`                    // 兜底策略
	// FallbackResponse  string              `json:"fallback_response"`                    // 固定回复内容
	// EmbeddingTopK     int                 `json:"embedding_top_k"`                      // 向量召回TopK
	// KeywordThreshold  float64             `json:"keyword_threshold"`                    // 关键词召回阈值
	// VectorThreshold   float64             `json:"vector_threshold"`                     // 向量召回阈值
	// RerankModelID     string              `json:"rerank_model_id"`                      // 排序模型ID
	// RerankTopK        int                 `json:"rerank_top_k"`                         // 排序TopK
	// RerankThreshold   float64             `json:"rerank_threshold"`                     // 排序阈值
	// SummaryModelID    string              `json:"summary_model_id"`                     // 总结模型ID
	// SummaryParameters *SummaryConfig      `json:"summary_parameters" gorm:"type:json"`  // 总结模型参数
	// AgentConfig       *SessionAgentConfig `json:"agent_config"       gorm:"type:jsonb"` // Agent 配置（会话级别，仅存储enabled和knowledge_bases）
	// ContextConfig     *ContextConfig      `json:"context_config"     gorm:"type:jsonb"` // 上下文管理配置（可选）

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`

	// Association relationship, not stored in the database
	Messages []Message `json:"-" gorm:"foreignKey:SessionID"`
}

func (s *Session) BeforeCreate(tx *gorm.DB) (err error) {
	s.ID = uuid.New().String()
	return nil
}

// StringArray represents a list of strings
type StringArray []string

// Value implements the driver.Valuer interface, used to convert StringArray to database value
func (c StringArray) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface, used to convert database value to StringArray
func (c *StringArray) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// Value implements the driver.Valuer interface, used to convert SummaryConfig to database value
func (c *SummaryConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface, used to convert database value to SummaryConfig
func (c *SummaryConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// Value implements the driver.Valuer interface, used to convert ContextConfig to database value
func (c *ContextConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface, used to convert database value to ContextConfig
func (c *ContextConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}
