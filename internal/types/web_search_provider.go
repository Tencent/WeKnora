package types

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// WebSearchProviderType represents the type of web search provider
type WebSearchProviderType string

const (
	WebSearchProviderTypeBing       WebSearchProviderType = "bing"
	WebSearchProviderTypeGoogle     WebSearchProviderType = "google"
	WebSearchProviderTypeDuckDuckGo WebSearchProviderType = "duckduckgo"
	WebSearchProviderTypeTavily     WebSearchProviderType = "tavily"
	WebSearchProviderTypeOllama     WebSearchProviderType = "ollama"
)

// WebSearchProviderEntity represents a configured web search provider instance for a tenant.
// This is a CRUD entity stored in the database, similar to the Model entity.
// Each tenant can create multiple provider configurations (e.g., "Production Bing", "Test Google").
// Agents reference these by ID.
type WebSearchProviderEntity struct {
	// Unique identifier (UUID, auto-generated)
	ID string `yaml:"id" json:"id" gorm:"type:varchar(36);primaryKey"`
	// Tenant ID for scoping
	TenantID uint64 `yaml:"tenant_id" json:"tenant_id"`
	// User-friendly name, e.g., "Production Bing Search"
	Name string `yaml:"name" json:"name" gorm:"type:varchar(255);not null"`
	// Provider type: bing, google, duckduckgo, tavily
	Provider WebSearchProviderType `yaml:"provider" json:"provider" gorm:"type:varchar(50);not null"`
	// Description
	Description string `yaml:"description" json:"description" gorm:"type:text"`
	// Provider-specific parameters (API key, engine ID, etc.) stored as encrypted JSON
	Parameters WebSearchProviderParameters `yaml:"parameters" json:"parameters" gorm:"type:json"`
	// Whether this is the default provider for the tenant
	IsDefault bool `yaml:"is_default" json:"is_default" gorm:"default:false"`
	// Timestamps
	CreatedAt time.Time      `yaml:"created_at" json:"created_at"`
	UpdatedAt time.Time      `yaml:"updated_at" json:"updated_at"`
	DeletedAt gorm.DeletedAt `yaml:"deleted_at" json:"deleted_at" gorm:"index"`
}

// TableName returns the table name for WebSearchProviderEntity
func (WebSearchProviderEntity) TableName() string {
	return "web_search_providers"
}

// BeforeCreate is a GORM hook that runs before creating a new record.
// Automatically generates a UUID for new providers.
func (e *WebSearchProviderEntity) BeforeCreate(tx *gorm.DB) (err error) {
	if e.ID == "" {
		e.ID = uuid.New().String()
	}
	return nil
}

// WebSearchProviderParameters holds provider-specific configuration.
// API keys are encrypted at rest using AES-GCM.
// BaseURL is intentionally NOT included — each provider type uses a hardcoded
// official API endpoint to prevent SSRF attacks.
type WebSearchProviderParameters struct {
	// API key for the search provider (encrypted in DB)
	APIKey string `yaml:"api_key" json:"api_key,omitempty"`
	// Google Custom Search Engine ID (only for Google provider)
	EngineID string `yaml:"engine_id" json:"engine_id,omitempty"`
	// Provider-specific extra configuration for future extensibility
	ExtraConfig map[string]string `yaml:"extra_config" json:"extra_config,omitempty"`
}

// Value implements the driver.Valuer interface.
// Encrypts APIKey before persisting to database.
func (p WebSearchProviderParameters) Value() (driver.Value, error) {
	if key := utils.GetAESKey(); key != nil && p.APIKey != "" {
		if encrypted, err := utils.EncryptAESGCM(p.APIKey, key); err == nil {
			p.APIKey = encrypted
		}
	}
	return json.Marshal(p)
}

// Scan implements the sql.Scanner interface.
// Decrypts APIKey after loading from database.
func (p *WebSearchProviderParameters) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	if err := json.Unmarshal(b, p); err != nil {
		return err
	}
	if key := utils.GetAESKey(); key != nil && p.APIKey != "" {
		if decrypted, err := utils.DecryptAESGCM(p.APIKey, key); err == nil {
			p.APIKey = decrypted
		}
	}
	return nil
}

// WebSearchProviderTypeInfo describes the metadata of a provider type.
// Used by the GET /types endpoint so the frontend can dynamically render forms.
type WebSearchProviderTypeInfo struct {
	// Provider type identifier
	ID string `json:"id"`
	// Human-readable name
	Name string `json:"name"`
	// Whether the provider requires an API key
	RequiresAPIKey bool `json:"requires_api_key"`
	// Whether the provider requires an engine ID (e.g., Google CSE)
	RequiresEngineID bool `json:"requires_engine_id"`
	// Description
	Description string `json:"description"`
	// URL to the provider's official website or documentation for obtaining credentials
	DocsURL string `json:"docs_url,omitempty"`
}

// GetWebSearchProviderTypes returns metadata for all supported provider types.
func GetWebSearchProviderTypes() []WebSearchProviderTypeInfo {
	return []WebSearchProviderTypeInfo{
		{
			ID:             "duckduckgo",
			Name:           "DuckDuckGo",
			RequiresAPIKey: false,
			Description:    "DuckDuckGo Search (free, no API key required)",
			DocsURL:        "https://duckduckgo.com/",
		},
		{
			ID:             "bing",
			Name:           "Bing",
			RequiresAPIKey: true,
			Description:    "Bing Search API (requires API key from Azure)",
			DocsURL:        "https://learn.microsoft.com/en-us/bing/search-apis/bing-web-search/overview",
		},
		{
			ID:               "google",
			Name:             "Google",
			RequiresAPIKey:   true,
			RequiresEngineID: true,
			Description:      "Google Custom Search API (requires API key and engine ID)",
			DocsURL:          "https://developers.google.com/custom-search/v1/overview",
		},
		{
			ID:             "tavily",
			Name:           "Tavily",
			RequiresAPIKey: true,
			Description:    "Tavily Search API (requires API key)",
			DocsURL:        "https://tavily.com/",
		},
		{
			ID:             "ollama",
			Name:           "Ollama Web Search",
			RequiresAPIKey: true,
			Description:    "Ollama Cloud web search (requires Ollama API key)",
			DocsURL:        "https://docs.ollama.com/capabilities/web-search",
		},
	}
}
