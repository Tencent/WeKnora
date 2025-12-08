package types

import (
	"database/sql/driver"
	"encoding/json"
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
	// Global Agent configuration for this tenant (default for all sessions)
	AgentConfig *AgentConfig `yaml:"agent_config"        json:"agent_config"        gorm:"type:jsonb"`
	// Global Context configuration for this tenant (default for all sessions)
	ContextConfig *ContextConfig `yaml:"context_config"      json:"context_config"      gorm:"type:jsonb"`
	// Global WebSearch configuration for this tenant
	WebSearchConfig *WebSearchConfig `yaml:"web_search_config"   json:"web_search_config"   gorm:"type:jsonb"`
	// Global Conversation configuration for this tenant (default for normal mode sessions)
	ConversationConfig *ConversationConfig `yaml:"conversation_config" json:"conversation_config" gorm:"type:jsonb"`
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
	UseCustomSystemPrompt    bool   `json:"use_custom_system_prompt"`
	Prompt                   string `json:"prompt"`
	UseCustomContextTemplate bool   `json:"use_custom_context_template"`
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
