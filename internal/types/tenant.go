package types

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"
)

// retrieverEngineMapping maps RETRIEVE_DRIVER values to retriever engine configurations
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
	"milvus": {
		{RetrieverType: VectorRetrieverType, RetrieverEngineType: MilvusRetrieverEngineType},
		{RetrieverType: KeywordsRetrieverType, RetrieverEngineType: MilvusRetrieverEngineType},
	},
	"sqlite": {
		{RetrieverType: KeywordsRetrieverType, RetrieverEngineType: SQLiteRetrieverEngineType},
		{RetrieverType: VectorRetrieverType, RetrieverEngineType: SQLiteRetrieverEngineType},
	},
}

// GetRetrieverEngineMapping returns the retriever engine mapping
// This allows other packages to access the driver capabilities
func GetRetrieverEngineMapping() map[string][]RetrieverEngineParams {
	return retrieverEngineMapping
}

// GetDefaultRetrieverEngines returns the default retriever engines based on RETRIEVE_DRIVER env
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

// Tenant represents the tenant
type Tenant struct {
	// ID
	ID uint64 `yaml:"id"                  json:"id"                  gorm:"primaryKey"`
	// Name
	Name string `yaml:"name"                json:"name"`
	// Description
	Description string `yaml:"description"         json:"description"`
	// API key
	APIKey string `yaml:"api_key"             json:"api_key"`
	// Status
	Status string `yaml:"status"              json:"status"              gorm:"default:'active'"`
	// Retriever engines
	RetrieverEngines RetrieverEngines `yaml:"retriever_engines"   json:"retriever_engines"   gorm:"type:json"`
	// Business
	Business string `yaml:"business"            json:"business"`
	// Storage quota (Bytes), default is 10GB, including vector, original file, text, index, etc.
	StorageQuota int64 `yaml:"storage_quota"       json:"storage_quota"       gorm:"default:10737418240"`
	// Storage used (Bytes)
	StorageUsed int64 `yaml:"storage_used"        json:"storage_used"        gorm:"default:0"`
	// Deprecated: AgentConfig is deprecated, use CustomAgent (builtin-smart-reasoning) config instead.
	// This field is kept for backward compatibility and will be removed in future versions.
	AgentConfig *AgentConfig `yaml:"agent_config"        json:"agent_config"        gorm:"type:jsonb"`
	// Global Context configuration for this tenant (default for all sessions)
	ContextConfig *ContextConfig `yaml:"context_config"      json:"context_config"      gorm:"type:jsonb"`
	// Global WebSearch configuration for this tenant
	WebSearchConfig *WebSearchConfig `yaml:"web_search_config"   json:"web_search_config"   gorm:"type:jsonb"`
	// Deprecated: ConversationConfig is deprecated, use CustomAgent (builtin-quick-answer) config instead.
	// This field is kept for backward compatibility and will be removed in future versions.
	ConversationConfig *ConversationConfig `yaml:"conversation_config" json:"conversation_config" gorm:"type:jsonb"`
	// Parser engine config overrides (MinerU endpoint, API key, etc.). Used when parsing documents; overrides env.
	ParserEngineConfig *ParserEngineConfig `yaml:"parser_engine_config" json:"parser_engine_config" gorm:"type:jsonb"`
	// Storage engine config: parameters for Local, MinIO, COS. Used for document/file storage and docreader.
	StorageEngineConfig *StorageEngineConfig `yaml:"storage_engine_config" json:"storage_engine_config" gorm:"type:jsonb"`
	// Creation time
	CreatedAt time.Time `yaml:"created_at"          json:"created_at"`
	// Last updated time
	UpdatedAt time.Time `yaml:"updated_at"          json:"updated_at"`
	// Deletion time
	DeletedAt gorm.DeletedAt `yaml:"deleted_at"          json:"deleted_at"          gorm:"index"`
}

// RetrieverEngines represents the retriever engines for a tenant
type RetrieverEngines struct {
	Engines []RetrieverEngineParams `yaml:"engines" json:"engines" gorm:"type:json"`
}

// GetEffectiveEngines returns the tenant's engines if configured, otherwise returns system defaults
func (t *Tenant) GetEffectiveEngines() []RetrieverEngineParams {
	if len(t.RetrieverEngines.Engines) > 0 {
		return t.RetrieverEngines.Engines
	}
	return GetDefaultRetrieverEngines()
}

