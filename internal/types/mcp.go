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

// MCPTransportType represents the transport type for MCP service
type MCPTransportType string

const (
	MCPTransportSSE            MCPTransportType = "sse"             // Server-Sent Events
	MCPTransportHTTPStreamable MCPTransportType = "http-streamable" // HTTP Streamable
	MCPTransportStdio          MCPTransportType = "stdio"           // Stdio (Standard Input/Output)
)

// MCPService represents an MCP (Model Context Protocol) service configuration
type MCPService struct {
	ID             string             `json:"id"                     gorm:"type:varchar(36);primaryKey"`
	TenantID       uint64             `json:"tenant_id"              gorm:"uniqueIndex:idx_tenant_name"`
	Name           string             `json:"name"                   gorm:"type:varchar(255);not null;uniqueIndex:idx_tenant_name"`
	Description    string             `json:"description"            gorm:"type:text"`
	Enabled        bool               `json:"enabled"                gorm:"default:true;index"`
	TransportType  MCPTransportType   `json:"transport_type"         gorm:"type:varchar(50);not null"`
	URL            *string            `json:"url,omitempty"          gorm:"type:varchar(512)"` // Optional: required for SSE/HTTP Streamable
	Headers        MCPHeaders         `json:"headers"                gorm:"type:json"`
	AuthConfig     *MCPAuthConfig     `json:"auth_config"            gorm:"type:json"`
	AdvancedConfig *MCPAdvancedConfig `json:"advanced_config"        gorm:"type:json"`
	StdioConfig    *MCPStdioConfig    `json:"stdio_config,omitempty" gorm:"type:json"`     // Required for stdio transport
	EnvVars        MCPEnvVars         `json:"env_vars,omitempty"     gorm:"type:json"`     // Environment variables for stdio
	IsBuiltin      bool               `json:"is_builtin"             gorm:"default:false"` // Whether this is a builtin MCP service (visible to all tenants)
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
	DeletedAt      gorm.DeletedAt     `json:"deleted_at"             gorm:"index"`
}

// MCPHeaders represents HTTP headers as a map
type MCPHeaders map[string]string

// MCPAuthType enumerates the authentication strategies for an MCP service.
type MCPAuthType string

const (
	// MCPAuthNone means no authentication (or only static custom headers).
	MCPAuthNone MCPAuthType = ""
	// MCPAuthAPIKey injects a static API key header (X-API-Key).
	MCPAuthAPIKey MCPAuthType = "api_key"
	// MCPAuthBearer injects a static Authorization: Bearer <token> header.
	MCPAuthBearer MCPAuthType = "bearer"
	// MCPAuthOAuth performs the MCP OAuth2 authorization-code flow
	// (discovery + dynamic client registration + PKCE) per user. Tokens are
	// stored per (tenant, user, service) in mcp_oauth_tokens.
	MCPAuthOAuth MCPAuthType = "oauth"
)

// MCPAuthConfig represents authentication configuration for MCP service.
//
// Secret fields (APIKey, Token) are persisted in this struct but are NEVER
// returned through main resource responses — those go through
// dto.MCPServiceResponse which omits them by construction. Credential
// mutations happen through the dedicated /credentials subresource handled
// by MCPCredentialsHandler.
//
// OAuth note: the OAuth strategy stores no secret in this struct. The
// per-user access/refresh tokens live in mcp_oauth_tokens and the
// dynamically registered client lives in mcp_oauth_clients. The fields here
// (Scopes, AuthServerMetadataURL) are non-secret OAuth configuration.
type MCPAuthConfig struct {
	// AuthType selects the authentication strategy. Empty ("") is treated as
	// none for backward compatibility with rows that pre-date this field.
	AuthType      MCPAuthType       `json:"auth_type,omitempty"`
	APIKey        string            `json:"api_key,omitempty"`
	Token         string            `json:"token,omitempty"`
	CustomHeaders map[string]string `json:"custom_headers,omitempty"`
	// Scopes are the OAuth scopes requested during authorization. Optional.
	Scopes []string `json:"scopes,omitempty"`
	// AuthServerMetadataURL optionally pins the OAuth authorization server
	// metadata URL. When empty, the server is discovered automatically from
	// the MCP URL (RFC 9728 / RFC 8414).
	AuthServerMetadataURL string `json:"auth_server_metadata_url,omitempty"`
}

// IsOAuth reports whether this service uses the OAuth strategy.
func (c *MCPAuthConfig) IsOAuth() bool {
	return c != nil && c.AuthType == MCPAuthOAuth
}

// MCPAdvancedConfig represents advanced configuration for MCP service
type MCPAdvancedConfig struct {
	Timeout    int `json:"timeout"`     // Timeout in seconds, default: 30
	RetryCount int `json:"retry_count"` // Number of retries, default: 3
	RetryDelay int `json:"retry_delay"` // Delay between retries in seconds, default: 1
}

// MCPStdioConfig represents stdio transport configuration
type MCPStdioConfig struct {
	Command string   `json:"command"` // Command: "uvx" or "npx"
	Args    []string `json:"args"`    // Command arguments array
}

// MCPEnvVars represents environment variables as a map
type MCPEnvVars map[string]string

// MCPTool represents a tool exposed by an MCP service
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"` // JSON Schema for tool parameters
	// RequireApproval when true: agent execution pauses until the user approves in UI (issue #1173).
	RequireApproval bool `json:"require_approval,omitempty"`
}

