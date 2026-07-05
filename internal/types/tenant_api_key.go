package types

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/utils"
	"gorm.io/gorm"
)

const (
	TenantAPIKeyScopeRead  = "read"
	TenantAPIKeyScopeWrite = "write"
	TenantAPIKeyScopeAdmin = "admin"
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
	Scopes           StringArray `json:"scopes" gorm:"type:jsonb;not null;default:'[]'"`
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
	Scopes           StringArray
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
		Scopes:           normalizeScopeArray(s.Scopes),
		KnowledgeBaseIDs: normalizeIDArray(s.KnowledgeBaseIDs),
	}
}

func (s TenantAPIKeyScope) HasScope(scope string) bool {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		return false
	}
	for _, item := range s.Normalize().Scopes {
		if item == scope {
			return true
		}
	}
	return false
}

// TenantRole maps API-key scopes to the tenant RBAC role carried in request
// context. Admin scope -> Admin; write -> Contributor; read-only -> Viewer.
func (s TenantAPIKeyScope) TenantRole() TenantRole {
	s = s.Normalize()
	switch {
	case s.HasScope(TenantAPIKeyScopeAdmin):
		return TenantRoleAdmin
	case s.HasScope(TenantAPIKeyScopeWrite):
		return TenantRoleContributor
	default:
		return TenantRoleViewer
	}
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

func normalizeScopeArray(in StringArray) StringArray {
	out := make(StringArray, 0, len(in))
	seen := map[string]struct{}{}
	for _, item := range in {
		item = strings.TrimSpace(strings.ToLower(item))
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	if len(out) == 0 {
		out = StringArray{TenantAPIKeyScopeRead}
	}
	return out
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
