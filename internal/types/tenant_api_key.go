package types

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/utils"
	"gorm.io/gorm"
)

// TenantAPIKey is a revocable, per-tenant API key. KeyHash is used for
// authentication lookup; APIKey is stored encrypted when SYSTEM_AES_KEY is set
// and returned by owner-only management APIs.
type TenantAPIKey struct {
	ID               uint64      `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID         uint64      `json:"tenant_id" gorm:"not null;index"`
	Name             string      `json:"name" gorm:"type:varchar(128);not null"`
	KeyHash          string      `json:"-" gorm:"type:varchar(64);not null;uniqueIndex"`
	APIKey           string      `json:"api_key" gorm:"column:api_key;type:text;not null;default:''"`
	FullAccess       bool        `json:"full_access" gorm:"not null;default:false"`
	KnowledgeBaseIDs StringArray `json:"knowledge_base_ids" gorm:"type:jsonb;not null;default:'[]'"`
	// Capabilities are bounded grants for non-full-access keys. Each
	// capability maps to an integration persona (retrieval, chat, ingest,
	// tenant infrastructure management, and history access). KB scoping
	// (KnowledgeBaseIDs) still applies on top where a route targets knowledge
	// bases.
	Capabilities StringArray `json:"capabilities" gorm:"type:jsonb;not null;default:'[]'"`
	LastUsedAt   *time.Time  `json:"last_used_at,omitempty"`
	ExpiresAt    *time.Time  `json:"expires_at,omitempty"`
	RevokedAt    *time.Time  `json:"revoked_at,omitempty" gorm:"index"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
}

func (TenantAPIKey) TableName() string {
	return "tenant_api_keys"
}

// APIKeyCapability is a bounded grant on a tenant API key. See
// TenantAPIKey.Capabilities.
type APIKeyCapability string