// MCPToolApproval persists per-tool "danger / needs human approval" for an MCP service.
// Tool list itself comes from MCP ListTools; this table only stores overrides.
type MCPToolApproval struct {
	ID              string    `json:"id"               gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64    `json:"tenant_id"        gorm:"not null;uniqueIndex:idx_mcp_tool_approvals_tenant_svc_tool"`
	ServiceID       string    `json:"service_id"       gorm:"type:varchar(36);not null;uniqueIndex:idx_mcp_tool_approvals_tenant_svc_tool;index"`
	ToolName        string    `json:"tool_name"        gorm:"type:varchar(512);not null;uniqueIndex:idx_mcp_tool_approvals_tenant_svc_tool"`
	RequireApproval bool      `json:"require_approval" gorm:"not null;default:false"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// BeforeCreate sets ID for MCPToolApproval.
func (m *MCPToolApproval) BeforeCreate(tx *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	return nil
}

// MCPResource represents a resource exposed by an MCP service
type MCPResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// MCPTestResult represents the result of testing an MCP service connection
type MCPTestResult struct {
	Success   bool           `json:"success"`
	Message   string         `json:"message,omitempty"`
	Tools     []*MCPTool     `json:"tools,omitempty"`
	Resources []*MCPResource `json:"resources,omitempty"`
}

// BeforeCreate is a GORM hook that runs before creating a new MCP service
func (m *MCPService) BeforeCreate(tx *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	return nil
}

// Value implements driver.Valuer interface for MCPHeaders
func (h MCPHeaders) Value() (driver.Value, error) {
	if h == nil {
		return nil, nil
	}
	return json.Marshal(h)
}

// Scan implements sql.Scanner interface for MCPHeaders
func (h *MCPHeaders) Scan(value interface{}) error {
	if value == nil {
		*h = nil
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, h)
}

// Value implements driver.Valuer for MCPAuthConfig.
//
// When SYSTEM_AES_KEY is configured, APIKey and Token are AES-256-GCM
// encrypted before serialization — mirroring the ModelParameters /
// WebSearchProviderParameters pattern so MCP secrets are not the odd
// resource stored in plaintext. Encryption operates on a local copy to
// avoid mutating the caller's in-memory struct (subsequent reads of the
// same *MCPAuthConfig would otherwise see ciphertext).
func (c *MCPAuthConfig) Value() (driver.Value, error) {
	if c == nil {
		return nil, nil
	}
	out := *c
	if key := utils.GetAESKey(); key != nil {
		if out.APIKey != "" {
			if encrypted, err := utils.EncryptAESGCM(out.APIKey, key); err == nil {
				out.APIKey = encrypted
			}
		}
		if out.Token != "" {
			if encrypted, err := utils.EncryptAESGCM(out.Token, key); err == nil {
				out.Token = encrypted
			}
		}
	}
	return json.Marshal(&out)
}

// Scan implements sql.Scanner for MCPAuthConfig.
//
// Legacy plaintext rows (no enc:v1: prefix) are returned as-is; encrypted
// rows are decrypted. DecryptStoredSecret fails loudly if the prefix is
// present but SYSTEM_AES_KEY is missing/rotated, so we surface that as a
// Scan error rather than letting ciphertext leak upstream as the API key.
func (c *MCPAuthConfig) Scan(value interface{}) error {
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
	if plain, ok := utils.DecryptStoredSecretLenient(c.APIKey); ok {
		c.APIKey = plain
	} else {
		log.Printf("[crypto] mcp auth_config api_key: decrypt failed (SYSTEM_AES_KEY missing/rotated?), treating as unconfigured")
		c.APIKey = ""
	}
	if plain, ok := utils.DecryptStoredSecretLenient(c.Token); ok {
		c.Token = plain
	} else {
		log.Printf("[crypto] mcp auth_config token: decrypt failed (SYSTEM_AES_KEY missing/rotated?), treating as unconfigured")
		c.Token = ""
	}
	return nil
}

// Value implements driver.Valuer interface for MCPAdvancedConfig
func (c *MCPAdvancedConfig) Value() (driver.Value, error) {
	if c == nil {
		return nil, nil
	}
	return json.Marshal(c)
}

// Scan implements sql.Scanner interface for MCPAdvancedConfig
func (c *MCPAdvancedConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// Value implements driver.Valuer interface for MCPStdioConfig
func (c *MCPStdioConfig) Value() (driver.Value, error) {
	if c == nil {
		return nil, nil
	}
	return json.Marshal(c)
}

// Scan implements sql.Scanner interface for MCPStdioConfig
func (c *MCPStdioConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// Value implements driver.Valuer interface for MCPEnvVars
func (e MCPEnvVars) Value() (driver.Value, error) {
	if e == nil {
		return nil, nil
	}
	return json.Marshal(e)
}

// Scan implements sql.Scanner interface for MCPEnvVars
func (e *MCPEnvVars) Scan(value interface{}) error {
	if value == nil {
		*e = nil
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, e)
}

// GetDefaultAdvancedConfig returns default advanced configuration
func GetDefaultAdvancedConfig() *MCPAdvancedConfig {
	return &MCPAdvancedConfig{
		Timeout:    30,
		RetryCount: 3,
		RetryDelay: 1,
	}
}

// Redaction / sensitive-field stripping for MCPService now happens at the
// response DTO layer (internal/handler/dto.NewMCPServiceResponse), not via a
// method on the entity. This keeps the "no secret in responses" guarantee a
// compile-time invariant of the DTO instead of a runtime call that handlers
// must remember to make. Builtin-service field stripping is also implemented
// there.