// BeforeCreate is a hook function that is called before creating a tenant
func (t *Tenant) BeforeCreate(tx *gorm.DB) error {
	if t.RetrieverEngines.Engines == nil {
		t.RetrieverEngines.Engines = []RetrieverEngineParams{}
	}
	return nil
}

// Value implements the driver.Valuer interface, used to convert RetrieverEngines to database value
func (c RetrieverEngines) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface, used to convert database value to RetrieverEngines
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

// ConversationConfig represents the conversation configuration for normal mode
type ConversationConfig struct {
	// Prompt is the system prompt for normal mode
	Prompt string `json:"prompt"`
	// ContextTemplate is the prompt template for summarizing retrieval results
	ContextTemplate string `json:"context_template"`
	// Temperature controls the randomness of the model output
	Temperature float64 `json:"temperature"`
	// MaxTokens is the maximum number of tokens to generate
	MaxCompletionTokens int `json:"max_completion_tokens"`

	// Retrieval & strategy parameters
	MaxRounds            int     `json:"max_rounds"`
	EmbeddingTopK        int     `json:"embedding_top_k"`
	KeywordThreshold     float64 `json:"keyword_threshold"`
	VectorThreshold      float64 `json:"vector_threshold"`
	RerankTopK           int     `json:"rerank_top_k"`
	RerankThreshold      float64 `json:"rerank_threshold"`
	EnableRewrite        bool    `json:"enable_rewrite"`
	EnableQueryExpansion bool    `json:"enable_query_expansion"`

	// Model configuration
	SummaryModelID string `json:"summary_model_id"`
	RerankModelID  string `json:"rerank_model_id"`

	// Fallback strategy
	FallbackStrategy string `json:"fallback_strategy"`
	FallbackResponse string `json:"fallback_response"`
	FallbackPrompt   string `json:"fallback_prompt"`

	// Rewrite prompts
	RewritePromptSystem string `json:"rewrite_prompt_system"`
	RewritePromptUser   string `json:"rewrite_prompt_user"`
}