const (
	// APIKeyCapabilityRetrieve lets a key read and search knowledge-base data
	// within its allowed KB scope without granting chat or content writes.
	APIKeyCapabilityRetrieve APIKeyCapability = "retrieve"
	// APIKeyCapabilityChat lets a scoped key run the conversation flow
	// (sessions + agent listing + self identity) without granting broader
	// tenant management access.
	APIKeyCapabilityChat APIKeyCapability = "chat"
	// APIKeyCapabilityReadAgents lets a scoped key list and inspect agents
	// without allowing chat sessions or agent authoring.
	APIKeyCapabilityReadAgents APIKeyCapability = "read_agents"
	// APIKeyCapabilityIngest lets a key write content into its allowed
	// knowledge bases (upload documents, edit chunks/FAQ/tags/wiki). It only
	// lifts the content-write routes: it never allows creating new knowledge
	// bases or agents, nor destructive KB clears, and the key's
	// knowledge_base_ids allow-list still bounds every write.
	APIKeyCapabilityIngest APIKeyCapability = "ingest"
	// APIKeyCapabilityManageKnowledgeBases lets a scoped key manage existing
	// knowledge-base metadata/configuration within its allowed KB scope. It is
	// separate from ingest: uploading or editing KB contents does not imply
	// permission to rename, reconfigure, or delete the KB itself.
	APIKeyCapabilityManageKnowledgeBases APIKeyCapability = "manage_kbs"
	// APIKeyCapabilityManageAgents lets a key create/read/update/delete/copy
	// agents. Agent config can carry sensitive model/MCP bindings, so this is
	// opt-in and off by default.
	APIKeyCapabilityManageAgents APIKeyCapability = "manage_agents"
	// APIKeyCapabilityMessageHistory lets a key search and inspect the
	// tenant-level chat-history knowledge base without granting full Owner
	// access. It is separate from chat: chat only covers the caller's own
	// live conversation flow, while message_history can expose historical
	// messages across the tenant.
	APIKeyCapabilityMessageHistory APIKeyCapability = "message_history"
	// APIKeyCapabilityManageModels lets a key manage tenant model
	// definitions, credentials, model checks, and WeKnoraCloud credentials.
	APIKeyCapabilityManageModels APIKeyCapability = "manage_models"
	// APIKeyCapabilityManageMCPServices lets a key manage tenant MCP service
	// definitions, credentials, tool policies, and per-principal OAuth state.
	APIKeyCapabilityManageMCPServices APIKeyCapability = "manage_mcp_services"
	// APIKeyCapabilityManageDataSources lets a key manage data-source
	// connectors and sync jobs. KB scoping applies to data sources bound to a
	// knowledge base.
	APIKeyCapabilityManageDataSources APIKeyCapability = "manage_datasources"
	// APIKeyCapabilityManageChannels lets a key manage embed and IM channel
	// integrations for agents.
	APIKeyCapabilityManageChannels APIKeyCapability = "manage_channels"
	// APIKeyCapabilityManageVectorStores lets a key manage retrieval
	// infrastructure such as vector stores, parser engines, and storage checks.
	APIKeyCapabilityManageVectorStores APIKeyCapability = "manage_vector_stores"
	// APIKeyCapabilityManageWebSearch lets a key manage tenant web-search
	// provider configurations and credentials.
	APIKeyCapabilityManageWebSearch APIKeyCapability = "manage_web_search"
	// APIKeyCapabilityRunEvaluations lets a key run and inspect evaluation
	// jobs without full tenant ownership.
	APIKeyCapabilityRunEvaluations APIKeyCapability = "run_evaluations"
	// APIKeyCapabilityManageMembers lets a key list and manage tenant
	// members and invitations. It does not include API key management,
	// tenant deletion, or ownership transfer.
	APIKeyCapabilityManageMembers APIKeyCapability = "manage_members"
	// APIKeyCapabilityManageSpaces lets a key manage organization/space
	// collaboration surfaces such as space membership and join flows. It does
	// not grant KB/agent share management: share management is reserved for
	// full-access keys (and JWT) and scoped keys stay default-deny. This
	// capability never lifts it.
	APIKeyCapabilityManageSpaces APIKeyCapability = "manage_spaces"
	// APIKeyCapabilityManageTenantSettings lets a key read and update
	// tenant-scoped integration settings exposed under /tenants, such as API
	// principal mode, request headers, and tenant KV. It does not include API
	// key management, member management, tenant deletion, or ownership transfer.
	APIKeyCapabilityManageTenantSettings APIKeyCapability = "manage_tenant_settings"
)

// NormalizeAPIKeyCapability maps an input capability string to a known
// capability, returning "" for anything unrecognised so callers can drop it.
func NormalizeAPIKeyCapability(c APIKeyCapability) APIKeyCapability {
	switch APIKeyCapability(strings.ToLower(strings.TrimSpace(string(c)))) {
	case APIKeyCapabilityRetrieve:
		return APIKeyCapabilityRetrieve
	case APIKeyCapabilityChat:
		return APIKeyCapabilityChat
	case APIKeyCapabilityReadAgents:
		return APIKeyCapabilityReadAgents
	case APIKeyCapabilityIngest:
		return APIKeyCapabilityIngest
	case APIKeyCapabilityManageKnowledgeBases:
		return APIKeyCapabilityManageKnowledgeBases
	case APIKeyCapabilityManageAgents:
		return APIKeyCapabilityManageAgents
	case APIKeyCapabilityMessageHistory:
		return APIKeyCapabilityMessageHistory
	case APIKeyCapabilityManageModels:
		return APIKeyCapabilityManageModels
	case APIKeyCapabilityManageMCPServices:
		return APIKeyCapabilityManageMCPServices
	case APIKeyCapabilityManageDataSources:
		return APIKeyCapabilityManageDataSources
	case APIKeyCapabilityManageChannels:
		return APIKeyCapabilityManageChannels
	case APIKeyCapabilityManageVectorStores:
		return APIKeyCapabilityManageVectorStores
	case APIKeyCapabilityManageWebSearch:
		return APIKeyCapabilityManageWebSearch
	case APIKeyCapabilityRunEvaluations:
		return APIKeyCapabilityRunEvaluations
	case APIKeyCapabilityManageMembers:
		return APIKeyCapabilityManageMembers
	case APIKeyCapabilityManageSpaces:
		return APIKeyCapabilityManageSpaces
	case APIKeyCapabilityManageTenantSettings:
		return APIKeyCapabilityManageTenantSettings
	default:
		return ""
	}
}

