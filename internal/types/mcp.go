package types

import (
	"database/sql/driver"
	"encoding/json"
	"time"

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
	TenantID       uint64             `json:"tenant_id"              gorm:"index"`
	Name           string             `json:"name"                   gorm:"type:varchar(255);not null"`
	Description    string             `json:"description"            gorm:"type:text"`
	Enabled        bool               `json:"enabled"                gorm:"default:true;index"`
	TransportType  MCPTransportType   `json:"transport_type"         gorm:"type:varchar(50);not null"`
	URL            *string            `json:"url,omitempty"          gorm:"type:varchar(512)"` // Optional: required for SSE/HTTP Streamable
	Headers        MCPHeaders         `json:"headers"                gorm:"type:json"`
	AuthConfig     *MCPAuthConfig     `json:"auth_config"            gorm:"type:json"`
	AdvancedConfig *MCPAdvancedConfig `json:"advanced_config"        gorm:"type:json"`
	StdioConfig    *MCPStdioConfig    `json:"stdio_config,omitempty" gorm:"type:json"` // Required for stdio transport
	EnvVars        MCPEnvVars         `json:"env_vars,omitempty"     gorm:"type:json"` // Environment variables for stdio
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
	DeletedAt      gorm.DeletedAt     `json:"deleted_at"             gorm:"index"`
}

// MCPHeaders represents HTTP headers as a map
type MCPHeaders map[string]string

// MCPAuthConfig represents authentication configuration for MCP service
type MCPAuthConfig struct {
	APIKey        string            `json:"api_key,omitempty"`
	Token         string            `json:"token,omitempty"`
	CustomHeaders map[string]string `json:"custom_headers,omitempty"`
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

// Value implements driver.Valuer interface for MCPAuthConfig
func (c *MCPAuthConfig) Value() (driver.Value, error) {
	if c == nil {
		return nil, nil
	}
	return json.Marshal(c)
}

// Scan implements sql.Scanner interface for MCPAuthConfig
func (c *MCPAuthConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
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

// MaskSensitiveData masks sensitive information in the MCP service for display
func (m *MCPService) MaskSensitiveData() {
	if m.AuthConfig != nil {
		if m.AuthConfig.APIKey != "" {
			m.AuthConfig.APIKey = maskString(m.AuthConfig.APIKey)
		}
		if m.AuthConfig.Token != "" {
			m.AuthConfig.Token = maskString(m.AuthConfig.Token)
		}
	}
}

// maskString masks a string, showing only first 4 and last 4 characters
func maskString(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}
