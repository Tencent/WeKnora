package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// ErrCustomAgentNotFound is returned when a custom agent is not found
var ErrCustomAgentNotFound = errors.New("custom agent not found")

// customAgentRepository implements the CustomAgentRepository interface
type customAgentRepository struct {
	db *gorm.DB
}

// NewCustomAgentRepository creates a new custom agent repository
func NewCustomAgentRepository(db *gorm.DB) interfaces.CustomAgentRepository {
	return &customAgentRepository{db: db}
}

// CreateAgent creates a new custom agent
func (r *customAgentRepository) CreateAgent(ctx context.Context, agent *types.CustomAgent) error {
	return r.db.WithContext(ctx).Create(agent).Error
}

// GetAgentByID gets an agent by id and tenant
func (r *customAgentRepository) GetAgentByID(ctx context.Context, id string, tenantID uint64) (*types.CustomAgent, error) {
	var agent types.CustomAgent
	if err := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&agent).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrCustomAgentNotFound
		}
		return nil, err
	}
	return &agent, nil
}

// ListAgentsByTenantID lists all agents for a specific tenant
func (r *customAgentRepository) ListAgentsByTenantID(ctx context.Context, tenantID uint64) ([]*types.CustomAgent, error) {
	var agents []*types.CustomAgent
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("created_at DESC").
		Find(&agents).Error; err != nil {
		return nil, err
	}
	return agents, nil
}

// UpdateAgent updates an agent
func (r *customAgentRepository) UpdateAgent(ctx context.Context, agent *types.CustomAgent) error {
	return r.db.WithContext(ctx).Save(agent).Error
}

// DeleteAgent deletes an agent (soft delete)
func (r *customAgentRepository) DeleteAgent(ctx context.Context, id string, tenantID uint64) error {
	return r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&types.CustomAgent{}).Error
}

// CountByModelID counts active agents whose config references modelID.
func (r *customAgentRepository) CountByModelID(
	ctx context.Context, tenantID uint64, modelID string,
) (int64, error) {
	var count int64
	query := r.db.WithContext(ctx).
		Model(&types.CustomAgent{}).
		Where("tenant_id = ?", tenantID)
	query = scopeCustomAgentsByModelID(query, modelID)
	err := query.Count(&count).Error
	return count, err
}
