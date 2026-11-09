package types

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// MCPEndpoint is one workspace-owned MCP server surface that external MCP
// clients (Claude Desktop, Cursor, VS Code Copilot, ...) can connect to over
// Streamable HTTP. A workspace may publish any number of endpoints; each one
// carries its own bearer token, knowledge-base scope and tool allowlist, so a
// single WeKnora deployment can serve several narrowly scoped MCP servers
// without running a separate process per workspace.
//
// A space endpoint is the unit of trust: everything a connected client may do
// is derived from this row (see MCPEndpointScope), and the token authenticates
// the endpoint rather than a person. A personal endpoint additionally acts as
// its owning member, so calls are bounded by that member's current role too.
type MCPEndpoint struct {
	ID       string          `json:"id"          gorm:"type:varchar(36);primaryKey"`
	TenantID uint64          `json:"tenant_id"   gorm:"not null;index:idx_mcp_endpoints_tenant"`
	Kind     MCPEndpointKind `json:"kind"        gorm:"type:varchar(16);not null;default:'space'"`
	// UserID owns a personal endpoint; empty for space endpoints.
	UserID      string `json:"user_id"     gorm:"type:varchar(36);not null;default:''"`
	Name        string `json:"name"        gorm:"type:varchar(255);not null;default:''"`
	Description string `json:"description" gorm:"type:text;not null;default:''"`
	Enabled     bool   `json:"enabled"     gorm:"not null;default:true"`
	// TokenHash is the SHA-256 hex digest of the bearer token. The plaintext
	// is shown once on create/rotate and never stored, mirroring
	// TenantAPIKey.KeyHash rather than the plaintext embed publish token.
	TokenHash string `json:"-" gorm:"type:varchar(64);not null;default:'';uniqueIndex:idx_mcp_endpoints_token_hash"`
	// TokenHint is the first few characters of the token so the UI can show
	// "mcp_ab12…" next to an endpoint without revealing the secret.
	TokenHint string `json:"token_hint" gorm:"type:varchar(16);not null;default:''"`
	// KnowledgeBaseIDs bounds every knowledge-facing tool. Empty means every
	// knowledge base the workspace owns or has been granted at call time.
	KnowledgeBaseIDs StringArray `json:"knowledge_base_ids" gorm:"type:jsonb;not null;default:'[]'"`
	// Tools is the allowlist of MCP tool names this endpoint exposes. Names
	// must come from the MCP endpoint tool catalog; unknown names are dropped
	// on write.
	Tools StringArray `json:"tools" gorm:"type:jsonb;not null;default:'[]'"`
	// DefaultAgentID is the agent used by the ask tool when the caller does
	// not name one. Empty falls back to the workspace's builtin agent.
	DefaultAgentID     string         `json:"default_agent_id"      gorm:"type:varchar(36);not null;default:''"`
	RateLimitPerMinute int            `json:"rate_limit_per_minute" gorm:"not null;default:60"`
	LastUsedAt         *time.Time     `json:"last_used_at,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	DeletedAt          gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

// TableName returns the storage table.
func (MCPEndpoint) TableName() string { return "mcp_endpoints" }

// MCPEndpointKind separates admin-published space endpoints from endpoints a
// member creates for their own use.
type MCPEndpointKind string

// Endpoint kinds stored in mcp_endpoints.kind.
const (
	MCPEndpointKindSpace    MCPEndpointKind = "space"
	MCPEndpointKindPersonal MCPEndpointKind = "personal"
)

// MCPEndpointOwner selects the endpoints a management call may see: the
// workspace's space endpoints, or one member's personal endpoints.
type MCPEndpointOwner struct {
	TenantID uint64
	Kind     MCPEndpointKind
	UserID   string
}

// SpaceMCPEndpointOwner addresses the workspace's space endpoints.
func SpaceMCPEndpointOwner(tenantID uint64) MCPEndpointOwner {
	return MCPEndpointOwner{TenantID: tenantID, Kind: MCPEndpointKindSpace}
}

// PersonalMCPEndpointOwner addresses userID's personal endpoints.
func PersonalMCPEndpointOwner(tenantID uint64, userID string) MCPEndpointOwner {
	return MCPEndpointOwner{TenantID: tenantID, Kind: MCPEndpointKindPersonal, UserID: userID}
}

// Owns reports whether ep belongs to this owner.
func (o MCPEndpointOwner) Owns(ep *MCPEndpoint) bool {
	if ep == nil || ep.TenantID != o.TenantID || ep.Kind != o.Kind {
		return false
	}
	return o.Kind != MCPEndpointKindPersonal || (o.UserID != "" && ep.UserID == o.UserID)
}

// IsPersonal reports whether the endpoint acts as its owning member.
func (e *MCPEndpoint) IsPersonal() bool {
	return e != nil && e.Kind == MCPEndpointKindPersonal
}

// MCPEndpointSettings holds workspace-wide MCP endpoint settings that only an
// admin may change.
type MCPEndpointSettings struct {
	TenantID uint64 `json:"tenant_id" gorm:"primaryKey;autoIncrement:false"`
	// PersonalRateLimitPerMinute bounds tool calls per minute across every
	// personal endpoint in the workspace combined.
	PersonalRateLimitPerMinute int       `json:"personal_rate_limit_per_minute" gorm:"not null;default:60"`
	CreatedAt                  time.Time `json:"created_at"`
	UpdatedAt                  time.Time `json:"updated_at"`
}

// TableName returns the storage table.
func (MCPEndpointSettings) TableName() string { return "mcp_endpoint_settings" }

// DefaultMCPEndpointRateLimitPerMinute bounds tool calls per endpoint per
// minute when the row does not say otherwise.
const DefaultMCPEndpointRateLimitPerMinute = 60

// MCPEndpointTokenPrefix distinguishes endpoint tokens from tenant API keys
// (sk-) and embed publish tokens (em_) at a glance.
const MCPEndpointTokenPrefix = "mcp_"

// BeforeCreate assigns the id and fills defaults before insert.
func (e *MCPEndpoint) BeforeCreate(_ *gorm.DB) error {
	if e.ID == "" {
		e.ID = uuid.New().String()
	}
	if e.Kind == "" {
		e.Kind = MCPEndpointKindSpace
	}
	if e.RateLimitPerMinute <= 0 {
		e.RateLimitPerMinute = DefaultMCPEndpointRateLimitPerMinute
	}
	if e.KnowledgeBaseIDs == nil {
		e.KnowledgeBaseIDs = StringArray{}
	}
	if e.Tools == nil {
		e.Tools = StringArray{}
	}
	return nil
}

// HasTool reports whether the endpoint exposes the named tool.
func (e *MCPEndpoint) HasTool(name string) bool {
	if e == nil {
		return false
	}
	name = strings.TrimSpace(name)
	for _, t := range e.Tools {
		if t == name {
			return true
		}
	}
	return false
}

// RestrictsKnowledgeBases reports whether the endpoint pins an explicit
// knowledge-base allowlist rather than inheriting the whole workspace.
func (e *MCPEndpoint) RestrictsKnowledgeBases() bool {
	return e != nil && len(e.KnowledgeBaseIDs) > 0
}

// AllowsKnowledgeBase reports whether kbID is inside the endpoint scope. An
// endpoint without an allowlist admits every knowledge base; ownership is
// still checked by the knowledge services.
func (e *MCPEndpoint) AllowsKnowledgeBase(kbID string) bool {
	if e == nil {
		return false
	}
	if len(e.KnowledgeBaseIDs) == 0 {
		return true
	}
	kbID = strings.TrimSpace(kbID)
	for _, id := range e.KnowledgeBaseIDs {
		if id == kbID {
			return true
		}
	}
	return false
}
