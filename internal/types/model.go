package types

import (
	"database/sql/driver"
	"encoding/json"
	"log"
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
	ModelSourceLocal       ModelSource = "local"        // Local model
	ModelSourceRemote      ModelSource = "remote"       // Remote model
	ModelSourceAliyun      ModelSource = "aliyun"       // Aliyun DashScope model
	ModelSourceZhipu       ModelSource = "zhipu"        // Zhipu model
	ModelSourceVolcengine  ModelSource = "volcengine"   // Volcengine model
	ModelSourceDeepseek    ModelSource = "deepseek"     // Deepseek model
	ModelSourceHunyuan     ModelSource = "hunyuan"      // Hunyuan model
	ModelSourceMinimax     ModelSource = "minimax"      // Minimax mode
	ModelSourceOpenAI      ModelSource = "openai"       // OpenAI model
	ModelSourceGemini      ModelSource = "gemini"       // Gemini model
	ModelSourceMimo        ModelSource = "mimo"         // Mimo model
	ModelSourceSiliconFlow ModelSource = "siliconflow"  // SiliconFlow model
	ModelSourceJina        ModelSource = "jina"         // Jina AI model
	ModelSourceOpenRouter  ModelSource = "openrouter"   // OpenRouter model
	ModelSourceRequesty    ModelSource = "requesty"     // Requesty model
	ModelSourceNvidia      ModelSource = "nvidia"       // NVIDIA model
	ModelSourceNovita      ModelSource = "novita"       // Novita AI model
	ModelSourceAzureOpenAI ModelSource = "azure_openai" // Azure OpenAI model
)

// EmbeddingParameters represents the embedding parameters for a model
type EmbeddingParameters struct {
	Dimension                 int  `yaml:"dimension"                   json:"dimension"`
	TruncatePromptTokens      int  `yaml:"truncate_prompt_tokens"      json:"truncate_prompt_tokens"`
	SupportsDimensionOverride bool `yaml:"supports_dimension_override" json:"supports_dimension_override"`
}

type ModelParameters struct {
	BaseURL             string              `yaml:"base_url"             json:"base_url"`
	APIKey              string              `yaml:"api_key"              json:"api_key"`
	InterfaceType       string              `yaml:"interface_type"       json:"interface_type"`
	EmbeddingParameters EmbeddingParameters `yaml:"embedding_parameters" json:"embedding_parameters"`
	ParameterSize       string              `yaml:"parameter_size"       json:"parameter_size"` // Ollama model parameter size (e.g., "7B", "13B", "70B")
	Provider            string              `yaml:"provider"             json:"provider"`       // Provider identifier: openai, aliyun, zhipu, generic
	ExtraConfig         map[string]string   `yaml:"extra_config"         json:"extra_config"`   // Provider-specific configuration
	// CustomHeaders 允许在调用远程模型 API 时附加自定义 HTTP 请求头，
	// 用途类似 Python OpenAI SDK 的 extra_headers 参数，
	// 常见场景包括透传企业网关鉴权信息、追踪 ID、路由标识等。
	// 保留字段（Authorization、api-key、Content-Type、Accept 等）会在运行期被忽略以避免破坏签名/鉴权流程。
	CustomHeaders  map[string]string `yaml:"custom_headers,omitempty" json:"custom_headers,omitempty"`
	SupportsVision bool              `yaml:"supports_vision"      json:"supports_vision"` // Whether the model accepts image/multimodal input
	// MaxConcurrency caps concurrent in-flight BACKGROUND (ingestion /
	// enrichment) calls to THIS specific model, keyed by model ID and shared
	// across all replicas. 0 (the default) means "fall back to the
	// process-wide model.max_concurrency". Interactive user-facing calls are
	// never gated. Only chat / vlm / embedding honour this (see limiter.Gate).
	MaxConcurrency int `yaml:"max_concurrency,omitempty" json:"max_concurrency,omitempty"`
	// WeKnoraCloud 厂商专用凭证
	AppID     string `yaml:"app_id,omitempty"     json:"app_id,omitempty"`
	AppSecret string `yaml:"app_secret,omitempty" json:"app_secret,omitempty"` // AES-256 加密存储，实际承载上游 API Key
}

