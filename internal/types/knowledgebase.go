package types

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// KnowledgeBaseType 지식베이스 유형을 나타냅니다
const (
	// KnowledgeBaseTypeDocument 문서 지식베이스 유형을 나타냅니다
	KnowledgeBaseTypeDocument = "document"
	KnowledgeBaseTypeFAQ      = "faq"
)

// FAQIndexMode FAQ 인덱스 모드: 질문만 인덱싱 또는 질문과 답변 인덱싱
type FAQIndexMode string

const (
	// FAQIndexModeQuestionOnly 질문과 유사 질문만 인덱싱
	FAQIndexModeQuestionOnly FAQIndexMode = "question_only"
	// FAQIndexModeQuestionAnswer 질문과 답변을 함께 인덱싱
	FAQIndexModeQuestionAnswer FAQIndexMode = "question_answer"
)

// FAQQuestionIndexMode FAQ 질문 인덱스 모드: 함께 인덱싱 또는 별도 인덱싱
type FAQQuestionIndexMode string

const (
	// FAQQuestionIndexModeCombined 질문과 유사 질문을 함께 인덱싱
	FAQQuestionIndexModeCombined FAQQuestionIndexMode = "combined"
	// FAQQuestionIndexModeSeparate 질문과 유사 질문을 별도로 인덱싱
	FAQQuestionIndexModeSeparate FAQQuestionIndexMode = "separate"
)

// KnowledgeBase 지식베이스 엔티티를 나타냅니다
type KnowledgeBase struct {
	// 지식베이스 고유 식별자
	ID string `yaml:"id"                      json:"id"                      gorm:"type:varchar(36);primaryKey"`
	// 지식베이스 이름
	Name string `yaml:"name"                    json:"name"`
	// 지식베이스 유형 (document, faq 등)
	Type string `yaml:"type"                    json:"type"                    gorm:"type:varchar(32);default:'document'"`
	// 임시(일시적) 지식베이스 여부 (UI에서 숨김)
	IsTemporary bool `yaml:"is_temporary"            json:"is_temporary"            gorm:"default:false"`
	// 지식베이스 설명
	Description string `yaml:"description"             json:"description"`
	// 테넌트 ID
	TenantID uint64 `yaml:"tenant_id"               json:"tenant_id"`
	// 청크 구성
	ChunkingConfig ChunkingConfig `yaml:"chunking_config"         json:"chunking_config"         gorm:"type:json"`
	// 이미지 처리 구성
	ImageProcessingConfig ImageProcessingConfig `yaml:"image_processing_config" json:"image_processing_config" gorm:"type:json"`
	// 임베딩 모델 ID
	EmbeddingModelID string `yaml:"embedding_model_id"      json:"embedding_model_id"`
	// 요약 모델 ID
	SummaryModelID string `yaml:"summary_model_id"        json:"summary_model_id"`
	// VLM 구성
	VLMConfig VLMConfig `yaml:"vlm_config"              json:"vlm_config"              gorm:"type:json"`
	// 저장소 구성
	StorageConfig StorageConfig `yaml:"cos_config"              json:"cos_config"              gorm:"column:cos_config;type:json"`
	// 추출 구성
	ExtractConfig *ExtractConfig `yaml:"extract_config"          json:"extract_config"          gorm:"column:extract_config;type:json"`
	// FAQConfig 인덱싱 전략과 같은 FAQ 고유 구성 저장
	FAQConfig *FAQConfig `yaml:"faq_config"              json:"faq_config"              gorm:"column:faq_config;type:json"`
	// QuestionGenerationConfig 문서 지식베이스에 대한 질문 생성 구성 저장
	QuestionGenerationConfig *QuestionGenerationConfig `yaml:"question_generation_config" json:"question_generation_config" gorm:"column:question_generation_config;type:json"`
	// 지식베이스 생성 시간
	CreatedAt time.Time `yaml:"created_at"              json:"created_at"`
	// 지식베이스 마지막 업데이트 시간
	UpdatedAt time.Time `yaml:"updated_at"              json:"updated_at"`
	// 지식베이스 삭제 시간
	DeletedAt gorm.DeletedAt `yaml:"deleted_at"              json:"deleted_at"              gorm:"index"`
	// 지식 수 (데이터베이스에 저장되지 않음, 쿼리 시 계산됨)
	KnowledgeCount int64 `yaml:"knowledge_count"         json:"knowledge_count"         gorm:"-"`
	// 청크 수 (데이터베이스에 저장되지 않음, 쿼리 시 계산됨)
	ChunkCount int64 `yaml:"chunk_count"             json:"chunk_count"             gorm:"-"`
	// IsProcessing 처리 중인 가져오기 작업이 있는지 여부 (FAQ 유형 지식베이스용)
	IsProcessing bool `yaml:"is_processing"           json:"is_processing"           gorm:"-"`
	// ProcessingCount 처리 중인 지식 항목 수 (문서 유형 지식베이스용)
	ProcessingCount int64 `yaml:"processing_count"        json:"processing_count"        gorm:"-"`
}