// NormalizeAPIKeyCapabilities dedups and drops unknown capabilities.
func NormalizeAPIKeyCapabilities(in StringArray) StringArray {
	out := make(StringArray, 0, len(in))
	seen := map[string]struct{}{}
	for _, item := range in {
		norm := NormalizeAPIKeyCapability(APIKeyCapability(item))
		if norm == "" {
			continue
		}
		s := string(norm)
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func (k *TenantAPIKey) BeforeSave(tx *gorm.DB) error {
	if key := utils.GetAESKey(); key != nil && k.APIKey != "" {
		encrypted, err := utils.EncryptAESGCM(k.APIKey, key)
		if err != nil {
			// Never fall through to storing the plaintext key: abort the
			// write so the caller sees the failure instead of silently
			// persisting an unencrypted secret.
			return fmt.Errorf("encrypt tenant_api_keys.api_key (id=%d): %w", k.ID, err)
		}
		tx.Statement.SetColumn("api_key", encrypted)
	}
	return nil
}

func (k *TenantAPIKey) AfterFind(tx *gorm.DB) error {
	decrypted, err := utils.DecryptStoredSecret(k.APIKey)
	if err != nil {
		return fmt.Errorf("decrypt tenant_api_keys.api_key (id=%d): %w", k.ID, err)
	}
	k.APIKey = decrypted
	return nil
}

// TenantAPIKeyScope is the request-context projection used by middleware.
type TenantAPIKeyScope struct {
	KeyID            uint64
	FullAccess       bool
	KnowledgeBaseIDs StringArray
	Capabilities     StringArray
}

func WithTenantAPIKeyScope(ctx context.Context, scope TenantAPIKeyScope) context.Context {
	return context.WithValue(ctx, TenantAPIKeyScopeContextKey, scope.Normalize())
}

func TenantAPIKeyScopeFromContext(ctx context.Context) (TenantAPIKeyScope, bool) {
	if ctx == nil {
		return TenantAPIKeyScope{}, false
	}
	scope, ok := ctx.Value(TenantAPIKeyScopeContextKey).(TenantAPIKeyScope)
	if !ok {
		return TenantAPIKeyScope{}, false
	}
	return scope.Normalize(), true
}

func (s TenantAPIKeyScope) Normalize() TenantAPIKeyScope {
	return TenantAPIKeyScope{
		KeyID:            s.KeyID,
		FullAccess:       s.FullAccess,
		KnowledgeBaseIDs: normalizeIDArray(s.KnowledgeBaseIDs),
		Capabilities:     NormalizeAPIKeyCapabilities(s.Capabilities),
	}
}

// HasCapability reports whether the scope carries the given additive grant.
func (s TenantAPIKeyScope) HasCapability(c APIKeyCapability) bool {
	c = NormalizeAPIKeyCapability(c)
	if c == "" {
		return false
	}
	for _, item := range NormalizeAPIKeyCapabilities(s.Capabilities) {
		if item == string(c) {
			return true
		}
	}
	return false
}

func (s TenantAPIKeyScope) AllowsKnowledgeBase(kbID string) bool {
	kbID = strings.TrimSpace(kbID)
	if kbID == "" {
		return false
	}
	s = s.Normalize()
	if len(s.KnowledgeBaseIDs) == 0 {
		return true
	}
	for _, allowed := range s.KnowledgeBaseIDs {
		if allowed == kbID {
			return true
		}
	}
	return false
}

func (s TenantAPIKeyScope) IsKnowledgeBaseRestricted() bool {
	return len(s.Normalize().KnowledgeBaseIDs) > 0
}

func (s TenantAPIKeyScope) AllowsKnowledgeBases(kbIDs []string) bool {
	s = s.Normalize()
	if len(s.KnowledgeBaseIDs) == 0 {
		return true
	}
	if len(kbIDs) == 0 {
		return false
	}
	for _, kbID := range kbIDs {
		if !s.AllowsKnowledgeBase(kbID) {
			return false
		}
	}
	return true
}

func normalizeIDArray(in StringArray) StringArray {
	out := make(StringArray, 0, len(in))
	seen := map[string]struct{}{}
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

// AuthorizeTenantAPIKeyKnowledgeBases rejects KB-restricted API key callers that
// target one or more knowledge bases outside their allow-list.
func AuthorizeTenantAPIKeyKnowledgeBases(ctx context.Context, kbIDs ...string) error {
	scope, ok := TenantAPIKeyScopeFromContext(ctx)
	if !ok || !scope.IsKnowledgeBaseRestricted() {
		return nil
	}
	if len(kbIDs) > 0 && !scope.AllowsKnowledgeBases(kbIDs) {
		return errors.NewForbiddenError("API key scope does not allow one or more knowledge bases")
	}
	return nil
}

// AuthorizeTenantAPIKeyKnowledgeTargets rejects KB-restricted API key callers
// that reference knowledge_ids without a verified KB binding, or kb_ids outside
// the allow-list.
func AuthorizeTenantAPIKeyKnowledgeTargets(ctx context.Context, kbIDs, knowledgeIDs []string) error {
	scope, ok := TenantAPIKeyScopeFromContext(ctx)
	if !ok || !scope.IsKnowledgeBaseRestricted() {
		return nil
	}
	if len(knowledgeIDs) > 0 {
		return errors.NewForbiddenError("API key scope does not allow knowledge_ids without a verified knowledge base")
	}
	if len(kbIDs) > 0 && !scope.AllowsKnowledgeBases(kbIDs) {
		return errors.NewForbiddenError("API key scope does not allow one or more knowledge bases")
	}
	return nil
}

// AuthorizeTenantAPIKeyOptionalTagIDs rejects tag_ids for KB-restricted keys
// because tag resolution can pull documents from arbitrary knowledge bases.
func AuthorizeTenantAPIKeyOptionalTagIDs(ctx context.Context, tagIDs []string) error {
	scope, ok := TenantAPIKeyScopeFromContext(ctx)
	if !ok || !scope.IsKnowledgeBaseRestricted() {
		return nil
	}
	if len(tagIDs) > 0 {
		return errors.NewForbiddenError("API key scope does not allow tag_ids without a verified knowledge base")
	}
	return nil
}

// FilterKnowledgeBasesForTenantAPIKeyScope intersects resolved KB IDs with the
// API key allow-list. When the caller supplied explicit kb_ids, every ID must
// be allowed; implicit agent defaults are intersected instead of rejected.
func FilterKnowledgeBasesForTenantAPIKeyScope(
	ctx context.Context, requestedKBIDs, resolvedKBIDs []string,
) ([]string, error) {
	scope, ok := TenantAPIKeyScopeFromContext(ctx)
	if !ok || !scope.IsKnowledgeBaseRestricted() {
		return resolvedKBIDs, nil
	}
	if len(requestedKBIDs) > 0 {
		if !scope.AllowsKnowledgeBases(requestedKBIDs) {
			return nil, errors.NewForbiddenError("API key scope does not allow one or more knowledge bases")
		}
		return resolvedKBIDs, nil
	}
	allowed := make(map[string]struct{}, len(scope.KnowledgeBaseIDs))
	for _, id := range scope.KnowledgeBaseIDs {
		allowed[id] = struct{}{}
	}
	filtered := make([]string, 0, len(resolvedKBIDs))
	for _, id := range resolvedKBIDs {
		if _, ok := allowed[id]; ok {
			filtered = append(filtered, id)
		}
	}
	return filtered, nil
}
