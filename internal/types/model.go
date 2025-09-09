package types

import (
	"database/sql/driver"
	"encoding/json"
	"time"

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
)

type ModelStatus string

const (
	ModelStatusActive         ModelStatus = "active"          // Model is active
	ModelStatusDownloading    ModelStatus = "downloading"     // Model is downloading
	ModelStatusDownloadFailed ModelStatus = "download_failed" // Model download failed
)

// ModelSource represents the source of the model
type ModelSource string

const (
	ModelSourceLocal  ModelSource = "local"  // Local model
	ModelSourceRemote ModelSource = "remote" // Remote model
	ModelSourceAliyun ModelSource = "aliyun" // Aliyun DashScope model
)

type EmbeddingParameters struct {
	Dimension            int `yaml:"dimension" json:"dimension"`
	TruncatePromptTokens int `yaml:"truncate_prompt_tokens" json:"truncate_prompt_tokens"`
}

type ModelParameters struct {
	BaseURL             string              `yaml:"base_url" json:"base_url"`
	APIKey              string              `yaml:"api_key" json:"api_key"`
	EmbeddingParameters EmbeddingParameters `yaml:"embedding_parameters" json:"embedding_parameters"`
}

// Model represents the AI model
type Model struct {
	// Unique identifier of the model
	ID string `yaml:"id" json:"id" gorm:"type:varchar(36);primaryKey"`
	// Tenant ID
	TenantID uint `yaml:"tenant_id" json:"tenant_id"`
	// Name of the model
	Name string `yaml:"name" json:"name"`
	// Type of the model
	Type ModelType `yaml:"type" json:"type"`
	// Source of the model
	Source ModelSource `yaml:"source" json:"source"`
	// Description of the model
	Description string `yaml:"description" json:"description"`
	// Model parameters in JSON format
	Parameters ModelParameters `yaml:"parameters" json:"parameters" gorm:"type:json"`
	// Whether the model is the default model
	IsDefault bool `yaml:"is_default" json:"is_default"`
	// Model status, default: active, possible: downloading, download_failed
	Status ModelStatus `yaml:"status" json:"status"`
	// Creation time of the model
	CreatedAt time.Time `yaml:"created_at" json:"created_at"`
	// Last updated time of the model
	UpdatedAt time.Time `yaml:"updated_at" json:"updated_at"`
	// Deletion time of the model
	DeletedAt gorm.DeletedAt `yaml:"deleted_at" json:"deleted_at" gorm:"index"`
}

// Value implements the driver.Valuer interface, used to convert ModelParameters to database value
func (c ModelParameters) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface, used to convert database value to ModelParameters
func (c *ModelParameters) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
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
