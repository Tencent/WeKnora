package repository

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type tenantDisabledSharedAgentRepository struct {
	db *gorm.DB
}

// NewTenantDisabledSharedAgentRepository creates a new repository
func NewTenantDisabledSharedAgentRepository(db *gorm.DB) interfaces.TenantDisabledSharedAgentRepository {
	return &tenantDisabledSharedAgentRepository{db: db}
}

func (r *tenantDisabledSharedAgentRepository) ListByTenantID(ctx context.Context, tenantID uint64) ([]*types.TenantDisabledSharedAgent, error) {
	var list []*types.TenantDisabledSharedAgent
	err := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Find(&list).Error
	return list, err
}

func (r *tenantDisabledSharedAgentRepository) ListDisabledOwnAgentIDs(ctx context.Context, tenantID uint64) ([]string, error) {
	var ids []string
	err := r.db.WithContext(ctx).Model(&types.TenantDisabledSharedAgent{}).
		Where("tenant_id = ? AND source_tenant_id = ?", tenantID, tenantID).
		Pluck("agent_id", &ids).Error
	return ids, err
}

func (r *tenantDisabledSharedAgentRepository) Add(ctx context.Context, tenantID uint64, agentID string, sourceTenantID uint64) error {
	rec := &types.TenantDisabledSharedAgent{
		TenantID:       tenantID,
		AgentID:        agentID,
		SourceTenantID: sourceTenantID,
	}
	return r.db.WithContext(ctx).Where(rec).FirstOrCreate(rec).Error
}

func (r *tenantDisabledSharedAgentRepository) Remove(ctx context.Context, tenantID uint64, agentID string, sourceTenantID uint64) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND agent_id = ? AND source_tenant_id = ?", tenantID, agentID, sourceTenantID).
		Delete(&types.TenantDisabledSharedAgent{}).Error
}