// KnowledgeBaseConfig 지식베이스 구성을 나타냅니다
type KnowledgeBaseConfig struct {
	// 청크 구성
	ChunkingConfig ChunkingConfig `yaml:"chunking_config"         json:"chunking_config"`
	// 이미지 처리 구성
	ImageProcessingConfig ImageProcessingConfig `yaml:"image_processing_config" json:"image_processing_config"`
	// FAQ 구성 (FAQ 유형 지식베이스에만 해당)
	FAQConfig *FAQConfig `yaml:"faq_config"              json:"faq_config"`
}

// ChunkingConfig 문서 분할 구성을 나타냅니다
type ChunkingConfig struct {
	// 청크 크기
	ChunkSize int `yaml:"chunk_size"    json:"chunk_size"`
	// 청크 중복
	ChunkOverlap int `yaml:"chunk_overlap" json:"chunk_overlap"`
	// 구분자
	Separators []string `yaml:"separators"    json:"separators"`
	// EnableMultimodal (더 이상 사용되지 않음, 이전 데이터와의 호환성을 위해 유지됨)
	EnableMultimodal bool `yaml:"enable_multimodal,omitempty" json:"enable_multimodal,omitempty"`
}

// StorageConfig COS 구성을 나타냅니다
type StorageConfig struct {
	// Secret ID
	SecretID string `yaml:"secret_id"   json:"secret_id"`
	// Secret Key
	SecretKey string `yaml:"secret_key"  json:"secret_key"`
	// 지역
	Region string `yaml:"region"      json:"region"`
	// 버킷 이름
	BucketName string `yaml:"bucket_name" json:"bucket_name"`
	// 앱 ID
	AppID string `yaml:"app_id"      json:"app_id"`
	// 경로 접두사
	PathPrefix string `yaml:"path_prefix" json:"path_prefix"`
	// 공급자
	Provider string `yaml:"provider"    json:"provider"`
}

