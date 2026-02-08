package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var (
	ErrAgentShareNotFound      = errors.New("agent share not found")
	ErrAgentShareAlreadyExists = errors.New("agent already shared to this organization")
)

// agentShareRepository implements AgentShareRepository interface
type agentShareRepository struct {
	db *gorm.DB
}

// NewAgentShareRepository creates a new agent share repository
func NewAgentShareRepository(db *gorm.DB) interfaces.AgentShareRepository {
	return &agentShareRepository{db: db}
}

// Create creates a new agent share record
func (r *agentShareRepository) Create(ctx context.Context, share *types.AgentShare) error {
	var count int64
	r.db.WithContext(ctx).Model(&types.AgentShare{}).
		Where("agent_id = ? AND source_tenant_id = ? AND organization_id = ? AND deleted_at IS NULL",
			share.AgentID, share.SourceTenantID, share.OrganizationID).
		Count(&count)
	if count > 0 {
		return ErrAgentShareAlreadyExists
	}
	return r.db.WithContext(ctx).Create(share).Error
}

// GetByID gets a share record by ID
func (r *agentShareRepository) GetByID(ctx context.Context, id string) (*types.AgentShare, error) {
	var share types.AgentShare
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&share).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAgentShareNotFound
		}
		return nil, err
	}
	return &share, nil
}

// GetByAgentAndOrg gets a share record by agent ID and organization ID
func (r *agentShareRepository) GetByAgentAndOrg(ctx context.Context, agentID string, orgID string) (*types.AgentShare, error) {
	var share types.AgentShare
	err := r.db.WithContext(ctx).
		Where("agent_id = ? AND organization_id = ?", agentID, orgID).
		First(&share).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAgentShareNotFound
		}
		return nil, err
	}
	return &share, nil
}

// Update updates a share record
func (r *agentShareRepository) Update(ctx context.Context, share *types.AgentShare) error {
	return r.db.WithContext(ctx).Model(&types.AgentShare{}).
		Where("id = ?", share.ID).Updates(share).Error
}

// Delete soft deletes a share record
func (r *agentShareRepository) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&types.AgentShare{}).Error
}

// DeleteByAgentIDAndSourceTenant soft deletes all share records for an agent (id, tenant_id)
func (r *agentShareRepository) DeleteByAgentIDAndSourceTenant(ctx context.Context, agentID string, sourceTenantID uint64) error {
	return r.db.WithContext(ctx).
		Where("agent_id = ? AND source_tenant_id = ?", agentID, sourceTenantID).
		Delete(&types.AgentShare{}).Error
}

// DeleteByOrganizationID soft deletes all share records for an organization
func (r *agentShareRepository) DeleteByOrganizationID(ctx context.Context, orgID string) error {
	return r.db.WithContext(ctx).Where("organization_id = ?", orgID).Delete(&types.AgentShare{}).Error
}

// ListByAgent lists all share records for an agent
func (r *agentShareRepository) ListByAgent(ctx context.Context, agentID string) ([]*types.AgentShare, error) {
	var shares []*types.AgentShare
	err := r.db.WithContext(ctx).
		Preload("Organization").
		Where("agent_id = ?", agentID).
		Order("created_at DESC").
		Find(&shares).Error
	if err != nil {
		return nil, err
	}
	return shares, nil
}

// ListByOrganization lists all share records for an organization (excluding deleted agents)
func (r *agentShareRepository) ListByOrganization(ctx context.Context, orgID string) ([]*types.AgentShare, error) {
	var shares []*types.AgentShare
	err := r.db.WithContext(ctx).
		Joins("JOIN custom_agents ON custom_agents.id = agent_shares.agent_id AND custom_agents.tenant_id = agent_shares.source_tenant_id AND custom_agents.deleted_at IS NULL").
		Preload("Agent").
		Preload("Organization").
		Where("agent_shares.organization_id = ? AND agent_shares.deleted_at IS NULL", orgID).
		Order("agent_shares.created_at DESC").
		Find(&shares).Error
	if err != nil {
		return nil, err
	}
	return shares, nil
}

