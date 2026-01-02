package types

import (
	"database/sql/driver"
	"encoding/json"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"
)

// retrieverEngineMapping RETRIEVE_DRIVER 값을 검색 엔진 구성에 매핑합니다.
var retrieverEngineMapping = map[string][]RetrieverEngineParams{
	"postgres": {
		{RetrieverType: KeywordsRetrieverType, RetrieverEngineType: PostgresRetrieverEngineType},
		{RetrieverType: VectorRetrieverType, RetrieverEngineType: PostgresRetrieverEngineType},
	},
	"elasticsearch_v7": {
		{RetrieverType: KeywordsRetrieverType, RetrieverEngineType: ElasticsearchRetrieverEngineType},
	},
	"elasticsearch_v8": {
		{RetrieverType: KeywordsRetrieverType, RetrieverEngineType: ElasticsearchRetrieverEngineType},
		{RetrieverType: VectorRetrieverType, RetrieverEngineType: ElasticsearchRetrieverEngineType},
	},
	"qdrant": {
		{RetrieverType: KeywordsRetrieverType, RetrieverEngineType: QdrantRetrieverEngineType},
		{RetrieverType: VectorRetrieverType, RetrieverEngineType: QdrantRetrieverEngineType},
	},
}

// GetDefaultRetrieverEngines RETRIEVE_DRIVER 환경 변수에 따라 기본 검색 엔진을 반환합니다.
func GetDefaultRetrieverEngines() []RetrieverEngineParams {
	result := []RetrieverEngineParams{}
	seen := make(map[string]bool)

	for _, driver := range strings.Split(os.Getenv("RETRIEVE_DRIVER"), ",") {
		driver = strings.TrimSpace(driver)
		if params, ok := retrieverEngineMapping[driver]; ok {
			for _, p := range params {
				key := string(p.RetrieverType) + ":" + string(p.RetrieverEngineType)
				if !seen[key] {
					seen[key] = true
					result = append(result, p)
				}
			}
		}
	}
	return result
}

// Tenant 테넌트를 나타냅니다.
type Tenant struct {
	// ID
	ID uint64 `yaml:"id"                  json:"id"                  gorm:"primaryKey"`
	// 이름
	Name string `yaml:"name"                json:"name"`
	// 설명
	Description string `yaml:"description"         json:"description"`
	// API 키
	APIKey string `yaml:"api_key"             json:"api_key"`
	// 상태
	Status string `yaml:"status"              json:"status"              gorm:"default:'active'"`
	// 검색 엔진
	RetrieverEngines RetrieverEngines `yaml:"retriever_engines"   json:"retriever_engines"   gorm:"type:json"`
	// 비즈니스
	Business string `yaml:"business"            json:"business"`
	// 저장소 할당량 (바이트), 기본값은 10GB이며 벡터, 원본 파일, 텍스트, 인덱스 등을 포함합니다.
	StorageQuota int64 `yaml:"storage_quota"       json:"storage_quota"       gorm:"default:10737418240"`
	// 사용된 저장소 (바이트)
	StorageUsed int64 `yaml:"storage_used"        json:"storage_used"        gorm:"default:0"`
	// Deprecated: AgentConfig는 더 이상 사용되지 않으며, 대신 CustomAgent (builtin-smart-reasoning) 구성을 사용하세요.
	// 이 필드는 하위 호환성을 위해 유지되며 향후 버전에서 제거될 예정입니다.
	AgentConfig *AgentConfig `yaml:"agent_config"        json:"agent_config"        gorm:"type:jsonb"`
	// 이 테넌트에 대한 전역 컨텍스트 구성 (모든 세션의 기본값)
	ContextConfig *ContextConfig `yaml:"context_config"      json:"context_config"      gorm:"type:jsonb"`
	// 이 테넌트에 대한 전역 웹 검색 구성
	WebSearchConfig *WebSearchConfig `yaml:"web_search_config"   json:"web_search_config"   gorm:"type:jsonb"`
	// Deprecated: ConversationConfig는 더 이상 사용되지 않으며, 대신 CustomAgent (builtin-quick-answer) 구성을 사용하세요.
	// 이 필드는 하위 호환성을 위해 유지되며 향후 버전에서 제거될 예정입니다.
	ConversationConfig *ConversationConfig `yaml:"conversation_config" json:"conversation_config" gorm:"type:jsonb"`
	// 생성 시간
	CreatedAt time.Time `yaml:"created_at"          json:"created_at"`
	// 마지막 업데이트 시간
	UpdatedAt time.Time `yaml:"updated_at"          json:"updated_at"`
	// 삭제 시간
	DeletedAt gorm.DeletedAt `yaml:"deleted_at"          json:"deleted_at"          gorm:"index"`
}

