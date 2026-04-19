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
	WebSearchProviderTypeBaidu      WebSearchProviderType = "baidu"
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
//
// ClearAPIKey is a write-only flag used in Update requests to explicitly
// remove the stored API key. It is never persisted and never returned in
// responses. Without this flag an empty APIKey in an Update request is
// interpreted as "no change", so this is the only way to intentionally clear
// a stored key without deleting the entire provider record.
type WebSearchProviderParameters struct {
	// API key for the search provider (encrypted in DB)
	APIKey string `yaml:"api_key" json:"api_key,omitempty"`
	// Google Custom Search Engine ID (only for Google provider)
	EngineID string `yaml:"engine_id" json:"engine_id,omitempty"`
	// Optional HTTP/HTTPS proxy URL for outbound search requests (e.g. http://host:port); validated with utils.ValidateURLForSSRF.
	// Does not replace the search API endpoint; only tunnels traffic to the official APIs.
	ProxyURL string `yaml:"proxy_url" json:"proxy_url,omitempty"`
	// Provider-specific extra configuration for future extensibility
	ExtraConfig map[string]string `yaml:"extra_config" json:"extra_config,omitempty"`

	// Write-only clear flag — never persisted, never returned.
	ClearAPIKey bool `yaml:"-" json:"clear_api_key,omitempty" gorm:"-"`
}

// RedactSensitiveData replaces APIKey with RedactedSecretPlaceholder when a
// value is set. Empty values stay empty so the frontend can distinguish
// "set (hidden)" from "not set". Mutates the receiver in place.
func (e *WebSearchProviderEntity) RedactSensitiveData() {
	if e.Parameters.APIKey != "" {
		e.Parameters.APIKey = RedactedSecretPlaceholder
	}
}

// MergeUpdate applies write-only secret merge semantics for Update requests:
//
//   - ClearAPIKey takes precedence — when true, APIKey becomes empty
//   - Otherwise APIKey uses PreserveIfRedacted: empty or the redacted
//     placeholder preserves existing, any other value replaces
//   - Non-secret fields (EngineID, ProxyURL, ExtraConfig) are replaced
//     directly from incoming
//   - The write-only ClearAPIKey flag is cleared on the merged result so it
//     never leaks to storage
func (p WebSearchProviderParameters) MergeUpdate(existing WebSearchProviderParameters) WebSearchProviderParameters {
	merged := p
	if p.ClearAPIKey {
		merged.APIKey = ""
	} else {
		merged.APIKey = PreserveIfRedacted(p.APIKey, existing.APIKey)
	}
	merged.ClearAPIKey = false
	return merged
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
	// Whether optional proxy_url in parameters is honored for outbound requests
	SupportsProxy bool `json:"supports_proxy"`
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
			SupportsProxy:  true,
			Description:    "DuckDuckGo Search (free, no API key required)",
			DocsURL:        "https://duckduckgo.com/",
		},
		{
			ID:             "bing",
			Name:           "Bing",
			RequiresAPIKey: true,
			SupportsProxy:  true,
			Description:    "Bing Search API (requires API key from Azure)",
			DocsURL:        "https://learn.microsoft.com/en-us/bing/search-apis/bing-web-search/overview",
		},
		{
			ID:               "google",
			Name:             "Google",
			RequiresAPIKey:   true,
			RequiresEngineID: true,
			SupportsProxy:    true,
			Description:      "Google Custom Search API (requires API key and engine ID)",
			DocsURL:          "https://developers.google.com/custom-search/v1/overview",
		},
		{
			ID:             "tavily",
			Name:           "Tavily",
			RequiresAPIKey: true,
			SupportsProxy:  true,
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
		{
			ID:             "baidu",
			Name:           "Baidu",
			RequiresAPIKey: true,
			Description:    "Baidu AI Search (requires API key from Baidu Cloud)",
			DocsURL:        "https://cloud.baidu.com/doc/AppBuilder/s/qlvEcai0p",
		},
	}
}
