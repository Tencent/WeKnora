package types

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Agent grant scopes. A session grant lives as long as its root
// conversation; an automation grant lives with its automation and stops
// applying when the automation's binding changes.
const (
	AgentGrantScopeSession    = "session"
	AgentGrantScopeAutomation = "automation"
)

// AgentGrantIdleExpiry is how long an automation grant survives without use.
const AgentGrantIdleExpiry = 90 * 24 * time.Hour

// AgentGrant remembers that a user allowed one kind of agent action without
// asking again. GrantKey names the action and its resource scope; Label is
// how the approval card described it.
type AgentGrant struct {
	ID       string `json:"id"        gorm:"type:varchar(36);primaryKey"`
	TenantID uint64 `json:"tenant_id" gorm:"not null;uniqueIndex:idx_agent_grants_key"`
	// UserID is the principal storage ID of the user who granted it.
	UserID  string `json:"-"        gorm:"type:varchar(128);not null;uniqueIndex:idx_agent_grants_key"`
	Scope   string `json:"scope"    gorm:"type:varchar(16);not null;uniqueIndex:idx_agent_grants_key"`
	ScopeID string `json:"scope_id" gorm:"type:varchar(64);not null;uniqueIndex:idx_agent_grants_key"`
	// GrantKey is what the grant allows; see approval.PendingRequest.Grant.
	GrantKey string `json:"grant_key" gorm:"type:varchar(512);not null;uniqueIndex:idx_agent_grants_key"`
	Label    string `json:"label"     gorm:"type:text"`
	// BindingHash, when set, is the configuration the grant was given
	// under; a run with a different one does not get it.
	BindingHash string `json:"-"            gorm:"type:varchar(64);not null;default:''"`
	// Risk is the highest risk the grant was approved for; a call that has
	// since become riskier is not covered.
	Risk       string     `json:"risk"         gorm:"type:varchar(16);not null;default:'write'"`
	PromptID   string     `json:"prompt_id"    gorm:"type:varchar(36)"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

// BeforeCreate sets the ID.
func (g *AgentGrant) BeforeCreate(*gorm.DB) error {
	if g.ID == "" {
		g.ID = uuid.NewString()
	}
	return nil
}

// Why a remembered grant no longer applies. The approval card shows it so
// the user knows why they are asked again.
const (
	AgentGrantLapseRevoked = "revoked"
	AgentGrantLapseExpired = "expired"
	// AgentGrantLapseChanged: the run's configuration differs from the one
	// the grant was given under.
	AgentGrantLapseChanged = "changed"
	// AgentGrantLapseIdle: an automation grant went unused too long.
	AgentGrantLapseIdle = "idle"
)

// ActiveAt reports whether the grant applies at now under binding.
func (g *AgentGrant) ActiveAt(now time.Time, binding string) bool {
	return g != nil && g.LapseAt(now, binding) == ""
}

// LapseAt is why an existing grant does not apply at now under binding, or
// "" when it does.
func (g *AgentGrant) LapseAt(now time.Time, binding string) string {
	switch {
	case g.RevokedAt != nil:
		return AgentGrantLapseRevoked
	case g.ExpiresAt != nil && !now.Before(*g.ExpiresAt):
		return AgentGrantLapseExpired
	case g.BindingHash != binding:
		return AgentGrantLapseChanged
	}
	if g.Scope == AgentGrantScopeAutomation {
		used := g.CreatedAt
		if g.LastUsedAt != nil {
			used = *g.LastUsedAt
		}
		if now.Sub(used) >= AgentGrantIdleExpiry {
			return AgentGrantLapseIdle
		}
	}
	return ""
}

// GrantScope is where a run's grants are kept and looked up.
type GrantScope struct {
	Scope   string
	ScopeID string
	// BindingHash is the run's configuration; see AgentGrant.BindingHash.
	BindingHash string
}

// WithGrantScope records where the run's grants live.
func WithGrantScope(ctx context.Context, scope GrantScope) context.Context {
	return context.WithValue(ctx, GrantScopeContextKey, scope)
}

// GrantScopeFromContext is the run's grant scope, if it has one.
func GrantScopeFromContext(ctx context.Context) (GrantScope, bool) {
	scope, ok := ctx.Value(GrantScopeContextKey).(GrantScope)
	return scope, ok && scope.Scope != "" && scope.ScopeID != ""
}

// WithStandingGrantScope records grants the run may use but never adds to:
// an automation's, approved in its settings. Approvals given during the run
// go to the run's own scope.
func WithStandingGrantScope(ctx context.Context, scope GrantScope) context.Context {
	return context.WithValue(ctx, StandingGrantScopeContextKey, scope)
}

// StandingGrantScopeFromContext is the run's standing grant scope, if any.
func StandingGrantScopeFromContext(ctx context.Context) (GrantScope, bool) {
	scope, ok := ctx.Value(StandingGrantScopeContextKey).(GrantScope)
	return scope, ok && scope.Scope != "" && scope.ScopeID != ""
}

// AutomationBinding is what an automation's grants were given under. A
// grant stops applying once any of it changes: the agent's tools, MCP
// services or instructions, the owner's workspace role, or the agent's
// access ceiling.
type AutomationBinding struct {
	AgentID       string               `json:"agent_id"`
	Tools         []string             `json:"tools"`
	MCPServiceIDs []string             `json:"mcp_service_ids"`
	SystemPrompt  string               `json:"system_prompt"`
	OwnerRole     TenantRole           `json:"owner_role"`
	Ceiling       AssistantAccessLevel `json:"ceiling"`
}

// Hash is the binding as stored in AgentGrant.BindingHash. Tool and
// service order does not matter.
func (b AutomationBinding) Hash() string {
	b.Tools = slices.Sorted(slices.Values(b.Tools))
	b.MCPServiceIDs = slices.Sorted(slices.Values(b.MCPServiceIDs))
	data, _ := json.Marshal(b)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
