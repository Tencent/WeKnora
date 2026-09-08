package types

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
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