// Value implements the driver.Valuer interface, used to convert ConversationConfig to database value
func (c *ConversationConfig) Value() (driver.Value, error) {
	if c == nil {
		return nil, nil
	}
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface, used to convert database value to ConversationConfig
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

// ParserEngineConfig holds tenant-level overrides for document parser engines (e.g. MinerU endpoint, API key).
// These values take precedence over environment variables when parsing documents.
type ParserEngineConfig struct {
	DocReaderAddr    string `json:"docreader_addr"`      // 文档解析服务地址
	MinerUEndpoint   string `json:"mineru_endpoint"`     // MinerU 自建服务端点
	MinerUAPIKey     string `json:"mineru_api_key"`      // MinerU 云 API Key
	MinerUAPIBaseURL string `json:"mineru_api_base_url"` // MinerU 云 API Base URL（可选）

	// MinerU 自建解析参数
	MinerUModel         string `json:"mineru_model,omitempty"` // backend: pipeline, vlm-*, hybrid-*
	MinerUEnableFormula *bool  `json:"mineru_enable_formula,omitempty"`
	MinerUEnableTable   *bool  `json:"mineru_enable_table,omitempty"`
	MinerUEnableOCR     *bool  `json:"mineru_enable_ocr,omitempty"`
	MinerULanguage      string `json:"mineru_language,omitempty"`

	// MinerU 云 API 解析参数
	MinerUCloudModel         string `json:"mineru_cloud_model,omitempty"` // model_version: pipeline, vlm, MinerU-HTML
	MinerUCloudEnableFormula *bool  `json:"mineru_cloud_enable_formula,omitempty"`
	MinerUCloudEnableTable   *bool  `json:"mineru_cloud_enable_table,omitempty"`
	MinerUCloudEnableOCR     *bool  `json:"mineru_cloud_enable_ocr,omitempty"`
	MinerUCloudLanguage      string `json:"mineru_cloud_language,omitempty"`
}

// ToOverridesMap returns a map suitable for ParserEngineOverrides in parse requests.
// Keys are snake_case (mineru_endpoint, mineru_api_key, mineru_api_base_url).
func (c *ParserEngineConfig) ToOverridesMap() map[string]string {
	if c == nil {
		return nil
	}
	m := make(map[string]string)
	if c.MinerUEndpoint != "" {
		m["mineru_endpoint"] = c.MinerUEndpoint
	}
	if c.MinerUAPIKey != "" {
		m["mineru_api_key"] = c.MinerUAPIKey
	}
	if c.MinerUAPIBaseURL != "" {
		m["mineru_api_base_url"] = c.MinerUAPIBaseURL
	}
	if c.MinerUModel != "" {
		m["mineru_model"] = c.MinerUModel
	}
	if c.MinerUEnableFormula != nil {
		m["mineru_enable_formula"] = fmt.Sprintf("%v", *c.MinerUEnableFormula)
	}
	if c.MinerUEnableTable != nil {
		m["mineru_enable_table"] = fmt.Sprintf("%v", *c.MinerUEnableTable)
	}
	if c.MinerUEnableOCR != nil {
		m["mineru_enable_ocr"] = fmt.Sprintf("%v", *c.MinerUEnableOCR)
	}
	if c.MinerULanguage != "" {
		m["mineru_language"] = c.MinerULanguage
	}
	if c.MinerUCloudModel != "" {
		m["mineru_cloud_model"] = c.MinerUCloudModel
	}
	if c.MinerUCloudEnableFormula != nil {
		m["mineru_cloud_enable_formula"] = fmt.Sprintf("%v", *c.MinerUCloudEnableFormula)
	}
	if c.MinerUCloudEnableTable != nil {
		m["mineru_cloud_enable_table"] = fmt.Sprintf("%v", *c.MinerUCloudEnableTable)
	}
	if c.MinerUCloudEnableOCR != nil {
		m["mineru_cloud_enable_ocr"] = fmt.Sprintf("%v", *c.MinerUCloudEnableOCR)
	}
	if c.MinerUCloudLanguage != "" {
		m["mineru_cloud_language"] = c.MinerUCloudLanguage
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// Value implements the driver.Valuer interface for ParserEngineConfig
func (c *ParserEngineConfig) Value() (driver.Value, error) {
	if c == nil {
		return nil, nil
	}
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface for ParserEngineConfig
func (c *ParserEngineConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// StorageEngineConfig holds tenant-level storage engine parameters for Local, MinIO, COS, and TOS.
// Knowledge bases select which provider to use; parameters are read from here.
type StorageEngineConfig struct {
	DefaultProvider string             `json:"default_provider"` // "local", "minio", "cos", "tos"
	Local           *LocalEngineConfig `json:"local,omitempty"`
	MinIO           *MinIOEngineConfig `json:"minio,omitempty"`
	COS             *COSEngineConfig   `json:"cos,omitempty"`
	TOS             *TOSEngineConfig   `json:"tos,omitempty"`
}

// LocalEngineConfig is for local file system storage (single-machine deployment only).
type LocalEngineConfig struct {
	PathPrefix string `json:"path_prefix"`
}

// MinIOEngineConfig is for MinIO/S3-compatible object storage.
// Mode "docker" uses env vars for endpoint/credentials; "remote" uses the fields below.
type MinIOEngineConfig struct {
	Mode            string `json:"mode"` // "docker" or "remote"
	Endpoint        string `json:"endpoint"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	BucketName      string `json:"bucket_name"`
	UseSSL          bool   `json:"use_ssl"`
	PathPrefix      string `json:"path_prefix"`
}

// COSEngineConfig is for Tencent Cloud COS.
type COSEngineConfig struct {
	SecretID   string `json:"secret_id"`
	SecretKey  string `json:"secret_key"`
	Region     string `json:"region"`
	BucketName string `json:"bucket_name"`
	AppID      string `json:"app_id"`
	PathPrefix string `json:"path_prefix"`
}

// TOSEngineConfig is for Volcengine TOS (火山引擎对象存储).
type TOSEngineConfig struct {
	Endpoint   string `json:"endpoint"`
	Region     string `json:"region"`
	AccessKey  string `json:"access_key"`
	SecretKey  string `json:"secret_key"`
	BucketName string `json:"bucket_name"`
	PathPrefix string `json:"path_prefix"`
}

// Value implements the driver.Valuer interface for StorageEngineConfig
func (c *StorageEngineConfig) Value() (driver.Value, error) {
	if c == nil {
		return nil, nil
	}
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface for StorageEngineConfig
func (c *StorageEngineConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}