func (c StorageConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

func (c *StorageConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// ImageProcessingConfig 이미지 처리 구성을 나타냅니다
type ImageProcessingConfig struct {
	// 모델 ID
	ModelID string `yaml:"model_id" json:"model_id"`
}

// Value ChunkingConfig를 데이터베이스 값으로 변환하는 driver.Valuer 인터페이스 구현
func (c ChunkingConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan 데이터베이스 값을 ChunkingConfig로 변환하는 sql.Scanner 인터페이스 구현
func (c *ChunkingConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// Value ImageProcessingConfig를 데이터베이스 값으로 변환하는 driver.Valuer 인터페이스 구현
func (c ImageProcessingConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan 데이터베이스 값을 ImageProcessingConfig로 변환하는 sql.Scanner 인터페이스 구현
func (c *ImageProcessingConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// VLMConfig VLM 구성을 나타냅니다
type VLMConfig struct {
	Enabled bool   `yaml:"enabled"  json:"enabled"`
	ModelID string `yaml:"model_id" json:"model_id"`

	// 이전 버전 호환성
	// 모델 이름
	ModelName string `yaml:"model_name" json:"model_name"`
	// 기본 URL
	BaseURL string `yaml:"base_url" json:"base_url"`
	// API 키
	APIKey string `yaml:"api_key" json:"api_key"`
	// 인터페이스 유형: "ollama" 또는 "openai"
	InterfaceType string `yaml:"interface_type" json:"interface_type"`
}

// IsEnabled 멀티모달 활성화 여부 판단 (신규 및 구형 구성 호환)
// 신규: Enabled && ModelID != ""
// 구형: ModelName != "" && BaseURL != ""
func (c VLMConfig) IsEnabled() bool {
	// 신규 버전 구성
	if c.Enabled && c.ModelID != "" {
		return true
	}
	// 구형 버전 호환 구성
	if c.ModelName != "" && c.BaseURL != "" {
		return true
	}
	return false
}

// QuestionGenerationConfig 문서 지식베이스에 대한 질문 생성 구성을 나타냅니다
// 활성화되면 시스템은 문서 파싱 중에 각 청크에 대해 LLM을 사용하여 질문을 생성합니다
// 생성된 질문은 재현율을 높이기 위해 별도로 인덱싱됩니다
type QuestionGenerationConfig struct {
	Enabled bool `yaml:"enabled"  json:"enabled"`
	// 청크당 생성할 질문 수 (기본값: 3, 최대: 10)
	QuestionCount int `yaml:"question_count" json:"question_count"`
}

// Value driver.Valuer 인터페이스 구현
func (c QuestionGenerationConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan sql.Scanner 인터페이스 구현
func (c *QuestionGenerationConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// Value VLMConfig를 데이터베이스 값으로 변환하는 driver.Valuer 인터페이스 구현
func (c VLMConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan 데이터베이스 값을 VLMConfig로 변환하는 sql.Scanner 인터페이스 구현
func (c *VLMConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// ExtractConfig 지식베이스에 대한 추출 구성을 나타냅니다
type ExtractConfig struct {
	Enabled   bool             `yaml:"enabled"   json:"enabled"`
	Text      string           `yaml:"text"      json:"text,omitempty"`
	Tags      []string         `yaml:"tags"      json:"tags,omitempty"`
	Nodes     []*GraphNode     `yaml:"nodes"     json:"nodes,omitempty"`
	Relations []*GraphRelation `yaml:"relations" json:"relations,omitempty"`
}

// Value ExtractConfig를 데이터베이스 값으로 변환하는 driver.Valuer 인터페이스 구현
func (e ExtractConfig) Value() (driver.Value, error) {
	return json.Marshal(e)
}

// Scan 데이터베이스 값을 ExtractConfig로 변환하는 sql.Scanner 인터페이스 구현
func (e *ExtractConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, e)
}

// FAQConfig FAQ 지식베이스 고유 구성 저장
type FAQConfig struct {
	IndexMode         FAQIndexMode         `yaml:"index_mode"          json:"index_mode"`
	QuestionIndexMode FAQQuestionIndexMode `yaml:"question_index_mode" json:"question_index_mode"`
}

// Value driver.Valuer 구현
func (f FAQConfig) Value() (driver.Value, error) {
	return json.Marshal(f)
}

// Scan sql.Scanner 구현
func (f *FAQConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, f)
}

// EnsureDefaults 유형 및 구성에 기본값이 있는지 확인
func (kb *KnowledgeBase) EnsureDefaults() {
	if kb == nil {
		return
	}
	if kb.Type == "" {
		kb.Type = KnowledgeBaseTypeDocument
	}
	if kb.Type != KnowledgeBaseTypeFAQ {
		kb.FAQConfig = nil
		return
	}
	if kb.FAQConfig == nil {
		kb.FAQConfig = &FAQConfig{
			IndexMode:         FAQIndexModeQuestionAnswer,
			QuestionIndexMode: FAQQuestionIndexModeCombined,
		}
		return
	}
	if kb.FAQConfig.IndexMode == "" {
		kb.FAQConfig.IndexMode = FAQIndexModeQuestionAnswer
	}
	if kb.FAQConfig.QuestionIndexMode == "" {
		kb.FAQConfig.QuestionIndexMode = FAQQuestionIndexModeCombined
	}
}

// IsMultimodalEnabled 멀티모달 활성화 여부 판단 (신규 및 구형 구성 호환)
// 신규: VLMConfig.IsEnabled()
// 구형: ChunkingConfig.EnableMultimodal
func (kb *KnowledgeBase) IsMultimodalEnabled() bool {
	if kb == nil {
		return false
	}
	// 신규 버전 구성 우선
	if kb.VLMConfig.IsEnabled() {
		return true
	}
	// 구형 버전 호환: chunking_config의 enable_multimodal 필드
	if kb.ChunkingConfig.EnableMultimodal {
		return true
	}
	return false
}
