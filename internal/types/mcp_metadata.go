package types

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

var (
	// ErrMCPServiceNotFound is returned when the workspace cannot see this MCP service.
	ErrMCPServiceNotFound = errors.New("MCP service not found")
	// ErrMCPOAuthPrincipalRequired is returned when an OAuth directory is read without a user.
	ErrMCPOAuthPrincipalRequired = errors.New("OAuth metadata requires an authenticated principal")
	// ErrMCPMetadataStorage is returned when the metadata repository is unavailable.
	ErrMCPMetadataStorage = errors.New("MCP metadata storage is unavailable")
	// ErrMCPMetadataConnectionChanged is returned when a refresh races a connection edit.
	ErrMCPMetadataConnectionChanged = errors.New("MCP connection changed during refresh")
	// ErrMCPMetadataTooLarge is returned when the serialized directory exceeds 8 MiB.
	ErrMCPMetadataTooLarge = errors.New("MCP metadata exceeds the 8 MiB storage limit")
	// ErrMCPMetadataInvalidTools is returned when the listed directory has empty or duplicate names.
	ErrMCPMetadataInvalidTools = errors.New("MCP directory contains empty or duplicate tool names")
)

// MCPMetadata is a complete, explicitly synchronized directory. OAuth snapshots
// are scoped to the authorizing principal; they must never become tenant-wide.
type MCPMetadata struct {
	TenantID          uint64     `json:"-"                  gorm:"primaryKey;autoIncrement:false"`
	ServiceID         string     `json:"service_id"         gorm:"primaryKey;type:varchar(36)"`
	Principal         string     `json:"-"                  gorm:"primaryKey;type:varchar(255)"`
	ConfigFingerprint string     `json:"-"                  gorm:"type:varchar(64);not null"`
	Tools             []*MCPTool `json:"tools"              gorm:"serializer:json;type:jsonb;not null"`
	Instructions      string     `json:"instructions"       gorm:"type:text"`
	ServerName        string     `json:"server_name"`
	ServerVersion     string     `json:"server_version"`
	ServerDescription string     `json:"server_description" gorm:"type:text"`
	SyncedAt          time.Time  `json:"synced_at"`
	Stale             bool       `json:"stale"              gorm:"-"`
}

// MCPMetadataSummary is the list-card view of a snapshot: counts only, no tool payloads.
type MCPMetadataSummary struct {
	ServiceID         string    `gorm:"column:service_id"`
	Principal         string    `gorm:"column:principal"`
	ConfigFingerprint string    `gorm:"column:config_fingerprint"`
	ToolCount         int       `gorm:"column:tool_count"`
	SyncedAt          time.Time `gorm:"column:synced_at"`
	ServerName        string    `gorm:"column:server_name"`
}

// TableName returns the directory snapshot table.
func (MCPMetadata) TableName() string { return "mcp_metadata" }

// MCPConfigFingerprint excludes display text and enabled state: editing documentation does not change
// upstream identity. Secrets affect identity but only their digest is stored.
func MCPConfigFingerprint(s *MCPService) string {
	raw, _ := json.Marshal(struct {
		Transport MCPTransportType
		URL       *string
		Headers   MCPHeaders
		Auth      *MCPAuthConfig
		Stdio     *MCPStdioConfig
		Env       MCPEnvVars
	}{s.TransportType, s.URL, s.Headers, s.AuthConfig, s.StdioConfig, s.EnvVars})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
