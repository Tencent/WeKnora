package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type agentGrantRepository struct {
	db *gorm.DB
}

// NewAgentGrantRepository creates a repository backed by GORM.
func NewAgentGrantRepository(db *gorm.DB) interfaces.AgentGrantRepository {
	return &agentGrantRepository{db: db}
}

func (r *agentGrantRepository) FindGrant(
	ctx context.Context, tenantID uint64, userID string, scope types.GrantScope, key string,
) (*types.AgentGrant, error) {
	var grant types.AgentGrant
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND user_id = ? AND scope = ? AND scope_id = ? AND grant_key = ?",
			tenantID, userID, scope.Scope, scope.ScopeID, key).
		First(&grant).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find agent grant: %w", err)
	}
	return &grant, nil
}

func (r *agentGrantRepository) SaveGrant(ctx context.Context, grant *types.AgentGrant) error {
	if grant == nil || grant.TenantID == 0 || grant.UserID == "" || grant.Scope == "" ||
		grant.ScopeID == "" || grant.GrantKey == "" {
		return errors.New("agent grant identity is required")
	}
	if grant.Risk == "" {
		grant.Risk = "write"
	}
	now := time.Now()
	if grant.CreatedAt.IsZero() {
		grant.CreatedAt = now
	}
	grant.UpdatedAt = now
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"}, {Name: "user_id"}, {Name: "scope"}, {Name: "scope_id"}, {Name: "grant_key"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"label":        grant.Label,
			"binding_hash": grant.BindingHash,
			"risk":         grant.Risk,
			"prompt_id":    grant.PromptID,
			"created_at":   grant.CreatedAt,
			"updated_at":   now,
			"last_used_at": nil,
			"expires_at":   grant.ExpiresAt,
			"revoked_at":   nil,
		}),
	}).Create(grant).Error
	if err != nil {
		return fmt.Errorf("save agent grant: %w", err)
	}
	return nil
}

func (r *agentGrantRepository) TouchGrant(ctx context.Context, id string, at time.Time) error {
	return r.db.WithContext(ctx).Model(&types.AgentGrant{}).Where("id = ?", id).
		UpdateColumn("last_used_at", at).Error
}

func (r *agentGrantRepository) ListGrants(
	ctx context.Context, tenantID uint64, userID, scope, scopeID string,
) ([]*types.AgentGrant, error) {
	var grants []*types.AgentGrant
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND user_id = ? AND scope = ? AND scope_id = ? AND revoked_at IS NULL",
			tenantID, userID, scope, scopeID).
		Order("created_at DESC").
		Find(&grants).Error
	if err != nil {
		return nil, fmt.Errorf("list agent grants: %w", err)
	}
	return grants, nil
}

func (r *agentGrantRepository) RevokeGrant(
	ctx context.Context, tenantID uint64, userID, id string, at time.Time,
) (bool, error) {
	res := r.db.WithContext(ctx).Model(&types.AgentGrant{}).
		Where("id = ? AND tenant_id = ? AND user_id = ? AND revoked_at IS NULL", id, tenantID, userID).
		Updates(map[string]interface{}{"revoked_at": at, "updated_at": at})
	if res.Error != nil {
		return false, fmt.Errorf("revoke agent grant: %w", res.Error)
	}
	return res.RowsAffected > 0, nil
}

func (r *agentGrantRepository) DeleteScopes(
	ctx context.Context, tenantID uint64, scope string, scopeIDs []string,
) error {
	if len(scopeIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope = ? AND scope_id IN ?", tenantID, scope, scopeIDs).
		Delete(&types.AgentGrant{}).Error
}
