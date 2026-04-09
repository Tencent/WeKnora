package types

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ModelType represents the type of AI model
type ModelType string

const (
	ModelTypeEmbedding   ModelType = "Embedding"   // Embedding model
	ModelTypeRerank      ModelType = "Rerank"      // Rerank model
	ModelTypeKnowledgeQA ModelType = "KnowledgeQA" // KnowledgeQA model
	ModelTypeVLLM        ModelType = "VLLM"        // VLLM model
	ModelTypeASR         ModelType = "ASR"         // ASR (Automatic Speech Recognition) model
)

// ModelStatus represents the status of the model
type ModelStatus string

const (
	ModelStatusActive         ModelStatus = "active"          // Model is active
	ModelStatusDownloading    ModelStatus = "downloading"     // Model is downloading
	ModelStatusDownloadFailed ModelStatus = "download_failed" // Model download failed
)

// ModelSource represents the source of the model
type ModelSource string

const (
	ModelSourceLocal       ModelSource = "local"       // Local model
	ModelSourceRemote      ModelSource = "remote"      // Remote model
	ModelSourceAliyun      ModelSource = "aliyun"      // Aliyun DashScope model
	ModelSourceZhipu       ModelSource = "zhipu"       // Zhipu model
	ModelSourceVolcengine  ModelSource = "volcengine"  // Volcengine model
	ModelSourceDeepseek    ModelSource = "deepseek"    // Deepseek model
	ModelSourceHunyuan     ModelSource = "hunyuan"     // Hunyuan model
	ModelSourceMinimax     ModelSource = "minimax"     // Minimax mode
	ModelSourceOpenAI      ModelSource = "openai"      // OpenAI model
	ModelSourceGemini      ModelSource = "gemini"      // Gemini model
	ModelSourceMimo        ModelSource = "mimo"        // Mimo model
	ModelSourceSiliconFlow ModelSource = "siliconflow" // SiliconFlow model
	ModelSourceJina        ModelSource = "jina"        // Jina AI model
	ModelSourceOpenRouter  ModelSource = "openrouter"  // OpenRouter model
	ModelSourceNvidia      ModelSource = "nvidia"      // NVIDIA model
	ModelSourceNovita      ModelSource = "novita"      // Novita AI model
	ModelSourceAzureOpenAI ModelSource = "azure_openai" // Azure OpenAI model
)

// EmbeddingParameters represents the embedding parameters for a model
type EmbeddingParameters struct {
	Dimension            int `yaml:"dimension"              json:"dimension"`
	TruncatePromptTokens int `yaml:"truncate_prompt_tokens" json:"truncate_prompt_tokens"`
}

type ModelParameters struct {
	BaseURL             string              `yaml:"base_url"             json:"base_url"`
	APIKey              string              `yaml:"api_key"              json:"api_key"`
	InterfaceType       string              `yaml:"interface_type"       json:"interface_type"`
	EmbeddingParameters EmbeddingParameters `yaml:"embedding_parameters" json:"embedding_parameters"`
	ParameterSize       string              `yaml:"parameter_size"       json:"parameter_size"`  // Ollama model parameter size (e.g., "7B", "13B", "70B")
	Provider            string              `yaml:"provider"             json:"provider"`        // Provider identifier: openai, aliyun, zhipu, generic
	ExtraConfig         map[string]string   `yaml:"extra_config"         json:"extra_config"`    // Provider-specific configuration
	SupportsVision      bool                `yaml:"supports_vision"      json:"supports_vision"` // Whether the model accepts image/multimodal input
}

// Model represents the AI model
type Model struct {
	// Unique identifier of the model
	ID string `yaml:"id"          json:"id"          gorm:"type:varchar(36);primaryKey"`
	// Tenant ID
	TenantID uint64 `yaml:"tenant_id"   json:"tenant_id"`
	// Name of the model
	Name string `yaml:"name"        json:"name"`
	// Type of the model
	Type ModelType `yaml:"type"        json:"type"`
	// Source of the model
	Source ModelSource `yaml:"source"      json:"source"`
	// Description of the model
	Description string `yaml:"description" json:"description"`
	// Model parameters in JSON format
	Parameters ModelParameters `yaml:"parameters"  json:"parameters"  gorm:"type:json"`
	// Whether the model is the default model
	IsDefault bool `yaml:"is_default"  json:"is_default"`
	// Whether the model is a builtin model (visible to all tenants)
	IsBuiltin bool `yaml:"is_builtin"  json:"is_builtin"  gorm:"default:false"`
	// Model status, default: active, possible: downloading, download_failed
	Status ModelStatus `yaml:"status"      json:"status"`
	// Creation time of the model
	CreatedAt time.Time `yaml:"created_at"  json:"created_at"`
	// Last updated time of the model
	UpdatedAt time.Time `yaml:"updated_at"  json:"updated_at"`
	// Deletion time of the model
	DeletedAt gorm.DeletedAt `yaml:"deleted_at"  json:"deleted_at"  gorm:"index"`
}

// Value implements the driver.Valuer interface, used to convert ModelParameters to database value.
// Encrypts APIKey before persisting to database (value receiver = no memory pollution).
func (c ModelParameters) Value() (driver.Value, error) {
	if key := utils.GetAESKey(); key != nil && c.APIKey != "" {
		if encrypted, err := utils.EncryptAESGCM(c.APIKey, key); err == nil {
			c.APIKey = encrypted
		}
	}
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface, used to convert database value to ModelParameters.
// Decrypts APIKey after loading from database; legacy plaintext is returned as-is.
func (c *ModelParameters) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	if err := json.Unmarshal(b, c); err != nil {
		return err
	}
	if key := utils.GetAESKey(); key != nil && c.APIKey != "" {
		if decrypted, err := utils.DecryptAESGCM(c.APIKey, key); err == nil {
			c.APIKey = decrypted
		}
	}
	return nil
}

// BeforeCreate is a GORM hook that runs before creating a new model record
// Automatically generates a UUID for new models
// Parameters:
//   - tx: GORM database transaction
//
// Returns:
//   - error: Any error encountered during the hook execution
func (m *Model) BeforeCreate(tx *gorm.DB) (err error) {
	m.ID = uuid.New().String()
	return nil
}
