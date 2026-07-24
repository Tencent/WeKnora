package types

import (
	"context"
	"fmt"
	"strings"
)

const (
	PrincipalWebUser         = "web_user"
	PrincipalAPITenant       = "api_tenant"
	PrincipalAPIPlatform     = "api_platform"
	PrincipalAPIExternalUser = "api_external_user"
	PrincipalIMUser          = "im_user"
	PrincipalEmbedChannel    = "embed_channel"
	PrincipalEmbedSession    = "embed_session"
	PrincipalEmbedVisitor    = "embed_visitor"
)

// EmbedVisitorHeader is sent by the embed widget to identify a browser visitor
// across chat sessions within the same channel.
const EmbedVisitorHeader = "X-Embed-Visitor"

// SessionOwnerAPITenantKeyPrefix prefixes sessions.user_id for rows created by a
// tenant API key. The full owner id is "api_tenant_key:<tenantID>:<keyID>", so a
// LIKE '<prefix>%' selects every API-key session in the tenant.
const SessionOwnerAPITenantKeyPrefix = "api_tenant_key:"

// Principal represents the terminal caller for per-subject isolation features.
// It is intentionally separate from UserID: many principals, such as IM users
// or embed visitors, are not WeKnora accounts and must not imply RBAC rights.
type Principal struct {
	Type string
	ID   string
}

func (p Principal) Normalize() Principal {
	return Principal{
		Type: strings.TrimSpace(p.Type),
		ID:   strings.TrimSpace(p.ID),
	}
}

func (p Principal) Valid() bool {
	p = p.Normalize()
	return p.Type != "" && p.ID != ""
}

func (p Principal) StorageID() string {
	p = p.Normalize()
	if !p.Valid() {
		return ""
	}
	return p.Type + ":" + p.ID
}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	principal = principal.Normalize()
	if !principal.Valid() {
		return ctx
	}
	return context.WithValue(ctx, PrincipalContextKey, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return Principal{}, false
	}
	if p, ok := ctx.Value(PrincipalContextKey).(Principal); ok && p.Valid() {
		return p.Normalize(), true
	}
	if uid, ok := UserIDFromContext(ctx); ok && strings.TrimSpace(uid) != "" {
		return Principal{Type: PrincipalWebUser, ID: uid}, true
	}
	return Principal{}, false
}

func WithEmbedVisitorID(ctx context.Context, visitorID string) context.Context {
	visitorID = strings.TrimSpace(visitorID)
	if visitorID == "" {
		return ctx
	}
	return context.WithValue(ctx, EmbedVisitorContextKey, visitorID)
}

func EmbedVisitorIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(EmbedVisitorContextKey).(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// EmbedSessionPrincipal identifies a single embed visitor chat session.
func EmbedSessionPrincipal(tenantID uint64, channelID, sessionID string) Principal {
	return Principal{
		Type: PrincipalEmbedSession,
		ID:   fmt.Sprintf("%d:%s:%s", tenantID, strings.TrimSpace(channelID), strings.TrimSpace(sessionID)),
	}
}

// EmbedVisitorPrincipal identifies one anonymous embed visitor (browser).
func EmbedVisitorPrincipal(tenantID uint64, channelID, visitorID string) Principal {
	return Principal{
		Type: PrincipalEmbedVisitor,
		ID:   fmt.Sprintf("%d:%s:%s", tenantID, strings.TrimSpace(channelID), strings.TrimSpace(visitorID)),
	}
}

// ValidateEmbedVisitorID checks the client-supplied anonymous visitor id.
func ValidateEmbedVisitorID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("empty embed visitor id")
	}
	if len(id) > 128 {
		return fmt.Errorf("embed visitor id too long (max 128)")
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("embed visitor id contains invalid characters")
		}
	}
	return nil
}

// MCPOAuthPrincipalFromContext resolves the OAuth token principal for ctx.
// Embed chat sessions map to a per-visitor principal when X-Embed-Visitor is
// present; otherwise OAuth falls back to the chat session principal.
func MCPOAuthPrincipalFromContext(ctx context.Context) Principal {
	p, ok := PrincipalFromContext(ctx)
	if !ok {
		return Principal{}
	}
	p = p.Normalize()
	if p.Type != PrincipalEmbedSession {
		return p
	}
	visitorID := EmbedVisitorIDFromContext(ctx)
	if visitorID == "" {
		return p
	}
	parts := strings.SplitN(p.ID, ":", 3)
	if len(parts) < 2 {
		return p
	}
	tenantPart := strings.TrimSpace(parts[0])
	channelPart := strings.TrimSpace(parts[1])
	if tenantPart == "" || channelPart == "" {
		return p
	}
	var tenantID uint64
	if _, err := fmt.Sscanf(tenantPart, "%d", &tenantID); err != nil || tenantID == 0 {
		return Principal{Type: PrincipalEmbedVisitor, ID: tenantPart + ":" + channelPart + ":" + visitorID}
	}
	return EmbedVisitorPrincipal(tenantID, channelPart, visitorID)
}

// SessionOwnerIDFromContext returns the sessions.user_id scope for the current
// caller. API external users and embed chat sessions use principal-derived IDs;
// tenant API keys are isolated per key id; MCP OAuth token storage uses
// MCPOAuthPrincipalFromContext (visitor-level for embed).
func SessionOwnerIDFromContext(ctx context.Context) string {
	if p, ok := PrincipalFromContext(ctx); ok {
		switch p.Type {
		case PrincipalAPIExternalUser, PrincipalEmbedSession:
			return p.StorageID()
		case PrincipalAPITenant:
			if scope, ok := TenantAPIKeyScopeFromContext(ctx); ok && scope.KeyID > 0 {
				if tenantID, ok := TenantIDFromContext(ctx); ok && tenantID > 0 {
					return fmt.Sprintf("%s%d:%d", SessionOwnerAPITenantKeyPrefix, tenantID, scope.KeyID)
				}
			}
		}
	}
	userID, _ := UserIDFromContext(ctx)
	return userID
}
