package types

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// FallbackStrategy 폴백 전략 유형을 나타냅니다.
type FallbackStrategy string

const (
	FallbackStrategyFixed FallbackStrategy = "fixed" // 고정 응답
	FallbackStrategyModel FallbackStrategy = "model" // 모델 폴백 응답
)

// SummaryConfig 세션에 대한 요약 구성을 나타냅니다.
type SummaryConfig struct {
	// 최대 토큰 수
	MaxTokens int `json:"max_tokens"`
	// 반복 페널티
	RepeatPenalty float64 `json:"repeat_penalty"`
	// TopK
	TopK int `json:"top_k"`
	// TopP
	TopP float64 `json:"top_p"`
	// 빈도 페널티
	FrequencyPenalty float64 `json:"frequency_penalty"`
	// 존재 페널티
	PresencePenalty float64 `json:"presence_penalty"`
	// 프롬프트
	Prompt string `json:"prompt"`
	// 컨텍스트 템플릿
	ContextTemplate string `json:"context_template"`
	// 불일치 접두사
	NoMatchPrefix string `json:"no_match_prefix"`
	// 온도
	Temperature float64 `json:"temperature"`
	// 시드
	Seed int `json:"seed"`
	// 최대 완료 토큰 수
	MaxCompletionTokens int `json:"max_completion_tokens"`
	// 생각 모드 활성화 여부
	Thinking *bool `json:"thinking"`
}

// ContextCompressionStrategy 컨텍스트 압축 전략을 나타냅니다.
type ContextCompressionStrategy string

const (
	// ContextCompressionSlidingWindow 가장 최근 N개의 메시지를 유지합니다.
	ContextCompressionSlidingWindow ContextCompressionStrategy = "sliding_window"
	// ContextCompressionSmart LLM을 사용하여 오래된 메시지를 요약합니다.
	ContextCompressionSmart ContextCompressionStrategy = "smart"
)

// ContextConfig LLM 컨텍스트 관리를 구성합니다.
// 이는 메시지 저장소와 별개이며 토큰 제한을 관리합니다.
type ContextConfig struct {
	// LLM 컨텍스트에 허용되는 최대 토큰 수
	MaxTokens int `json:"max_tokens"`
	// 압축 전략: "sliding_window" 또는 "smart"
	CompressionStrategy ContextCompressionStrategy `json:"compression_strategy"`
	// sliding_window의 경우: 유지할 메시지 수
	// smart의 경우: 압축하지 않고 유지할 최근 메시지 수
	RecentMessageCount int `json:"recent_message_count"`
	// 요약 임계값: 요약 전 메시지 수
	SummarizeThreshold int `json:"summarize_threshold"`
}

// Session 세션을 나타냅니다.
type Session struct {
	// ID
	ID string `json:"id"          gorm:"type:varchar(36);primaryKey"`
	// 제목
	Title string `json:"title"`
	// 설명
	Description string `json:"description"`
	// 테넌트 ID
	TenantID uint64 `json:"tenant_id"   gorm:"index"`

	// // 전략 구성
	// KnowledgeBaseID   string              `json:"knowledge_base_id"`                    // 연결된 지식베이스 ID
	// MaxRounds         int                 `json:"max_rounds"`                           // 다중 턴 유지 라운드 수
	// EnableRewrite     bool                `json:"enable_rewrite"`                       // 다중 턴 재작성 스위치
	// FallbackStrategy  FallbackStrategy    `json:"fallback_strategy"`                    // 폴백 전략
	// FallbackResponse  string              `json:"fallback_response"`                    // 고정 응답 내용
	// EmbeddingTopK     int                 `json:"embedding_top_k"`                      // 벡터 리콜 TopK
	// KeywordThreshold  float64             `json:"keyword_threshold"`                    // 키워드 리콜 임계값
	// VectorThreshold   float64             `json:"vector_threshold"`                     // 벡터 리콜 임계값
	// RerankModelID     string              `json:"rerank_model_id"`                      // 정렬 모델 ID
	// RerankTopK        int                 `json:"rerank_top_k"`                         // 정렬 TopK
	// RerankThreshold   float64             `json:"rerank_threshold"`                     // 정렬 임계값
	// SummaryModelID    string              `json:"summary_model_id"`                     // 요약 모델 ID
	// SummaryParameters *SummaryConfig      `json:"summary_parameters" gorm:"type:json"`  // 요약 모델 매개변수
	// AgentConfig       *SessionAgentConfig `json:"agent_config"       gorm:"type:jsonb"` // 에이전트 구성 (세션 수준, enabled 및 knowledge_bases만 저장)
	// ContextConfig     *ContextConfig      `json:"context_config"     gorm:"type:jsonb"` // 컨텍스트 관리 구성 (선택 사항)

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`

	// 연관 관계, 데이터베이스에 저장되지 않음
	Messages []Message `json:"-" gorm:"foreignKey:SessionID"`
}

func (s *Session) BeforeCreate(tx *gorm.DB) (err error) {
	s.ID = uuid.New().String()
	return nil
}

// StringArray 문자열 목록을 나타냅니다.
type StringArray []string

// Value StringArray를 데이터베이스 값으로 변환하는 driver.Valuer 인터페이스 구현
func (c StringArray) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan 데이터베이스 값을 StringArray로 변환하는 sql.Scanner 인터페이스 구현
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

// Value SummaryConfig를 데이터베이스 값으로 변환하는 driver.Valuer 인터페이스 구현
func (c *SummaryConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan 데이터베이스 값을 SummaryConfig로 변환하는 sql.Scanner 인터페이스 구현
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

// Value ContextConfig를 데이터베이스 값으로 변환하는 driver.Valuer 인터페이스 구현
func (c *ContextConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan 데이터베이스 값을 ContextConfig로 변환하는 sql.Scanner 인터페이스 구현
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