// Per-response redaction for Model now lives in dto.NewModelResponse. The
// previous RedactSensitiveData method has been removed because handlers must
// always serialize through the DTO, where the secret fields don't even
// exist; a runtime mutator on the entity is both redundant and a footgun
// (mutates an entity that other code may still be using).

// ModelIDMaxLen is the upper bound on `models.id`. Matches the actual
// schema width on both PostgreSQL (varchar(64) in migrations/versioned/
// 000000_init.up.sql) and SQLite (varchar(64) in migrations/sqlite/
// 000000_init.up.sql). Loaders that accept user-provided ids (e.g. the
// built-in models YAML loader) must reject anything longer to avoid a
// "value too long for type" failure at INSERT time.
const ModelIDMaxLen = 64

// DefaultBuiltinModelTenantID is the tenant id that built-in models are
// assigned to when YAML does not specify one. Kept in sync with the seed
// value of tenants_id_seq in migrations/versioned/000000_init.up.sql
// (and the equivalent SQLite init); changing one without the other will
// break visibility of built-in models for the default tenant.
const DefaultBuiltinModelTenantID uint64 = 10000

// Model represents the AI model
type Model struct {
	// Unique identifier of the model. The actual DB schema width is
	// varchar(64) on both PostgreSQL and SQLite (see ModelIDMaxLen);
	// GORM's struct tag is documented to match so AutoMigrate paths
	// produce the same shape.
	ID string `yaml:"id"          json:"id"          gorm:"type:varchar(64);primaryKey"`
	// Workspace ID
	TenantID uint64 `yaml:"tenant_id"   json:"tenant_id"`
	// Name of the model
	Name string `yaml:"name"        json:"name"`
	// Optional user-facing display name. Runtime calls still use Name.
	DisplayName string `yaml:"display_name" json:"display_name" gorm:"type:varchar(255);default:''"`
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
	// ManagedBy identifies which subsystem owns this row's lifecycle.
	// Empty / "" = manually created (UI / API / hand-written SQL); the YAML
	// builtin-models loader leaves these untouched.
	// "yaml" = declared in config/builtin_models.yaml; on every startup the
	// loader UPSERTs the YAML set and soft-deletes YAML-managed rows whose
	// id is no longer present in the file. Future origins (e.g. "helm",
	// "operator") can claim their own slice without interfering.
	ManagedBy string `yaml:"managed_by"  json:"managed_by,omitempty"  gorm:"type:varchar(32);default:''"`
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
// Encrypts APIKey and AppSecret before persisting to database (value receiver = no memory pollution).
func (c ModelParameters) Value() (driver.Value, error) {
	if key := utils.GetAESKey(); key != nil {
		if c.APIKey != "" {
			if encrypted, err := utils.EncryptAESGCM(c.APIKey, key); err == nil {
				c.APIKey = encrypted
			}
		}
		if c.AppSecret != "" {
			if encrypted, err := utils.EncryptAESGCM(c.AppSecret, key); err == nil {
				c.AppSecret = encrypted
			}
		}
	}
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface, used to convert database value to ModelParameters.
// Decrypts APIKey and AppSecret after loading from database; legacy plaintext is returned as-is.
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
	// Lenient decrypt: a row with broken ciphertext (key rotated/removed)
	// must still load — otherwise a single failure breaks ListModels and
	// the user can't even see which model needs re-credentialing.
	if plain, ok := utils.DecryptStoredSecretLenient(c.APIKey); ok {
		c.APIKey = plain
	} else {
		log.Printf("[crypto] model parameters api_key: decrypt failed (SYSTEM_AES_KEY missing/rotated?), treating as unconfigured")
		c.APIKey = ""
	}
	if plain, ok := utils.DecryptStoredSecretLenient(c.AppSecret); ok {
		c.AppSecret = plain
	} else {
		log.Printf("[crypto] model parameters app_secret: decrypt failed (SYSTEM_AES_KEY missing/rotated?), treating as unconfigured")
		c.AppSecret = ""
	}
	return nil
}

// BeforeCreate is a GORM hook that runs before creating a new model record.
// Generates a UUID only when the caller has not supplied an ID — preserves
// stable IDs declared in built-in model YAML config while keeping the
// existing UUID behaviour for API-driven model creation.
func (m *Model) BeforeCreate(tx *gorm.DB) (err error) {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	return nil
}
