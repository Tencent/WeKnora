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
	Role             TenantRole  `json:"role" gorm:"type:varchar(32);not null;default:'viewer'"`
	KnowledgeBaseIDs StringArray `json:"knowledge_base_ids" gorm:"type:jsonb;not null;default:'[]'"`
	LastUsedAt       *time.Time  `json:"last_used_at,omitempty"`
	ExpiresAt        *time.Time  `json:"expires_at,omitempty"`
	RevokedAt        *time.Time  `json:"revoked_at,omitempty" gorm:"index"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
}

func (TenantAPIKey) TableName() string {
	return "tenant_api_keys"
}

func (k *TenantAPIKey) BeforeSave(tx *gorm.DB) error {
	if key := utils.GetAESKey(); key != nil && k.APIKey != "" {
		if encrypted, err := utils.EncryptAESGCM(k.APIKey, key); err == nil {
			tx.Statement.SetColumn("api_key", encrypted)
		}
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
	Role             TenantRole
	KnowledgeBaseIDs StringArray
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
		Role:             NormalizeTenantAPIKeyRole(s.Role),
		KnowledgeBaseIDs: normalizeIDArray(s.KnowledgeBaseIDs),
	}
}

// NormalizeTenantAPIKeyRole maps stored/API role strings to tenant RBAC roles.
func NormalizeTenantAPIKeyRole(role TenantRole) TenantRole {
	switch strings.ToLower(strings.TrimSpace(string(role))) {
	case string(TenantRoleAdmin):
		return TenantRoleAdmin
	case string(TenantRoleContributor):
		return TenantRoleContributor
	case string(TenantRoleViewer), "":
		return TenantRoleViewer
	default:
		return ""
	}
}

func (s TenantAPIKeyScope) TenantRole() TenantRole {
	return s.Normalize().Role
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
	if !scope.AllowsKnowledgeBases(kbIDs) {
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
	if !scope.AllowsKnowledgeBases(kbIDs) {
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