// RetrieverEngines 테넌트의 검색 엔진을 나타냅니다.
type RetrieverEngines struct {
	Engines []RetrieverEngineParams `yaml:"engines" json:"engines" gorm:"type:json"`
}

// GetEffectiveEngines 테넌트의 엔진이 구성된 경우 해당 엔진을 반환하고, 그렇지 않으면 시스템 기본값을 반환합니다.
func (t *Tenant) GetEffectiveEngines() []RetrieverEngineParams {
	if len(t.RetrieverEngines.Engines) > 0 {
		return t.RetrieverEngines.Engines
	}
	return GetDefaultRetrieverEngines()
}

// BeforeCreate 테넌트를 생성하기 전에 호출되는 훅 함수입니다.
func (t *Tenant) BeforeCreate(tx *gorm.DB) error {
	if t.RetrieverEngines.Engines == nil {
		t.RetrieverEngines.Engines = []RetrieverEngineParams{}
	}
	return nil
}

// Value RetrieverEngines를 데이터베이스 값으로 변환하는 driver.Valuer 인터페이스 구현
func (c RetrieverEngines) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan 데이터베이스 값을 RetrieverEngines로 변환하는 sql.Scanner 인터페이스 구현
func (c *RetrieverEngines) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// ConversationConfig 일반 모드에 대한 대화 구성을 나타냅니다.
type ConversationConfig struct {
	// Prompt는 일반 모드의 시스템 프롬프트입니다.
	Prompt string `json:"prompt"`
	// ContextTemplate은 검색 결과를 요약하기 위한 프롬프트 템플릿입니다.
	ContextTemplate string `json:"context_template"`
	// Temperature는 모델 출력의 무작위성을 제어합니다.
	Temperature float64 `json:"temperature"`
	// MaxTokens는 생성할 최대 토큰 수입니다.
	MaxCompletionTokens int `json:"max_completion_tokens"`

	// 검색 및 전략 매개변수
	MaxRounds            int     `json:"max_rounds"`
	EmbeddingTopK        int     `json:"embedding_top_k"`
	KeywordThreshold     float64 `json:"keyword_threshold"`
	VectorThreshold      float64 `json:"vector_threshold"`
	RerankTopK           int     `json:"rerank_top_k"`
	RerankThreshold      float64 `json:"rerank_threshold"`
	EnableRewrite        bool    `json:"enable_rewrite"`
	EnableQueryExpansion bool    `json:"enable_query_expansion"`

	// 모델 구성
	SummaryModelID string `json:"summary_model_id"`
	RerankModelID  string `json:"rerank_model_id"`

	// 폴백 전략
	FallbackStrategy string `json:"fallback_strategy"`
	FallbackResponse string `json:"fallback_response"`
	FallbackPrompt   string `json:"fallback_prompt"`

	// 재작성 프롬프트
	RewritePromptSystem string `json:"rewrite_prompt_system"`
	RewritePromptUser   string `json:"rewrite_prompt_user"`
}

// Value ConversationConfig를 데이터베이스 값으로 변환하는 driver.Valuer 인터페이스 구현
func (c *ConversationConfig) Value() (driver.Value, error) {
	if c == nil {
		return nil, nil
	}
	return json.Marshal(c)
}

// Scan 데이터베이스 값을 ConversationConfig로 변환하는 sql.Scanner 인터페이스 구현
func (c *ConversationConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}