// ListByOrganizations lists all share records for the given organizations (batch).
func (r *agentShareRepository) ListByOrganizations(ctx context.Context, orgIDs []string) ([]*types.AgentShare, error) {
	if len(orgIDs) == 0 {
		return nil, nil
	}
	var shares []*types.AgentShare
	err := r.db.WithContext(ctx).
		Joins("JOIN custom_agents ON custom_agents.id = agent_shares.agent_id AND custom_agents.tenant_id = agent_shares.source_tenant_id AND custom_agents.deleted_at IS NULL").
		Preload("Agent").
		Preload("Organization").
		Where("agent_shares.organization_id IN ? AND agent_shares.deleted_at IS NULL", orgIDs).
		Order("agent_shares.created_at DESC").
		Find(&shares).Error
	if err != nil {
		return nil, err
	}
	return shares, nil
}

// CountByOrganizations returns share counts per organization (only orgs in orgIDs). Excludes deleted agents.
func (r *agentShareRepository) CountByOrganizations(ctx context.Context, orgIDs []string) (map[string]int64, error) {
	if len(orgIDs) == 0 {
		return make(map[string]int64), nil
	}
	type row struct {
		OrgID string `gorm:"column:organization_id"`
		Count int64  `gorm:"column:count"`
	}
	var rows []row
	err := r.db.WithContext(ctx).Model(&types.AgentShare{}).
		Joins("JOIN custom_agents ON custom_agents.id = agent_shares.agent_id AND custom_agents.tenant_id = agent_shares.source_tenant_id AND custom_agents.deleted_at IS NULL").
		Select("agent_shares.organization_id as organization_id, COUNT(*) as count").
		Where("agent_shares.organization_id IN ? AND agent_shares.deleted_at IS NULL", orgIDs).
		Group("agent_shares.organization_id").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64)
	for _, o := range orgIDs {
		out[o] = 0
	}
	for _, r := range rows {
		out[r.OrgID] = r.Count
	}
	return out, nil
}

// ListSharedAgentsForUser lists all agents shared to organizations that the user belongs to
func (r *agentShareRepository) ListSharedAgentsForUser(ctx context.Context, userID string) ([]*types.AgentShare, error) {
	var shares []*types.AgentShare
	err := r.db.WithContext(ctx).
		Joins("JOIN custom_agents ON custom_agents.id = agent_shares.agent_id AND custom_agents.tenant_id = agent_shares.source_tenant_id AND custom_agents.deleted_at IS NULL").
		Preload("Agent").
		Preload("Organization").
		Joins("JOIN organization_members ON organization_members.organization_id = agent_shares.organization_id").
		Joins("JOIN organizations ON organizations.id = agent_shares.organization_id AND organizations.deleted_at IS NULL").
		Where("organization_members.user_id = ?", userID).
		Where("agent_shares.deleted_at IS NULL").
		Order("agent_shares.created_at DESC").
		Find(&shares).Error
	if err != nil {
		return nil, err
	}
	return shares, nil
}

// GetShareByAgentIDForUser returns one share for the given agentID that the user can access (user in org), excluding source_tenant_id == excludeTenantID. Single query.
func (r *agentShareRepository) GetShareByAgentIDForUser(ctx context.Context, userID, agentID string, excludeTenantID uint64) (*types.AgentShare, error) {
	var share types.AgentShare
	err := r.db.WithContext(ctx).
		Joins("JOIN organization_members ON organization_members.organization_id = agent_shares.organization_id").
		Where("agent_shares.agent_id = ?", agentID).
		Where("organization_members.user_id = ?", userID).
		Where("agent_shares.source_tenant_id != ?", excludeTenantID).
		Where("agent_shares.deleted_at IS NULL").
		First(&share).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAgentShareNotFound
		}
		return nil, err
	}
	return &share, nil
}
