package types

import (
	"time"

	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// MCPOAuthClient stores the OAuth client credentials obtained for an MCP
// service. For servers that support RFC 7591 Dynamic Client Registration the
// client_id (and optional client_secret) is registered once per service and
// reused across all users of that service, avoiding a registration round-trip
// on every authorization.
//
// One row per (tenant_id, service_id). The client_secret is encrypted at rest
// (AES-256-GCM) when SYSTEM_AES_KEY is configured.
type MCPOAuthClient struct {
	ID           string    `json:"id"            gorm:"type:varchar(36);primaryKey"`
	TenantID     uint64    `json:"tenant_id"     gorm:"not null;uniqueIndex:idx_mcp_oauth_clients_tenant_svc"`
	ServiceID    string    `json:"service_id"    gorm:"type:varchar(36);not null;uniqueIndex:idx_mcp_oauth_clients_tenant_svc;index"`
	ClientID     string    `json:"client_id"     gorm:"type:varchar(512);not null"`
	ClientSecret string    `json:"-"             gorm:"type:text"`
	RedirectURI  string    `json:"redirect_uri"  gorm:"type:varchar(1024)"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TableName pins the table name. GORM's default naming would otherwise turn
// "MCPOAuthClient" into "mcpo_auth_clients" (treating "MCPO" as one token),
// which does not match the migration table "mcp_oauth_clients".
func (MCPOAuthClient) TableName() string { return "mcp_oauth_clients" }

// BeforeCreate sets the primary key and encrypts the client secret.
func (m *MCPOAuthClient) BeforeCreate(tx *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	m.encryptSecret()
	return nil
}

// BeforeSave re-encrypts the secret on update paths.
func (m *MCPOAuthClient) BeforeSave(tx *gorm.DB) error {
	m.encryptSecret()
	return nil
}

// AfterFind decrypts the client secret after loading.
func (m *MCPOAuthClient) AfterFind(tx *gorm.DB) error {
	if plain, ok := utils.DecryptStoredSecretLenient(m.ClientSecret); ok {
		m.ClientSecret = plain
	} else {
		m.ClientSecret = ""
	}
	return nil
}

func (m *MCPOAuthClient) encryptSecret() {
	if m.ClientSecret == "" {
		return
	}
	if key := utils.GetAESKey(); key != nil {
		if enc, err := utils.EncryptAESGCM(m.ClientSecret, key); err == nil {
			m.ClientSecret = enc
		}
	}
}

// MCPOAuthToken stores a per-user OAuth token for an MCP service. The agent
// connects to the MCP server on behalf of the invoking user, so tokens are
// isolated by (tenant_id, user_id, service_id).
//
// AccessToken and RefreshToken are encrypted at rest (AES-256-GCM) when
// SYSTEM_AES_KEY is configured.
type MCPOAuthToken struct {
	ID           string    `json:"id"            gorm:"type:varchar(36);primaryKey"`
	TenantID     uint64    `json:"tenant_id"     gorm:"not null;uniqueIndex:idx_mcp_oauth_tokens_tenant_user_svc"`
	UserID       string    `json:"user_id"       gorm:"type:varchar(64);not null;uniqueIndex:idx_mcp_oauth_tokens_tenant_user_svc;index"`
	ServiceID    string    `json:"service_id"    gorm:"type:varchar(36);not null;uniqueIndex:idx_mcp_oauth_tokens_tenant_user_svc;index"`
	AccessToken  string    `json:"-"             gorm:"type:text"`
	RefreshToken string    `json:"-"             gorm:"type:text"`
	TokenType    string    `json:"token_type"    gorm:"type:varchar(32)"`
	ExpiresAt    time.Time `json:"expires_at"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TableName pins the table name (see MCPOAuthClient.TableName); the default
// would be "mcpo_auth_tokens" instead of the migration's "mcp_oauth_tokens".
func (MCPOAuthToken) TableName() string { return "mcp_oauth_tokens" }

// BeforeCreate sets the primary key and encrypts secrets.
func (m *MCPOAuthToken) BeforeCreate(tx *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	m.encryptSecrets()
	return nil
}

// BeforeSave re-encrypts secrets on update paths.
func (m *MCPOAuthToken) BeforeSave(tx *gorm.DB) error {
	m.encryptSecrets()
	return nil
}

// AfterFind decrypts secrets after loading.
func (m *MCPOAuthToken) AfterFind(tx *gorm.DB) error {
	if plain, ok := utils.DecryptStoredSecretLenient(m.AccessToken); ok {
		m.AccessToken = plain
	} else {
		m.AccessToken = ""
	}
	if plain, ok := utils.DecryptStoredSecretLenient(m.RefreshToken); ok {
		m.RefreshToken = plain
	} else {
		m.RefreshToken = ""
	}
	return nil
}

func (m *MCPOAuthToken) encryptSecrets() {
	key := utils.GetAESKey()
	if key == nil {
		return
	}
	if m.AccessToken != "" {
		if enc, err := utils.EncryptAESGCM(m.AccessToken, key); err == nil {
			m.AccessToken = enc
		}
	}
	if m.RefreshToken != "" {
		if enc, err := utils.EncryptAESGCM(m.RefreshToken, key); err == nil {
			m.RefreshToken = enc
		}
	}
}
